"""Unit tests for IdentityService — application layer tested against mock ports (A.8).

All tests use mock ports (no MongoDB, no bcrypt, no JWT). Coverage floor:
every use case has ≥1 success test + ≥1 error scenario (Canon §8).
"""
from __future__ import annotations

from typing import Any
from unittest.mock import AsyncMock, MagicMock

import pytest

from services.identity.application.service import IdentityService
from services.identity.domain.domain import (
    AuthorizationTokens,
    Pagination,
    Token,
    User,
    UserState,
)
from services.identity.domain.errors import (
    ERR_ACCOUNT_SUSPENDED,
    ERR_EMAIL_ALREADY_EXISTS,
    ERR_INVALID_CREDENTIALS,
    ERR_INVALID_SESSION,
    ERR_INVALID_TOKEN,
    ERR_MISSING_EMAIL,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_PASSWORD,
    ERR_MISSING_PAYLOAD,
    ERR_USER_NOT_ACTIVE,
    ERR_USER_NOT_FOUND,
)
from shared.errors import DomainError

# ── Helpers ───────────────────────────────────────────────────────────────────

def _active_user(identifier: int = 1001, email: str = "alice@example.com") -> User:
    return User(identifier=identifier, email=email, state=UserState.USER_STATE_ACTIVE)


def _mock_repo(
    create_return: User | None = None,
    get_by_id_return: User | None = None,
    get_by_email_return: tuple[User, str] | None = None,
    list_return: tuple[list[User], Pagination] | None = None,
    update_return: User | None = None,
    create_raises: Exception | None = None,
    get_by_id_raises: Exception | None = None,
    get_by_email_raises: Exception | None = None,
    soft_delete_raises: Exception | None = None,
) -> Any:
    repo = MagicMock()
    if create_raises:
        repo.create = AsyncMock(side_effect=create_raises)
    else:
        repo.create = AsyncMock(return_value=create_return or _active_user())
    if get_by_id_raises:
        repo.get_by_id = AsyncMock(side_effect=get_by_id_raises)
    else:
        repo.get_by_id = AsyncMock(return_value=get_by_id_return or _active_user())
    if get_by_email_raises:
        repo.get_by_email = AsyncMock(side_effect=get_by_email_raises)
    else:
        repo.get_by_email = AsyncMock(
            return_value=get_by_email_return or (_active_user(), "$2b$12$fakehash")
        )
    if list_return:
        repo.list_users = AsyncMock(return_value=list_return)
    else:
        repo.list_users = AsyncMock(
            return_value=(
                [_active_user()],
                Pagination(total_size=1, current_page=1, page_size=10, total_pages=1),
            )
        )
    if soft_delete_raises:
        repo.soft_delete = AsyncMock(side_effect=soft_delete_raises)
    else:
        repo.soft_delete = AsyncMock(return_value=None)
    repo.update = AsyncMock(return_value=update_return or _active_user())
    return repo


def _mock_hasher(verify_result: bool = True) -> Any:
    hasher = MagicMock()
    hasher.hash = MagicMock(return_value="$2b$12$hashed")
    hasher.verify = MagicMock(return_value=verify_result)
    return hasher


def _mock_tokens(session_id: str = "abc123") -> Any:
    access = Token(value="access.jwt")
    refresh = Token(value="refresh.jwt")
    tokens_obj = AuthorizationTokens(
        access_token=access, refresh_token=refresh, expires_in=3600
    )
    tm = MagicMock()
    tm.new_tokens = MagicMock(return_value=tokens_obj)
    tm.extract_session_id = MagicMock(return_value=session_id)
    tm.verify_access = MagicMock(return_value=(1001, session_id))
    return tm


def _mock_sessions(
    user_id: int = 1001,
    is_system: bool = False,
    valid: bool = True,
    get_raises: Exception | None = None,
) -> Any:
    sessions = MagicMock()
    sessions.save = AsyncMock(return_value=None)
    sessions.delete = AsyncMock(return_value=None)
    if get_raises:
        sessions.get = AsyncMock(side_effect=get_raises)
    else:
        sessions.get = AsyncMock(return_value=(user_id, is_system, valid))
    return sessions


def _svc(**kwargs: Any) -> IdentityService:
    return IdentityService(
        repo=kwargs.get("repo", _mock_repo()),
        tx=None,
        hasher=kwargs.get("hasher", _mock_hasher()),
        tokens=kwargs.get("tokens", _mock_tokens()),
        sessions=kwargs.get("sessions", _mock_sessions()),
    )


