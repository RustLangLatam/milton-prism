"""TransactionManager port (A.3)."""
from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any, Protocol, runtime_checkable


@runtime_checkable
class TransactionManager(Protocol):
    async def with_transaction(self, fn: Callable[[], Awaitable[Any]]) -> Any: ...
