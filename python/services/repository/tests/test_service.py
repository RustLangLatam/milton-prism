"""RepositoryService unit tests (A.8).

All ports are mocked (no Motor, no MongoDB, no gRPC).
Every use case has ≥1 success + ≥1 error scenario.
Behaviour mirrors the Go implementation.
"""
from __future__ import annotations

from unittest.mock import AsyncMock, MagicMock

import pytest

from services.repository.application.service import RepositoryService
from services.repository.domain.domain import (
    ConnectionStatusOK,
    ConnectionStatusUnreachable,
    GitProvider,
    Repository,
)
from services.repository.domain.errors import (
    ERR_INTERNAL,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_OWNER_USER_ID,
    ERR_MISSING_PAYLOAD,
    ERR_OWNER_NOT_FOUND,
    ERR_REPOSITORY_NOT_FOUND,
)
from shared.errors import DomainError


def _make_svc(
    repo: AsyncMock | None = None,
    tx: MagicMock | None = None,
    identity: AsyncMock | None = None,
    git: AsyncMock | None = None,
) -> RepositoryService:
    if repo is None:
        repo = AsyncMock()
    if tx is None:
        # Passthrough tx: immediately calls fn()
        tx = MagicMock()
        async def _with_tx(fn):  # type: ignore[no-untyped-def]
            return await fn()
        tx.with_transaction = _with_tx
    if git is None:
        git = AsyncMock()
    return RepositoryService(repo=repo, tx=tx, identity=identity, git=git)


def _repo(
    identifier: int = 1,
    owner_user_id: int = 42,
    remote_url: str = "https://github.com/org/repo",
    provider: GitProvider = GitProvider.GIT_PROVIDER_GITHUB,
) -> Repository:
    return Repository(
        identifier=identifier,
        owner_user_id=owner_user_id,
        remote_url=remote_url,
        provider=provider,
    )


# ── create_repository ─────────────────────────────────────────────────────────

async def test_create_repository_success() -> None:
    created = _repo()
    repo = AsyncMock()
    repo.create.return_value = created
    svc = _make_svc(repo=repo)
    result = await svc.create_repository(_repo(identifier=0))
    assert result.identifier == 1
    repo.create.assert_called_once()


async def test_create_repository_missing_remote_url() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.create_repository(
            Repository(owner_user_id=1, provider=GitProvider.GIT_PROVIDER_GITHUB)
        )
    assert exc_info.value.code == ERR_MISSING_PAYLOAD.code


async def test_create_repository_missing_owner() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.create_repository(
            Repository(remote_url="https://x.com", provider=GitProvider.GIT_PROVIDER_GITHUB)
        )
    assert exc_info.value.code == ERR_MISSING_OWNER_USER_ID.code


async def test_create_repository_missing_provider() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.create_repository(Repository(remote_url="https://x.com", owner_user_id=1))
    assert exc_info.value.code == ERR_MISSING_PAYLOAD.code


async def test_create_repository_identity_validates() -> None:
    identity = AsyncMock()
    identity.validate_user_exists.return_value = None
    repo = AsyncMock()
    repo.create.return_value = _repo()
    svc = _make_svc(repo=repo, identity=identity)
    await svc.create_repository(_repo(identifier=0))
    identity.validate_user_exists.assert_awaited_once_with(42)


async def test_create_repository_owner_not_found() -> None:
    identity = AsyncMock()
    identity.validate_user_exists.side_effect = DomainError(
        ERR_OWNER_NOT_FOUND.code, ERR_OWNER_NOT_FOUND.message
    )
    svc = _make_svc(identity=identity)
    with pytest.raises(DomainError) as exc_info:
        await svc.create_repository(_repo(identifier=0))
    assert exc_info.value.code == ERR_OWNER_NOT_FOUND.code


# ── get_repository ────────────────────────────────────────────────────────────

async def test_get_repository_success() -> None:
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    svc = _make_svc(repo=repo)
    result = await svc.get_repository(1)
    assert result.identifier == 1


