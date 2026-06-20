"""RepositoryService — all repository use cases (A.3).

Depends only on domain types and ports. No infrastructure imports (A.9).
Behaviour mirrors the Go implementation; see docs/prism/python-parity-report.md
for documented deviations.
"""
from __future__ import annotations

from services.repository.domain.domain import (
    Branch,
    ConnectionStatus,
    ConnectionStatusUnreachable,
    GitProviderUnspecified,
    PageQueryParams,
    Pagination,
    RepositoriesFilter,
    Repository,
)
from services.repository.domain.errors import (
    ERR_INTERNAL,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_OWNER_USER_ID,
    ERR_MISSING_PAYLOAD,
)
from services.repository.ports.git_client import GitClient
from services.repository.ports.identity_client import IdentityClient
from services.repository.ports.repository import RepositoryRepository
from services.repository.ports.transaction import TransactionManager
from shared.errors import DomainError
from shared.logging import infof


class RepositoryService:
    """Orchestrates repository use cases. Assembled in wire.py (A.3)."""

    def __init__(
        self,
        repo: RepositoryRepository,
        tx: TransactionManager,
        identity: IdentityClient | None,
        git: GitClient,
    ) -> None:
        self._repo = repo
        self._tx = tx
        self._identity = identity
        self._git = git

    async def create_repository(self, r: Repository) -> Repository:
        if not r.remote_url:
            raise ERR_MISSING_PAYLOAD
        if r.owner_user_id == 0:
            raise ERR_MISSING_OWNER_USER_ID
        if r.provider == GitProviderUnspecified:
            raise ERR_MISSING_PAYLOAD
        if self._identity is not None:
            try:
                await self._identity.validate_user_exists(r.owner_user_id)
            except DomainError:
                raise
            except Exception as exc:
                raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        out: Repository | None = None

        async def _create() -> None:
            nonlocal out
            try:
                out = await self._repo.create(r)
            except DomainError:
                raise
            except Exception as exc:
                raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        await self._tx.with_transaction(_create)
        assert out is not None
        infof("repository.create-repository", identifier=out.identifier)
        return out

    async def get_repository(self, identifier: int) -> Repository:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            return await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def list_repositories(
        self,
        filter: RepositoriesFilter | None,
        params: PageQueryParams | None,
    ) -> tuple[list[Repository], Pagination]:
        if params is None:
            params = PageQueryParams(page_number=1, page_size=10)
        try:
            return await self._repo.list(filter, params)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def update_repository(self, r: Repository, mask: list[str]) -> Repository:
        if r.identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            existing = await self._repo.get_by_id(r.identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        _apply_repository_mask(existing, r, mask)

        try:
            await self._repo.update(existing)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        return existing

    async def delete_repository(self, identifier: int) -> None:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            await self._repo.soft_delete(identifier)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def test_connection(self, identifier: int) -> ConnectionStatus:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            r = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        try:
            status = await self._git.test_connection(r.remote_url, r.credential_ref)
        except Exception:
            status = ConnectionStatusUnreachable

        try:
            await self._repo.update_connection_status(identifier, status)
        except Exception:
            pass  # best-effort — mirrors Go: _ = s.repo.UpdateConnectionStatus(...)

        return status

    async def list_branches(self, identifier: int) -> list[Branch]:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            r = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        try:
            return await self._git.list_branches(r.remote_url, r.credential_ref)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def push_result(
        self,
        identifier: int,
        target_branch: str,
        create_new_repo: bool,
    ) -> tuple[str, str]:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            r = await self._repo.get_by_id(identifier, False)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        try:
            return await self._git.push_result(
                r.remote_url, r.credential_ref, target_branch, create_new_repo
            )
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc


def _apply_repository_mask(
    existing: Repository, update: Repository, mask: list[str]
) -> None:
    """Apply update fields to existing for each path in mask.

    If mask is empty, no fields are changed — matches Go applyRepositoryMask.
    """
    if not mask:
        return
    for path in mask:
        if path == "remote_url":
            existing.remote_url = update.remote_url
        elif path == "default_branch":
            existing.default_branch = update.default_branch
        elif path == "state":
            existing.state = update.state
        elif path == "connection_status":
            existing.connection_status = update.connection_status
        elif path == "credential_ref":
            existing.credential_ref = update.credential_ref
