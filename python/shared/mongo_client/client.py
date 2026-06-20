"""Motor async MongoDB client builder with graceful lifecycle (A.1).

Usage:
    builder = MongoClientBuilder(uri="mongodb://localhost:27017", database="milton_prism")
    db = builder.build()          # AsyncIOMotorDatabase
    # ...
    builder.close()               # call on shutdown
"""
from __future__ import annotations

from motor.motor_asyncio import AsyncIOMotorClient, AsyncIOMotorDatabase


class MongoClientBuilder:
    """Creates and owns an AsyncIOMotorClient. Call close() on service shutdown."""

    def __init__(self, uri: str, database: str) -> None:
        self._uri = uri
        self._database = database
        self._client: AsyncIOMotorClient[dict[str, object]] | None = None

    def build(self) -> AsyncIOMotorDatabase[dict[str, object]]:
        """Return (and lazily create) the named Motor database handle."""
        if self._client is None:
            self._client = AsyncIOMotorClient(self._uri)
        return self._client[self._database]  # type: ignore[index]

    def close(self) -> None:
        """Close the underlying TCP connections. Idempotent."""
        if self._client is not None:
            self._client.close()
            self._client = None


def get_database(uri: str, database: str) -> AsyncIOMotorDatabase[dict[str, object]]:
    """Convenience: create a one-shot database handle without lifecycle management."""
    client: AsyncIOMotorClient[dict[str, object]] = AsyncIOMotorClient(uri)
    return client[database]  # type: ignore[index]
