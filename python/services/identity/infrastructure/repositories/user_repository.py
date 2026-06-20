"""Motor MongoDB user repository — implements UserRepository port (A.3, A.5, A.6).

ID generation uses the system_counters collection (A.6).
"""
from __future__ import annotations

from datetime import datetime, timezone

from google.protobuf.timestamp_pb2 import Timestamp
from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams
from motor.motor_asyncio import AsyncIOMotorDatabase
from pymongo import ReturnDocument

from services.identity.domain.domain import Pagination, User, UsersFilter, UserState
from services.identity.domain.errors import (
    ERR_EMAIL_ALREADY_EXISTS,
    ERR_USER_NOT_FOUND,
)

_USERS_COLLECTION = "users"
_COUNTERS_COLLECTION = "system_counters"
_COUNTER_KEY = "users"
_COUNTER_SEED = 100_001


def _now_ts() -> Timestamp:
    ts = Timestamp()
    ts.FromDatetime(datetime.now(tz=timezone.utc))
    return ts


def _doc_to_user(doc: dict) -> User:  # type: ignore[type-arg]
    u = User(
        identifier=doc.get("identifier", 0),
        email=doc.get("email", ""),
        display_name=doc.get("display_name", ""),
        system_user=doc.get("system_user", False),
        state=doc.get("state", UserState.USER_STATE_ACTIVE),
    )
    for field in ("create_time", "update_time", "delete_time", "purge_time"):
        raw = doc.get(field)
        if raw is not None:
            ts = Timestamp()
            ts.FromDatetime(raw if raw.tzinfo else raw.replace(tzinfo=timezone.utc))
            getattr(u, field).CopyFrom(ts)
    return u


def _user_to_doc(u: User, password_hash: str = "") -> dict:  # type: ignore[type-arg]
    doc: dict = {  # type: ignore[type-arg]
        "identifier": u.identifier,
        "email": u.email,
        "display_name": u.display_name,
        "system_user": u.system_user,
        "state": u.state,
    }
    if password_hash:
        doc["password_hash"] = password_hash
    for field in ("create_time", "update_time", "delete_time", "purge_time"):
        ts_msg = getattr(u, field, None)
        if ts_msg is not None and ts_msg.seconds > 0:
            doc[field] = ts_msg.ToDatetime(tzinfo=timezone.utc)
    return doc


class MongoUserRepository:
    """UserRepository backed by Motor + MongoDB (A.3)."""

    def __init__(self, db: AsyncIOMotorDatabase) -> None:  # type: ignore[type-arg]
        self._users = db[_USERS_COLLECTION]
        self._counters = db[_COUNTERS_COLLECTION]

    async def _next_id(self) -> int:
        result = await self._counters.find_one_and_update(
            {"_id": _COUNTER_KEY},
            {"$inc": {"seq": 1}},
            upsert=True,
            return_document=ReturnDocument.AFTER,
        )
        seq: int = result["seq"]
        if seq < _COUNTER_SEED:
            await self._counters.update_one(
                {"_id": _COUNTER_KEY, "seq": {"$lt": _COUNTER_SEED}},
                {"$set": {"seq": _COUNTER_SEED}},
            )
            return _COUNTER_SEED
        return seq

    async def create(self, user: User, password_hash: str) -> User:
        existing = await self._users.find_one({"email": user.email})
        if existing is not None:
            raise ERR_EMAIL_ALREADY_EXISTS

        new_id = await self._next_id()
        now = datetime.now(tz=timezone.utc)
        now_ts = Timestamp()
        now_ts.FromDatetime(now)

        user.identifier = new_id
        user.state = UserState.USER_STATE_ACTIVE
        user.create_time.CopyFrom(now_ts)
        user.update_time.CopyFrom(now_ts)

        doc = _user_to_doc(user, password_hash)
        await self._users.insert_one(doc)
        return user

    async def get_by_id(self, identifier: int, include_deleted: bool = False) -> User:
        query: dict = {"identifier": identifier}  # type: ignore[type-arg]
        if not include_deleted:
            query["state"] = {"$ne": UserState.USER_STATE_DELETED}
        doc = await self._users.find_one(query)
        if doc is None:
            raise ERR_USER_NOT_FOUND
        return _doc_to_user(doc)

    async def get_by_email(self, email: str) -> tuple[User, str]:
        doc = await self._users.find_one({"email": email})
        if doc is None:
            raise ERR_USER_NOT_FOUND
        return _doc_to_user(doc), doc.get("password_hash", "")

    async def list_users(
        self,
        filter: UsersFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[User], Pagination]:
        query: dict = {}  # type: ignore[type-arg]
        if filter is not None:
            if filter.HasField("state"):
                query["state"] = filter.state
            if filter.HasField("email"):
                query["email"] = filter.email

        page_size = params.page_size or 10
        page_number = params.page_number or 1
        skip = (page_number - 1) * page_size
        sort_field = params.sort_by or "create_time"
        sort_dir = -1 if params.order == 0 else 1  # 0=DESC, 1=ASC

        total = await self._users.count_documents(query)
        cursor = self._users.find(query).sort(sort_field, sort_dir).skip(skip).limit(page_size)
        docs = await cursor.to_list(length=page_size)

        total_pages = (total + page_size - 1) // page_size if page_size > 0 else 1
        pagination = Pagination(
            current_page=page_number,
            page_size=page_size,
            total_size=total,
            total_pages=total_pages,
        )
        return [_doc_to_user(d) for d in docs], pagination

    async def update(self, user: User) -> User:
        now_ts = _now_ts()
        user.update_time.CopyFrom(now_ts)
        doc = _user_to_doc(user)
        result = await self._users.replace_one(
            {"identifier": user.identifier}, doc
        )
        if result.matched_count == 0:
            raise ERR_USER_NOT_FOUND
        return user

    async def soft_delete(self, identifier: int) -> None:
        now = datetime.now(tz=timezone.utc)
        result = await self._users.update_one(
            {"identifier": identifier, "state": {"$ne": UserState.USER_STATE_DELETED}},
            {
                "$set": {
                    "state": UserState.USER_STATE_DELETED,
                    "delete_time": now,
                    "update_time": now,
                }
            },
        )
        if result.matched_count == 0:
            raise ERR_USER_NOT_FOUND
