"""IdentityClient port — validates user existence via the identity service (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable


@runtime_checkable
class IdentityClient(Protocol):
    """Driven port for confirming a user exists before persisting a repository."""

    async def validate_user_exists(self, user_id: int) -> None:
        """Return None when the user exists and is active.

        Raise ERR_OWNER_NOT_FOUND when not found, ERR_INTERNAL on transport error.
        """
        ...
