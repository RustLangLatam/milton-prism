"""RepositoryClient port (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable


@runtime_checkable
class RepositoryClient(Protocol):
    async def validate_repository_exists(self, repository_id: int) -> None: ...
