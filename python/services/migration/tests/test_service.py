"""MigrationService unit tests (A.8).

All ports are mocked (no Motor, no MongoDB, no gRPC).
Every use case has ≥1 success + ≥1 error scenario.
State machine transitions mirror the Go implementation exactly.
"""
from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from services.migration.application.service import MigrationService, _is_terminal
from services.migration.domain.domain import (
    Migration,
    MigrationState,
    MigrationStateAnalyzing,
    MigrationStateAwaitingApproval,
    MigrationStateCancelled,
    MigrationStateFailed,
    MigrationStateGenerating,
    MigrationStatePending,
    MigrationStatePushed,
    MigrationStateReady,
    MigrationStateTesting,
    Pagination,
    TargetConfig,
    TargetDatabase,
    TargetLanguage,
)
from services.migration.domain.errors import (
    ERR_INTERNAL,
    ERR_INVALID_STATE_TRANSITION,
    ERR_INVALID_TARGET_CONFIG,
    ERR_MIGRATION_NOT_FOUND,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_OWNER_USER_ID,
    ERR_MISSING_REPOSITORY_ID,
    ERR_OWNER_NOT_FOUND,
    ERR_REPOSITORY_NOT_FOUND,
)
from shared.errors import DomainError


def _make_svc(
    repo: AsyncMock | None = None,
    tx: MagicMock | None = None,
    identity: AsyncMock | None = None,
    repository_svc: AsyncMock | None = None,
    analysis: AsyncMock | None = None,
) -> MigrationService:
    if repo is None:
        repo = AsyncMock()
    if tx is None:
        tx = MagicMock()

        async def _with_tx(fn):  # type: ignore[no-untyped-def]
            return await fn()

        tx.with_transaction = _with_tx
    if analysis is None:
        analysis = AsyncMock()
    return MigrationService(
        repo=repo,
        tx=tx,
        identity=identity,
        repository_svc=repository_svc,
        analysis=analysis,
    )


def _target(
    language: TargetLanguage = TargetLanguage.TARGET_LANGUAGE_GO,
    database: TargetDatabase = TargetDatabase.TARGET_DATABASE_MONGODB,
) -> TargetConfig:
    return TargetConfig(language=language, database=database)


def _migration(
    identifier: int = 1,
    owner_user_id: int = 42,
    repository_id: int = 10,
    state: MigrationState = MigrationStatePending,
    target: TargetConfig | None = None,
) -> Migration:
    m = Migration(
        identifier=identifier,
        owner_user_id=owner_user_id,
        repository_id=repository_id,
        state=state,
    )
    tc = target if target is not None else _target()
    m.target.CopyFrom(tc)
    return m


# ── _is_terminal helper ───────────────────────────────────────────────────────

def test_terminal_states() -> None:
    for state in (MigrationStatePushed, MigrationStateFailed, MigrationStateCancelled):
        assert _is_terminal(state)


def test_non_terminal_states() -> None:
    for state in (
        MigrationStatePending,
        MigrationStateAnalyzing,
        MigrationStateReady,
        MigrationStateTesting,
        MigrationStateGenerating,
        MigrationStateAwaitingApproval,
    ):
        assert not _is_terminal(state)


# ── create_migration ──────────────────────────────────────────────────────────

async def test_create_migration_success() -> None:
    created = _migration()
    repo = AsyncMock()
    repo.create.return_value = created
    svc = _make_svc(repo=repo)
    result = await svc.create_migration(_migration(identifier=0))
    assert result.identifier == 1
    repo.create.assert_called_once()


async def test_create_migration_sets_pending_state() -> None:
    repo = AsyncMock()
    captured: list[Migration] = []

    async def _capture(m: Migration) -> Migration:
        captured.append(Migration())
        captured[0].CopyFrom(m)
        return m

    repo.create.side_effect = _capture
    svc = _make_svc(repo=repo)
    await svc.create_migration(_migration(identifier=0))
    assert captured[0].state == MigrationStatePending


async def test_create_migration_missing_owner() -> None:
    svc = _make_svc()
    m = _migration(owner_user_id=0)
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(m)
    assert exc_info.value.code == ERR_MISSING_OWNER_USER_ID.code


async def test_create_migration_missing_repository() -> None:
    svc = _make_svc()
    m = _migration(repository_id=0)
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(m)
    assert exc_info.value.code == ERR_MISSING_REPOSITORY_ID.code