async def test_get_repository_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.get_repository(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_get_repository_not_found() -> None:
    repo = AsyncMock()
    repo.get_by_id.side_effect = DomainError(
        ERR_REPOSITORY_NOT_FOUND.code, ERR_REPOSITORY_NOT_FOUND.message
    )
    svc = _make_svc(repo=repo)
    with pytest.raises(DomainError) as exc_info:
        await svc.get_repository(99)
    assert exc_info.value.code == ERR_REPOSITORY_NOT_FOUND.code


# ── list_repositories ─────────────────────────────────────────────────────────

async def test_list_repositories_success() -> None:
    from services.repository.domain.domain import Pagination
    repo = AsyncMock()
    repo.list.return_value = ([_repo()], Pagination(total_size=1))
    svc = _make_svc(repo=repo)
    items, pag = await svc.list_repositories(None, None)
    assert len(items) == 1
    assert pag.total_size == 1


async def test_list_repositories_repo_error() -> None:
    repo = AsyncMock()
    repo.list.side_effect = RuntimeError("db error")
    svc = _make_svc(repo=repo)
    with pytest.raises(DomainError) as exc_info:
        await svc.list_repositories(None, None)
    assert exc_info.value.code == ERR_INTERNAL.code


# ── update_repository ─────────────────────────────────────────────────────────

async def test_update_repository_success() -> None:
    existing = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = existing
    repo.update.return_value = None
    svc = _make_svc(repo=repo)
    result = await svc.update_repository(
        Repository(identifier=1, default_branch="develop"), ["default_branch"]
    )
    assert result.default_branch == "develop"


async def test_update_repository_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.update_repository(Repository(), ["remote_url"])
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_update_repository_empty_mask_no_change() -> None:
    """Empty update_mask → no fields updated (matches Go applyRepositoryMask)."""
    existing = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = existing
    repo.update.return_value = None
    svc = _make_svc(repo=repo)
    result = await svc.update_repository(
        Repository(identifier=1, default_branch="changed"), []
    )
    assert result.default_branch == existing.default_branch


# ── delete_repository ─────────────────────────────────────────────────────────

async def test_delete_repository_success() -> None:
    repo = AsyncMock()
    repo.soft_delete.return_value = None
    svc = _make_svc(repo=repo)
    await svc.delete_repository(1)
    repo.soft_delete.assert_awaited_once_with(1)


async def test_delete_repository_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.delete_repository(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


# ── test_connection ───────────────────────────────────────────────────────────

async def test_test_connection_success_ok() -> None:
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    repo.update_connection_status.return_value = None
    git = AsyncMock()
    git.test_connection.return_value = ConnectionStatusOK
    svc = _make_svc(repo=repo, git=git)
    status = await svc.test_connection(1)
    assert status == ConnectionStatusOK
    repo.update_connection_status.assert_awaited_once_with(1, ConnectionStatusOK)


async def test_test_connection_git_raises_returns_unreachable() -> None:
    """When git stub raises (NoOp behaviour), service returns UNREACHABLE."""
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    repo.update_connection_status.return_value = None
    git = AsyncMock()
    git.test_connection.side_effect = DomainError("REPO500", "Failure_Git_Not_Implemented")
    svc = _make_svc(repo=repo, git=git)
    status = await svc.test_connection(1)
    assert status == ConnectionStatusUnreachable


async def test_test_connection_update_status_error_ignored() -> None:
    """update_connection_status errors are swallowed (best-effort, mirrors Go)."""
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    repo.update_connection_status.side_effect = RuntimeError("db down")
    git = AsyncMock()
    git.test_connection.return_value = ConnectionStatusOK
    svc = _make_svc(repo=repo, git=git)
    status = await svc.test_connection(1)
    assert status == ConnectionStatusOK


# ── list_branches ─────────────────────────────────────────────────────────────

async def test_list_branches_success() -> None:
    from services.repository.domain.domain import Branch
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    git = AsyncMock()
    git.list_branches.return_value = [Branch(name="main")]
    svc = _make_svc(repo=repo, git=git)
    branches = await svc.list_branches(1)
    assert branches[0].name == "main"


async def test_list_branches_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.list_branches(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


# ── push_result ───────────────────────────────────────────────────────────────

async def test_push_result_success() -> None:
    r = _repo()
    repo = AsyncMock()
    repo.get_by_id.return_value = r
    git = AsyncMock()
    git.push_result.return_value = ("migration/output", "")
    svc = _make_svc(repo=repo, git=git)
    pushed, new_url = await svc.push_result(1, "migration/output", False)
    assert pushed == "migration/output"
    assert new_url == ""


async def test_push_result_missing_identifier() -> None:
    svc = _make_svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.push_result(0, "branch", False)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


# ── error sentinels ───────────────────────────────────────────────────────────

def test_all_error_sentinels_have_correct_prefix() -> None:
    from services.repository.domain import errors as errs
    sentinels = [
        errs.ERR_MISSING_IDENTIFIER, errs.ERR_MISSING_PAYLOAD,
        errs.ERR_MISSING_OWNER_USER_ID, errs.ERR_INVALID_REMOTE_URL,
        errs.ERR_REPOSITORY_NOT_FOUND, errs.ERR_REPOSITORY_ALREADY_EXISTS,
        errs.ERR_OWNER_NOT_FOUND, errs.ERR_CONNECTION_FAILED,
        errs.ERR_FORBIDDEN_ACCESS, errs.ERR_INTERNAL,
    ]
    for s in sentinels:
        assert s.code.startswith("REPO"), f"Expected REPO prefix: {s.code}"
        assert s.message.startswith("Failure_"), f"Expected Failure_ prefix: {s.message}"
