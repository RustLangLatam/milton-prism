"""MongoDB session store — stores active sessions with a TTL index (A.10).

v1 only supports MongoDB; Redis is a hole (A.10). Sessions live in the
`sessions` collection with a TTL index on `expires_at` for auto-expiry.
"""
from __future__ import annotations

from datetime import datetime, timedelta, timezone

from motor.motor_asyncio import AsyncIOMotorDatabase

_COLLECTION = "sessions"
_TTL_DAYS = 7


class MongoSessionStore:
    """SessionStore backed by MongoDB with TTL-indexed expiry (A.5, A.10)."""

    def __init__(self, db: AsyncIOMotorDatabase) -> None:  # type: ignore[type-arg]
        self._col = db[_COLLECTION]

    async def save(self, session_id: str, user_id: int, is_system: bool) -> None:
        expires_at = datetime.now(tz=timezone.utc) + timedelta(days=_TTL_DAYS)
        await self._col.replace_one(
            {"_id": session_id},
            {
                "_id": session_id,
                "user_id": user_id,
                "is_system": is_system,
                "expires_at": expires_at,
            },
            upsert=True,
        )

    async def get(self, session_id: str) -> tuple[int, bool, bool]:
        doc = await self._col.find_one({"_id": session_id})
        if doc is None:
            return 0, False, False
        expires_at: datetime = doc["expires_at"]
        if expires_at.tzinfo is None:
            expires_at = expires_at.replace(tzinfo=timezone.utc)
        valid = expires_at > datetime.now(tz=timezone.utc)
        return int(doc["user_id"]), bool(doc["is_system"]), valid

    async def delete(self, session_id: str) -> None:
        await self._col.delete_one({"_id": session_id})
