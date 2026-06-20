"""MongoRepositoryRepository — Motor adapter for RepositoryRepository port (A.3, A.6)."""
from __future__ import annotations

from datetime import datetime, timezone

from google.protobuf.timestamp_pb2 import Timestamp
from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams
from motor.motor_asyncio import AsyncIOMotorDatabase
from pymongo import ReturnDocument

from services.repository.domain.domain import (
    ConnectionStatus,
    GitProvider,
    Pagination,
    RepositoriesFilter,
    Repository,
    RepositoryState,
    RepositoryStateDisconnected,
)
from services.repository.domain.errors import (
    ERR_REPOSITORY_ALREADY_EXISTS,
    ERR_REPOSITORY_NOT_FOUND,
)

_REPOS_COLLECTION = "repositories"
_COUNTERS_COLLECTION = "system_counters"
_COUNTER_KEY = "repositories"
_COUNTER_SEED = 100_001


def _now() -> datetime:
    return datetime.now(tz=timezone.utc)


def _now_ts() -> Timestamp:
    ts = Timestamp()
    ts.FromDatetime(_now())
    return ts


def _dt_to_ts(dt: datetime) -> Timestamp:
    ts = Timestamp()
    ts.FromDatetime(dt if dt.tzinfo else dt.replace(tzinfo=timezone.utc))
    return ts


def _doc_to_repo(doc: dict) -> Repository:  # type: ignore[type-arg]
    r = Repository(
        identifier=doc.get("identifier", 0),
        owner_user_id=doc.get("owner_user_id", 0),
        provider=doc.get("provider", GitProvider.GIT_PROVIDER_UNSPECIFIED),
        remote_url=doc.get("remote_url", ""),
        default_branch=doc.get("default_branch", ""),
        state=doc.get("state", RepositoryState.REPOSITORY_STATE_UNSPECIFIED),
        connection_status=doc.get(
            "connection_status", ConnectionStatus.CONNECTION_STATUS_UNSPECIFIED
        ),
        # credential_ref is INPUT_ONLY — never populated on read (mirrors Go repoDocToDomain)
    )
    for field in ("create_time", "update_time", "delete_time", "purge_time"):
        raw = doc.get(field)
        if raw is not None:
            getattr(r, field).CopyFrom(_dt_to_ts(raw))
    return r


def _repo_to_doc(r: Repository) -> dict:  # type: ignore[type-arg]
    doc: dict = {  # type: ignore[type-arg]
        "identifier": r.identifier,
        "owner_user_id": r.owner_user_id,
        "provider": r.provider,
        "remote_url": r.remote_url,
        "default_branch": r.default_branch,
        "state": r.state,
        "connection_status": r.connection_status,
        "credential_ref": r.credential_ref,
    }
    for field in ("create_time", "update_time", "delete_time", "purge_time"):
        ts_msg = getattr(r, field, None)
        if ts_msg is not None and ts_msg.seconds > 0:
            doc[field] = ts_msg.ToDatetime(tzinfo=timezone.utc)
    return doc


class MongoRepositoryRepository:
    """RepositoryRepository backed by Motor + MongoDB (A.3)."""

    def __init__(self, db: AsyncIOMotorDatabase) -> None:  # type: ignore[type-arg]
        self._repos = db[_REPOS_COLLECTION]
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

    async def create(self, r: Repository) -> Repository:
        existing = await self._repos.find_one({"remote_url": r.remote_url})
        if existing is not None:
            raise ERR_REPOSITORY_ALREADY_EXISTS

        new_id = await self._next_id()
        now = _now()
        now_ts = _dt_to_ts(now)

        r.identifier = new_id
        r.create_time.CopyFrom(now_ts)
        r.update_time.CopyFrom(now_ts)

        doc = _repo_to_doc(r)
        await self._repos.insert_one(doc)
        r.credential_ref = ""  # never return credential_ref
        return r

    async def get_by_id(self, identifier: int, include_deleted: bool = False) -> Repository:
        query: dict = {"identifier": identifier}  # type: ignore[type-arg]
        if not include_deleted:
            query["delete_time"] = None
        doc = await self._repos.find_one(query)
        if doc is None:
            raise ERR_REPOSITORY_NOT_FOUND
        return _doc_to_repo(doc)

    async def list(
        self,
        filter: RepositoriesFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[Repository], Pagination]:
        query: dict = {"delete_time": None}  # type: ignore[type-arg]
        if filter is not None:
            if filter.HasField("owner_user_id") and filter.owner_user_id != 0:
                query["owner_user_id"] = filter.owner_user_id
            if (
                filter.HasField("state")
                and filter.state != RepositoryState.REPOSITORY_STATE_UNSPECIFIED
            ):
                query["state"] = filter.state
            if (
                filter.HasField("provider")
                and filter.provider != GitProvider.GIT_PROVIDER_UNSPECIFIED
            ):
                query["provider"] = filter.provider

        page_size = params.page_size or 10
        page_number = params.page_number or 1
        skip = (page_number - 1) * page_size

        total = await self._repos.count_documents(query)
        cursor = self._repos.find(query).skip(skip).limit(page_size)
        docs = await cursor.to_list(length=page_size)

        total_pages = (total + page_size - 1) // page_size if page_size > 0 else 1
        pagination = Pagination(
            current_page=page_number,
            page_size=page_size,
            total_size=total,
            total_pages=total_pages,
        )
        return [_doc_to_repo(d) for d in docs], pagination

    async def update(self, r: Repository) -> None:
        now = _now()
        set_doc: dict = {  # type: ignore[type-arg]
            "remote_url": r.remote_url,
            "default_branch": r.default_branch,
            "state": r.state,
            "connection_status": r.connection_status,
            "update_time": now,
        }
        if r.credential_ref:
            set_doc["credential_ref"] = r.credential_ref

        result = await self._repos.update_one(
            {"identifier": r.identifier, "delete_time": None},
            {"$set": set_doc},
        )
        if result.matched_count == 0:
            raise ERR_REPOSITORY_NOT_FOUND

    async def soft_delete(self, identifier: int) -> None:
        now = _now()
        result = await self._repos.update_one(
            {"identifier": identifier, "delete_time": None},
            {
                "$set": {
                    "state": RepositoryStateDisconnected,
                    "delete_time": now,
                    "update_time": now,
                }
            },
        )
        if result.matched_count == 0:
            raise ERR_REPOSITORY_NOT_FOUND

    async def update_connection_status(
        self, identifier: int, status: ConnectionStatus
    ) -> None:
        now = _now()
        await self._repos.update_one(
            {"identifier": identifier, "delete_time": None},
            {"$set": {"connection_status": status, "update_time": now}},
        )
