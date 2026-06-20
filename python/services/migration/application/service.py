"""MigrationService — all migration use cases (A.3).

Depends only on domain types and ports. No infrastructure imports (A.9).
Behaviour mirrors the Go implementation; see docs/prism/python-parity-report.md
for documented deviations.
"""
from __future__ import annotations

from services.migration.domain.domain import (
    Migration,
    MigrationsFilter,
    MigrationState,
    MigrationStateAnalyzing,
    MigrationStateAwaitingApproval,
    MigrationStateCancelled,
    MigrationStateFailed,
    MigrationStateGenerating,
    MigrationStatePending,
    MigrationStatePushed,
    PageQueryParams,
    Pagination,
    TargetDatabaseUnspecified,
    TargetLanguageUnspecified,
)
from services.migration.domain.errors import (
    ERR_INTERNAL,
    ERR_INVALID_STATE_TRANSITION,
    ERR_INVALID_TARGET_CONFIG,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_OWNER_USER_ID,
    ERR_MISSING_PAYLOAD,
    ERR_MISSING_REPOSITORY_ID,
)
from services.migration.ports.analysis_client import AnalysisClient
from services.migration.ports.identity_client import IdentityClient
from services.migration.ports.migration import MigrationRepository
from services.migration.ports.repository_client import RepositoryClient
from services.migration.ports.transaction import TransactionManager
from shared.errors import DomainError
from shared.logging import infof


class MigrationService:
    """Orchestrates migration use cases. Assembled in wire.py (A.3)."""

    def __init__(
        self,
        repo: MigrationRepository,
        tx: TransactionManager,
        identity: IdentityClient | None,
        repository_svc: RepositoryClient | None,
        analysis: AnalysisClient,
    ) -> None:
        self._repo = repo
        self._tx = tx
        self._identity = identity
        self._repository_svc = repository_svc
        self._analysis = analysis

    async def create_migration(self, m: Migration) -> Migration:
        if m is None:
            raise ERR_MISSING_PAYLOAD
        if m.owner_user_id == 0:
            raise ERR_MISSING_OWNER_USER_ID
        if m.repository_id == 0:
            raise ERR_MISSING_REPOSITORY_ID
        if (
            m.target is None
            or m.target.language == TargetLanguageUnspecified
            or m.target.database == TargetDatabaseUnspecified
        ):
            raise ERR_INVALID_TARGET_CONFIG

        if self._identity is not None:
            try:
                await self._identity.validate_user_exists(m.owner_user_id)
            except DomainError:
                raise
            except Exception as exc:
                raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        if self._repository_svc is not None:
            try:
                await self._repository_svc.validate_repository_exists(m.repository_id)
            except DomainError:
                raise
            except Exception as exc:
                raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        m.state = MigrationStatePending

        out: Migration | None = None

        async def _create() -> None:
            nonlocal out
            try:
                out = await self._repo.create(m)
            except DomainError:
                raise
            except Exception as exc:
                raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        await self._tx.with_transaction(_create)
        assert out is not None
        infof("migration.create-migration", identifier=out.identifier)
        return out

    async def get_migration(self, identifier: int) -> Migration:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            return await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def list_migrations(
        self,
        filter: MigrationsFilter | None,
        params: PageQueryParams | None,
    ) -> tuple[list[Migration], Pagination]:
        if params is None:
            params = PageQueryParams(page_number=1, page_size=10)
        try:
            return await self._repo.list(filter, params)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def delete_migration(self, identifier: int) -> None:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            m = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if not _is_terminal(m.state):
            raise ERR_INVALID_STATE_TRANSITION
        try:
            await self._repo.soft_delete(identifier)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def start_migration(self, identifier: int) -> Migration:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            m = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if m.state != MigrationStatePending:
            raise ERR_INVALID_STATE_TRANSITION
        try:
            await self._repo.update_state(identifier, MigrationStateAnalyzing)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        m.state = MigrationStateAnalyzing
        return m

    async def approve_design(self, identifier: int, approved: bool) -> Migration:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            m = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if m.state != MigrationStateAwaitingApproval:
            raise ERR_INVALID_STATE_TRANSITION
        next_state: MigrationState = (
            MigrationStateGenerating if approved else MigrationStateCancelled
        )
        try:
            await self._repo.update_state(identifier, next_state)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        m.state = next_state
        return m

    async def cancel_migration(self, identifier: int) -> Migration:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            m = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if _is_terminal(m.state):
            raise ERR_INVALID_STATE_TRANSITION
        try:
            await self._repo.update_state(identifier, MigrationStateCancelled)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        m.state = MigrationStateCancelled
        return m


def _is_terminal(state: MigrationState) -> bool:
    """Terminal states: PUSHED, FAILED, CANCELLED — mirrors Go isTerminalState."""
    return state in (MigrationStatePushed, MigrationStateFailed, MigrationStateCancelled)