async def test_create_migration_invalid_target_language() -> None:
    svc = _make_svc()
    m = _migration(target=_target(language=TargetLanguage.TARGET_LANGUAGE_UNSPECIFIED))
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(m)
    assert exc_info.value.code == ERR_INVALID_TARGET_CONFIG.code


async def test_create_migration_invalid_target_database() -> None:
    svc = _make_svc()
    m = _migration(target=_target(database=TargetDatabase.TARGET_DATABASE_UNSPECIFIED))
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(m)
    assert exc_info.value.code == ERR_INVALID_TARGET_CONFIG.code


async def test_create_migration_identity_validated() -> None:
    identity = AsyncMock()
    identity.validate_user_exists.return_value = None
    repo = AsyncMock()
    repo.create.return_value = _migration()
    svc = _make_svc(repo=repo, identity=identity)
    await svc.create_migration(_migration(identifier=0))
    identity.validate_user_exists.assert_awaited_once_with(42)


async def test_create_migration_owner_not_found() -> None:
    identity = AsyncMock()
    identity.validate_user_exists.side_effect = DomainError(
        ERR_OWNER_NOT_FOUND.code, ERR_OWNER_NOT_FOUND.message
    )
    svc = _make_svc(identity=identity)
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(_migration(identifier=0))
    assert exc_info.value.code == ERR_OWNER_NOT_FOUND.code


async def test_create_migration_repository_client_validated() -> None:
    repository_svc = AsyncMock()
    repository_svc.validate_repository_exists.return_value = None
    repo = AsyncMock()
    repo.create.return_value = _migration()
    svc = _make_svc(repo=repo, repository_svc=repository_svc)
    await svc.create_migration(_migration(identifier=0))
    repository_svc.validate_repository_exists.assert_awaited_once_with(10)


async def test_create_migration_repository_not_found() -> None:
    repository_svc = AsyncMock()
    repository_svc.validate_repository_exists.side_effect = DomainError(
        ERR_REPOSITORY_NOT_FOUND.code, ERR_REPOSITORY_NOT_FOUND.message
    )
    svc = _make_svc(repository_svc=repository_svc)
    with pytest.raises(DomainError) as exc_info:
        await svc.create_migration(_migration(identifier=0))
    assert exc_info.value.code == ERR_REPOSITORY_NOT_FOUND.code


# ── get_migration ─────────────────────────────────────────────────────────────

async def test_get_migration_success() -> None:
    repo = AsyncMock()
    repo.get_by_id.return_value = _migration()
    svc = _make_svc(repo=repo)
    result = await svc.get_migration(1)
    assert result.identifier == 1


async def test_get_migration_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.get_migration(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_get_migration_not_found() -> None:
    repo = AsyncMock()
    repo.get_by_id.side_effect = DomainError(
        ERR_MIGRATION_NOT_FOUND.code, ERR_MIGRATION_NOT_FOUND.message
    )
    svc = _make_svc(repo=repo)
    with pytest.raises(DomainError) as exc_info:
        await svc.get_migration(99)
    assert exc_info.value.code == ERR_MIGRATION_NOT_FOUND.code


# ── list_migrations ───────────────────────────────────────────────────────────

async def test_list_migrations_success() -> None:
    repo = AsyncMock()
    repo.list.return_value = ([_migration()], Pagination(total_size=1))
    svc = _make_svc(repo=repo)
    items, pag = await svc.list_migrations(None, None)
    assert len(items) == 1
    assert pag.total_size == 1


async def test_list_migrations_repo_error() -> None:
    repo = AsyncMock()
    repo.list.side_effect = RuntimeError("db error")
    svc = _make_svc(repo=repo)
    with pytest.raises(DomainError) as exc_info:
        await svc.list_migrations(None, None)
    assert exc_info.value.code == ERR_INTERNAL.code


# ── delete_migration ──────────────────────────────────────────────────────────

async def test_delete_migration_success_from_terminal() -> None:
    for terminal_state in (MigrationStatePushed, MigrationStateFailed, MigrationStateCancelled):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=terminal_state)
        repo.soft_delete.return_value = None
        svc = _make_svc(repo=repo)
        await svc.delete_migration(1)
        repo.soft_delete.assert_awaited_once_with(1)


async def test_delete_migration_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.delete_migration(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_delete_migration_non_terminal_state_rejected() -> None:
    for non_terminal in (MigrationStatePending, MigrationStateAnalyzing, MigrationStateReady):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=non_terminal)
        svc = _make_svc(repo=repo)
        with pytest.raises(DomainError) as exc_info:
            await svc.delete_migration(1)
        assert exc_info.value.code == ERR_INVALID_STATE_TRANSITION.code


