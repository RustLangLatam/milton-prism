"""Tests for MotorTransactionManager (A.5).

Replica-set tests (test_commit_*, test_rollback_*) are skipped when the
configured MongoDB instance does not support transactions.  They MUST NOT
be deleted — they exist to be run on a properly configured cluster.

Setup for a local replica set:
    mongod --replSet rs0 --port 27017 --dbpath /tmp/rs0
    mongosh --eval "rs.initiate()"
or see docs/prism/python-dev-setup.md for the full procedure.
"""
from __future__ import annotations

import os
from typing import Any

import pytest
from motor.motor_asyncio import AsyncIOMotorClient

from shared.mongo_client.transaction_manager import MotorTransactionManager

_MONGO_URI = os.environ.get("MONGO_URI", "mongodb://localhost:27017/")
_TEST_DB = "tx_test_db"
_TEST_COL = "tx_test_col"


# ── helpers ──────────────────────────────────────────────────────────────────


async def _replica_set_available() -> bool:
    """Return True when the MongoDB at _MONGO_URI is reachable and supports transactions.

    Uses a short serverSelectionTimeoutMS so the probe fails fast when MongoDB
    is not running, rather than hanging for 30 s.  A no-op with_transaction
    callback never generates a round-trip, so we ping first to verify
    reachability, then probe session support.
    """
    client: AsyncIOMotorClient[Any] = AsyncIOMotorClient(
        _MONGO_URI, serverSelectionTimeoutMS=3000
    )
    try:
        await client.admin.command("ping")
        async with await client.start_session() as session:
            async def _probe(_s: Any) -> None:
                await client.admin.command("ping")

            await session.with_transaction(_probe)
        return True
    except Exception:
        return False
    finally:
        client.close()


# ── none-client (no MongoDB needed) ──────────────────────────────────────────


async def test_none_client_runs_fn_directly() -> None:
    """MotorTransactionManager(None) runs fn without a session — fallback path."""
    calls: list[int] = []

    async def fn() -> int:
        calls.append(1)
        return 42

    tx = MotorTransactionManager(None)
    result = await tx.with_transaction(fn)
    assert result == 42
    assert calls == [1]


async def test_none_client_propagates_exception() -> None:
    """Exceptions from fn() propagate through even when client is None."""

    async def fn() -> int:
        raise ValueError("boom")

    tx = MotorTransactionManager(None)
    with pytest.raises(ValueError, match="boom"):
        await tx.with_transaction(fn)


async def test_returns_fn_return_value() -> None:
    """with_transaction forwards the return value of fn()."""

    async def fn() -> str:
        return "hello"

    tx = MotorTransactionManager(None)
    assert await tx.with_transaction(fn) == "hello"


# ── replica-set tests (skipped when RS unavailable) ───────────────────────────


@pytest.mark.asyncio
async def test_commit_on_success() -> None:
    """with_transaction commits the write when fn() succeeds (requires RS)."""
    if not await _replica_set_available():
        pytest.skip("MongoDB replica set not available — skipping transaction commit test")

    client: AsyncIOMotorClient[Any] = AsyncIOMotorClient(
        _MONGO_URI, serverSelectionTimeoutMS=3000
    )
    db = client[_TEST_DB]
    col = db[_TEST_COL]
    await col.delete_many({})

    tx = MotorTransactionManager(client)

    async def fn() -> None:
        await col.insert_one({"key": "commit_test"})

    await tx.with_transaction(fn)
    count = await col.count_documents({"key": "commit_test"})
    assert count == 1
    client.close()


@pytest.mark.asyncio
async def test_rollback_on_exception() -> None:
    """with_transaction rolls back when fn() raises (requires RS)."""
    if not await _replica_set_available():
        pytest.skip("MongoDB replica set not available — skipping transaction rollback test")

    client: AsyncIOMotorClient[Any] = AsyncIOMotorClient(
        _MONGO_URI, serverSelectionTimeoutMS=3000
    )
    db = client[_TEST_DB]
    col = db[_TEST_COL]
    await col.delete_many({})

    tx = MotorTransactionManager(client)

    async def fn() -> None:
        await col.insert_one({"key": "rollback_test"})
        raise RuntimeError("abort transaction")

    with pytest.raises(RuntimeError):
        await tx.with_transaction(fn)

    count = await col.count_documents({"key": "rollback_test"})
    assert count == 0, "Document should have been rolled back"
    client.close()
