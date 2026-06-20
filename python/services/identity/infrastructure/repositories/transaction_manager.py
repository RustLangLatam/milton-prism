"""Motor transaction manager adapter (A.5).

Wraps `AsyncIOMotorClient.start_session()` + `session.with_transaction()`.
If degraded (no client), calls fn() directly without a transaction boundary.
"""
from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

from motor.motor_asyncio import AsyncIOMotorClient


class MotorTransactionManager:
    """TransactionManager backed by Motor async sessions (A.5)."""

    def __init__(self, client: AsyncIOMotorClient) -> None:  # type: ignore[type-arg]
        self._client = client

    async def with_transaction(self, fn: Callable[[], Awaitable[Any]]) -> Any:
        async with await self._client.start_session() as session:
            return await session.with_transaction(lambda _s: fn())
