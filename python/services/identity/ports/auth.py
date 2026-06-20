"""Auth-related ports: PasswordHasher, TokenManager, SessionStore (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable

from services.identity.domain.domain import AuthorizationTokens


@runtime_checkable
class PasswordHasher(Protocol):
    """Hashes and verifies passwords. bcrypt adapter implements this."""

    def hash(self, plain: str) -> str: ...

    def verify(self, hashed: str, plain: str) -> bool: ...


@runtime_checkable
class TokenManager(Protocol):
    """Issues and validates JWT access/refresh token pairs."""

    def new_tokens(
        self,
        user_id: int,
        is_system: bool,
        session_id: str,
    ) -> AuthorizationTokens: ...

    def extract_session_id(self, token: str) -> str: ...

    def verify_access(self, token: str) -> tuple[int, str]: ...


@runtime_checkable
class SessionStore(Protocol):
    """Stores active sessions by session_id. MongoDB adapter implements this (A.10)."""

    async def save(
        self, session_id: str, user_id: int, is_system: bool
    ) -> None: ...

    async def get(self, session_id: str) -> tuple[int, bool, bool]: ...

    async def delete(self, session_id: str) -> None: ...
