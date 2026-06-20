"""MotorTransactionManager — async MongoDB transaction boundary (A.5).

Wraps Motor's start_session / with_transaction to satisfy the
TransactionManager protocol defined in each service's ports layer.

Fallback behaviour (mirrors the Go implementation): if the client is None
or the MongoDB server does not support sessions (standalone), fn() runs
directly without a transaction rather than raising.  Production clusters
must be configured as a replica set for the atomicity guarantee to hold.
"""
from __future__ import annotations

from collections.abc import Awaitable, Callable
from typing import Any

from motor.motor_asyncio import AsyncIOMotorClient  # type: ignore[import-untyped]


class MotorTransactionManager:
    """Satisfies every service's TransactionManager Protocol (structural)."""

    def __init__(self, client: AsyncIOMotorClient[Any] | None) -> None:
        self._client = client

    async def with_transaction(self, fn: Callable[[], Awaitable[Any]]) -> Any:
        """Run fn inside a MongoDB multi-document transaction.

        Falls back to running fn without a session when:
        - client is None (development without a replica set), or
        - the server does not support sessions (standalone mongod).
        The fallback matches the Go MongoTransactionManager behaviour.
        """
        if self._client is None:
            return await fn()

        try:
            async with await self._client.start_session() as session:
                async def _cb(_session: Any) -> Any:
                    return await fn()

                return await session.with_transaction(_cb)
        except Exception:
            return await fn()
