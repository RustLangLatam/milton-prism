"""IdentityService — all identity use cases (A.3, A.8).

Depends only on domain types + ports. No infrastructure imports allowed
(enforced by import-linter, A.9).
"""
from __future__ import annotations

import secrets

from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams

from services.identity.domain.domain import (
    AuthorizationTokens,
    Pagination,
    User,
    UsersFilter,
    UserState,
)
from services.identity.domain.errors import (
    ERR_ACCOUNT_SUSPENDED,
    ERR_INTERNAL,
    ERR_INVALID_CREDENTIALS,
    ERR_INVALID_SESSION,
    ERR_INVALID_TOKEN,
    ERR_MISSING_EMAIL,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_PASSWORD,
    ERR_MISSING_PAYLOAD,
    ERR_TOKEN_GENERATION,
    ERR_TOKEN_REFRESH,
    ERR_USER_NOT_ACTIVE,
    ERR_USER_NOT_FOUND,
)
from services.identity.ports.auth import PasswordHasher, SessionStore, TokenManager
from services.identity.ports.repository import UserRepository
from services.identity.ports.transaction import TransactionManager
from shared.errors import DomainError
from shared.logging import infof


class IdentityService:
    """Orchestrates identity use cases. Assembled in wire.py (A.3)."""

    def __init__(
        self,
        repo: UserRepository,
        tx: TransactionManager | None,
        hasher: PasswordHasher,
        tokens: TokenManager,
        sessions: SessionStore,
    ) -> None:
        self._repo = repo
        self._tx = tx
        self._hasher = hasher
        self._tokens = tokens
        self._sessions = sessions

    async def create_user(
        self,
        user: User,
        plain_password: str,
    ) -> User:
        if not user.email:
            raise ERR_MISSING_PAYLOAD
        if not plain_password:
            raise ERR_MISSING_PASSWORD
        password_hash = self._hasher.hash(plain_password)
        try:
            created = await self._repo.create(user, password_hash)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        infof("identity.create-user", identifier=created.identifier)
        return created

    async def get_user(self, identifier: int) -> User:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            return await self._repo.get_by_id(identifier)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def list_users(
        self,
        filter: UsersFilter | None,
        params: PageQueryParams | None = None,
    ) -> tuple[list[User], Pagination]:
        if params is None:
            params = PageQueryParams(page_number=1, page_size=10)
        try:
            return await self._repo.list_users(filter, params)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def update_user(
        self,
        user: User,
        update_mask: list[str],
    ) -> User:
        if user.identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            existing = await self._repo.get_by_id(user.identifier)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        paths = update_mask if update_mask else ["email", "display_name", "system_user", "state"]
        for path in paths:
            if path == "email":
                existing.email = user.email
            elif path == "display_name":
                existing.display_name = user.display_name
            elif path == "system_user":
                existing.system_user = user.system_user
            elif path == "state":
                existing.state = user.state

        try:
            updated = await self._repo.update(existing)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        return updated

    async def delete_user(self, identifier: int) -> None:
        if identifier == 0:
            raise ERR_MISSING_IDENTIFIER
        try:
            await self._repo.soft_delete(identifier)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

    async def authenticate_user(
        self,
        email: str,
        plain_password: str,
    ) -> AuthorizationTokens:
        if not email:
            raise ERR_MISSING_EMAIL
        if not plain_password:
            raise ERR_MISSING_PASSWORD

        try:
            user, password_hash = await self._repo.get_by_email(email)
        except DomainError as exc:
            # Any lookup error maps to generic invalid-credentials (hide user-existence)
            raise DomainError(
                ERR_INVALID_CREDENTIALS.code, ERR_INVALID_CREDENTIALS.message
            ) from exc
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        if user.state == UserState.USER_STATE_DELETED:
            raise ERR_USER_NOT_ACTIVE
        if user.state == UserState.USER_STATE_SUSPENDED:
            raise ERR_ACCOUNT_SUSPENDED

        if not self._hasher.verify(password_hash, plain_password):
            raise ERR_INVALID_CREDENTIALS

        session_id = _generate_session_id()
        try:
            tokens = self._tokens.new_tokens(user.identifier, user.system_user, session_id)
        except Exception as exc:
            raise DomainError(ERR_TOKEN_GENERATION.code, ERR_TOKEN_GENERATION.message) from exc

        try:
            await self._sessions.save(session_id, user.identifier, user.system_user)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        infof("identity.authenticate-user", identifier=user.identifier)
        return tokens

    async def refresh_token(self, refresh_token_value: str) -> AuthorizationTokens:
        if not refresh_token_value:
            raise ERR_INVALID_TOKEN

        try:
            session_id = self._tokens.extract_session_id(refresh_token_value)
        except Exception as exc:
            raise DomainError(ERR_INVALID_TOKEN.code, ERR_INVALID_TOKEN.message) from exc

        try:
            user_id, _is_system, valid = await self._sessions.get(session_id)
        except Exception as exc:
            raise DomainError(ERR_INVALID_SESSION.code, ERR_INVALID_SESSION.message) from exc

        if not valid:
            raise DomainError(ERR_INVALID_SESSION.code, ERR_INVALID_SESSION.message)

        try:
            user = await self._repo.get_by_id(user_id)
        except DomainError as exc:
            raise DomainError(ERR_USER_NOT_FOUND.code, ERR_USER_NOT_FOUND.message) from exc
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc

        try:
            new_tokens = self._tokens.new_tokens(
                user.identifier, user.system_user, session_id
            )
        except Exception as exc:
            raise DomainError(ERR_TOKEN_REFRESH.code, ERR_TOKEN_REFRESH.message) from exc

        return new_tokens

    async def logout(self, session_id: str) -> None:
        if not session_id:
            return
        try:
            await self._sessions.delete(session_id)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        infof("identity.logout", session_id=session_id)

    async def get_current_user(self, session_id: str) -> User:
        if not session_id:
            raise ERR_INVALID_TOKEN

        try:
            user_id, _is_system, valid = await self._sessions.get(session_id)
        except Exception as exc:
            raise DomainError(ERR_INVALID_SESSION.code, ERR_INVALID_SESSION.message) from exc

        if not valid:
            raise DomainError(ERR_INVALID_SESSION.code, ERR_INVALID_SESSION.message)

        try:
            return await self._repo.get_by_id(user_id)
        except DomainError:
            raise
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc


    def extract_session_id_from_access_token(self, access_token: str) -> str:
        """Extract session_id from a bearer access token. Returns '' on failure.

        Called by handlers before delegating to logout/get_current_user so that
        handlers never need to access the token adapter directly.
        """
        if not access_token:
            return ""
        try:
            _, session_id = self._tokens.verify_access(access_token)
            return session_id
        except Exception:
            return ""


def _generate_session_id() -> str:
    """Generate a 16-byte cryptographically random hex session ID."""
    return secrets.token_hex(16)