# ── create_user ───────────────────────────────────────────────────────────────

async def test_create_user_success() -> None:
    user = User(email="bob@example.com")
    svc = _svc(repo=_mock_repo(create_return=_active_user(email="bob@example.com")))
    result = await svc.create_user(user, "Password1!")
    assert result.email == "bob@example.com"


async def test_create_user_missing_payload_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.create_user(User(), "Password1!")
    assert exc_info.value.code == ERR_MISSING_PAYLOAD.code


async def test_create_user_missing_password_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.create_user(User(email="x@x.com"), "")
    assert exc_info.value.code == ERR_MISSING_PASSWORD.code


async def test_create_user_email_conflict_propagates() -> None:
    svc = _svc(repo=_mock_repo(create_raises=ERR_EMAIL_ALREADY_EXISTS))
    with pytest.raises(DomainError) as exc_info:
        await svc.create_user(User(email="dup@x.com"), "Password1!")
    assert exc_info.value.code == ERR_EMAIL_ALREADY_EXISTS.code


# ── get_user ─────────────────────────────────────────────────────────────────

async def test_get_user_success() -> None:
    svc = _svc(repo=_mock_repo(get_by_id_return=_active_user(identifier=42)))
    result = await svc.get_user(42)
    assert result.identifier == 42


async def test_get_user_zero_identifier_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.get_user(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_get_user_not_found_propagates() -> None:
    svc = _svc(repo=_mock_repo(get_by_id_raises=ERR_USER_NOT_FOUND))
    with pytest.raises(DomainError) as exc_info:
        await svc.get_user(99)
    assert exc_info.value.code == ERR_USER_NOT_FOUND.code


# ── list_users ────────────────────────────────────────────────────────────────

async def test_list_users_success() -> None:
    users = [_active_user()]
    pg = Pagination(total_size=1, current_page=1, page_size=10, total_pages=1)
    svc = _svc(repo=_mock_repo(list_return=(users, pg)))
    result_users, result_pg = await svc.list_users(None)
    assert len(result_users) == 1
    assert result_pg.total_size == 1


async def test_list_users_default_params() -> None:
    svc = _svc()
    result_users, _ = await svc.list_users(None, None)
    assert isinstance(result_users, list)


# ── update_user ───────────────────────────────────────────────────────────────

async def test_update_user_success() -> None:
    existing = _active_user(identifier=1001)
    updated = User(identifier=1001, email="new@example.com")
    svc = _svc(
        repo=_mock_repo(get_by_id_return=existing, update_return=updated)
    )
    result = await svc.update_user(User(identifier=1001, email="new@example.com"), ["email"])
    assert result.identifier == 1001


async def test_update_user_zero_identifier_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.update_user(User(), [])
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_update_user_not_found_propagates() -> None:
    svc = _svc(repo=_mock_repo(get_by_id_raises=ERR_USER_NOT_FOUND))
    with pytest.raises(DomainError) as exc_info:
        await svc.update_user(User(identifier=99), [])
    assert exc_info.value.code == ERR_USER_NOT_FOUND.code


# ── delete_user ───────────────────────────────────────────────────────────────

async def test_delete_user_success() -> None:
    svc = _svc(repo=_mock_repo())
    await svc.delete_user(1001)  # no exception = success


async def test_delete_user_zero_identifier_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.delete_user(0)
    assert exc_info.value.code == ERR_MISSING_IDENTIFIER.code


async def test_delete_user_not_found_propagates() -> None:
    svc = _svc(repo=_mock_repo(soft_delete_raises=ERR_USER_NOT_FOUND))
    with pytest.raises(DomainError) as exc_info:
        await svc.delete_user(99)
    assert exc_info.value.code == ERR_USER_NOT_FOUND.code


# ── authenticate_user ─────────────────────────────────────────────────────────

async def test_authenticate_user_success() -> None:
    svc = _svc()
    tokens = await svc.authenticate_user("alice@example.com", "Password1!")
    assert tokens.access_token.value == "access.jwt"
    assert tokens.refresh_token.value == "refresh.jwt"


async def test_authenticate_user_missing_email_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.authenticate_user("", "Password1!")
    assert exc_info.value.code == ERR_MISSING_EMAIL.code


async def test_authenticate_user_missing_password_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.authenticate_user("alice@example.com", "")
    assert exc_info.value.code == ERR_MISSING_PASSWORD.code


async def test_authenticate_user_wrong_password_raises() -> None:
    svc = _svc(hasher=_mock_hasher(verify_result=False))
    with pytest.raises(DomainError) as exc_info:
        await svc.authenticate_user("alice@example.com", "wrong")
    assert exc_info.value.code == ERR_INVALID_CREDENTIALS.code


async def test_authenticate_user_deleted_account_raises() -> None:
    deleted = User(
        identifier=1001, email="alice@example.com", state=UserState.USER_STATE_DELETED
    )
    svc = _svc(repo=_mock_repo(get_by_email_return=(deleted, "$2b$12$hash")))
    with pytest.raises(DomainError) as exc_info:
        await svc.authenticate_user("alice@example.com", "Password1!")
    assert exc_info.value.code == ERR_USER_NOT_ACTIVE.code


async def test_authenticate_user_suspended_account_raises() -> None:
    suspended = User(
        identifier=1001, email="alice@example.com", state=UserState.USER_STATE_SUSPENDED
    )
    svc = _svc(repo=_mock_repo(get_by_email_return=(suspended, "$2b$12$hash")))
    with pytest.raises(DomainError) as exc_info:
        await svc.authenticate_user("alice@example.com", "Password1!")
    assert exc_info.value.code == ERR_ACCOUNT_SUSPENDED.code


# ── refresh_token ─────────────────────────────────────────────────────────────

async def test_refresh_token_success() -> None:
    svc = _svc()
    tokens = await svc.refresh_token("refresh.jwt")
    assert tokens.access_token.value == "access.jwt"


async def test_refresh_token_empty_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.refresh_token("")
    assert exc_info.value.code == ERR_INVALID_TOKEN.code


async def test_refresh_token_invalid_session_raises() -> None:
    svc = _svc(sessions=_mock_sessions(valid=False))
    with pytest.raises(DomainError) as exc_info:
        await svc.refresh_token("some.jwt")
    assert exc_info.value.code == ERR_INVALID_SESSION.code


# ── logout ────────────────────────────────────────────────────────────────────

async def test_logout_success() -> None:
    svc = _svc()
    await svc.logout("session-abc")  # no exception = success


async def test_logout_empty_session_id_is_noop() -> None:
    sessions = _mock_sessions()
    svc = _svc(sessions=sessions)
    await svc.logout("")
    sessions.delete.assert_not_called()


# ── get_current_user ─────────────────────────────────────────────────────────

async def test_get_current_user_success() -> None:
    svc = _svc()
    user = await svc.get_current_user("session-abc")
    assert user.identifier == 1001


async def test_get_current_user_empty_session_raises() -> None:
    svc = _svc()
    with pytest.raises(DomainError) as exc_info:
        await svc.get_current_user("")
    assert exc_info.value.code == ERR_INVALID_TOKEN.code


async def test_get_current_user_invalid_session_raises() -> None:
    svc = _svc(sessions=_mock_sessions(valid=False))
    with pytest.raises(DomainError) as exc_info:
        await svc.get_current_user("expired-session")
    assert exc_info.value.code == ERR_INVALID_SESSION.code


# ── error sentinel coverage ───────────────────────────────────────────────────

def test_all_error_sentinels_have_codes() -> None:
    """Every error sentinel must have a non-empty code and message (Canon §8)."""
    from services.identity.domain import errors as errs

    sentinels = [
        errs.ERR_MISSING_IDENTIFIER,
        errs.ERR_MISSING_PAYLOAD,
        errs.ERR_INVALID_EMAIL,
        errs.ERR_INVALID_PASSWORD,
        errs.ERR_MISSING_EMAIL,
        errs.ERR_MISSING_PASSWORD,
        errs.ERR_USER_NOT_FOUND,
        errs.ERR_EMAIL_ALREADY_EXISTS,
        errs.ERR_INVALID_CREDENTIALS,
        errs.ERR_USER_NOT_ACTIVE,
        errs.ERR_ACCOUNT_SUSPENDED,
        errs.ERR_INVALID_TOKEN,
        errs.ERR_INVALID_SESSION,
        errs.ERR_INTERNAL,
        errs.ERR_TOKEN_GENERATION,
        errs.ERR_TOKEN_REFRESH,
    ]
    for sentinel in sentinels:
        assert sentinel.code, f"empty code on {sentinel}"
        assert sentinel.message.startswith("Failure_"), f"bad message format on {sentinel.code}"
