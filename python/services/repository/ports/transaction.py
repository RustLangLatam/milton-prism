"""TransactionManager port — async closure-based transaction boundary (A.5)."""
from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any, Protocol, runtime_checkable


@runtime_checkable
class TransactionManager(Protocol):
    """Async transaction boundary. MotorTransactionManager implements this (A.5)."""

    async def with_transaction(self, fn: Callable[[], Awaitable[Any]]) -> Any: ...