# ── start_migration ───────────────────────────────────────────────────────────

async def test_start_migration_pending_to_analyzing() -> None:
    repo = AsyncMock()
    repo.get_by_id.return_value = _migration(state=MigrationStatePending)
    repo.update_state.return_value = None
    svc = _make_svc(repo=repo)
    result = await svc.start_migration(1)
    assert result.state == MigrationStateAnalyzing
    repo.update_state.assert_awaited_once_with(1, MigrationStateAnalyzing)


async def test_start_migration_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.start_migration(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_start_migration_wrong_state_rejected() -> None:
    for wrong_state in (MigrationStateAnalyzing, MigrationStateReady, MigrationStateCancelled):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=wrong_state)
        svc = _make_svc(repo=repo)
        with pytest.raises(DomainError) as exc_info:
            await svc.start_migration(1)
        assert exc_info.value.code == ERR_INVALID_STATE_TRANSITION.code


# ── approve_design ────────────────────────────────────────────────────────────

async def test_approve_design_approved_to_generating() -> None:
    repo = AsyncMock()
    repo.get_by_id.return_value = _migration(state=MigrationStateAwaitingApproval)
    repo.update_state.return_value = None
    svc = _make_svc(repo=repo)
    result = await svc.approve_design(1, approved=True)
    assert result.state == MigrationStateGenerating
    repo.update_state.assert_awaited_once_with(1, MigrationStateGenerating)


async def test_approve_design_rejected_to_cancelled() -> None:
    repo = AsyncMock()
    repo.get_by_id.return_value = _migration(state=MigrationStateAwaitingApproval)
    repo.update_state.return_value = None
    svc = _make_svc(repo=repo)
    result = await svc.approve_design(1, approved=False)
    assert result.state == MigrationStateCancelled


async def test_approve_design_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.approve_design(0, approved=True)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_approve_design_wrong_state_rejected() -> None:
    for wrong_state in (MigrationStatePending, MigrationStateGenerating, MigrationStateCancelled):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=wrong_state)
        svc = _make_svc(repo=repo)
        with pytest.raises(DomainError) as exc_info:
            await svc.approve_design(1, approved=True)
        assert exc_info.value.code == ERR_INVALID_STATE_TRANSITION.code


# ── cancel_migration ──────────────────────────────────────────────────────────

async def test_cancel_migration_success() -> None:
    for non_terminal in (
        MigrationStatePending,
        MigrationStateAnalyzing,
        MigrationStateAwaitingApproval,
        MigrationStateGenerating,
    ):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=non_terminal)
        repo.update_state.return_value = None
        svc = _make_svc(repo=repo)
        result = await svc.cancel_migration(1)
        assert result.state == MigrationStateCancelled


async def test_cancel_migration_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.cancel_migration(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_cancel_migration_terminal_state_rejected() -> None:
    for terminal in (MigrationStatePushed, MigrationStateFailed, MigrationStateCancelled):
        repo = AsyncMock()
        repo.get_by_id.return_value = _migration(state=terminal)
        svc = _make_svc(repo=repo)
        with pytest.raises(DomainError) as exc_info:
            await svc.cancel_migration(1)
        assert exc_info.value.code == ERR_INVALID_STATE_TRANSITION.code


# ── error sentinels ───────────────────────────────────────────────────────────

def test_all_error_sentinels_have_correct_prefix() -> None:
    from services.migration.domain import errors as errs
    sentinels = [
        errs.ERR_MISSING_IDENTIFIER, errs.ERR_MISSING_PAYLOAD,
        errs.ERR_MISSING_OWNER_USER_ID, errs.ERR_MISSING_REPOSITORY_ID,
        errs.ERR_INVALID_TARGET_CONFIG, errs.ERR_MIGRATION_NOT_FOUND,
        errs.ERR_INVALID_STATE_TRANSITION, errs.ERR_REPOSITORY_NOT_FOUND,
        errs.ERR_OWNER_NOT_FOUND, errs.ERR_FORBIDDEN_ACCESS, errs.ERR_INTERNAL,
    ]
    for s in sentinels:
        assert s.code.startswith("MIG"), f"Expected MIG prefix: {s.code}"
        assert s.message.startswith("Failure_"), f"Expected Failure_ prefix: {s.message}"
