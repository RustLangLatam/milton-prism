"""TransactionManager port — async closure-based boundary (A.5)."""
from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any, Protocol, TypeVar, runtime_checkable

T = TypeVar("T")


@runtime_checkable
class TransactionManager(Protocol):
    """Async transaction boundary. Motor adapter implements this (A.5)."""

    async def with_transaction(
        self, fn: Callable[[], Awaitable[Any]]
    ) -> Any: ...
