"""IdentityClient port (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable


@runtime_checkable
class IdentityClient(Protocol):
    async def validate_user_exists(self, user_id: int) -> None: ...
