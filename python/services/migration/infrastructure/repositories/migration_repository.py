"""MongoMigrationRepository — Motor-backed persistence (A.3).

Nested proto messages (target, plan, output) are serialized as bytes,
matching the Go implementation exactly.
"""
from __future__ import annotations

from datetime import UTC, datetime
from typing import Any

from google.protobuf.timestamp_pb2 import Timestamp
from milton_prism.types.migration.v1.migration_pb2 import (
    Migration,
    MigrationOutput,
    MigrationsFilter,
    MigrationState,
    RestructurePlan,
    TargetConfig,
)
from milton_prism.types.pagination.v1.pagination_pb2 import Pagination
from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams
from motor.motor_asyncio import AsyncIOMotorDatabase

from services.migration.domain.errors import (
    ERR_INTERNAL,
    ERR_MIGRATION_NOT_FOUND,
)
from shared.errors import DomainError

_COLL = "migrations"
_COUNTERS_COLL = "system_counters"
_ID_SEED = 100001


async def _next_identifier(db: AsyncIOMotorDatabase) -> int:  # type: ignore[type-arg]
    result = await db[_COUNTERS_COLL].find_one_and_update(
        {"_id": _COLL},
        {"$inc": {"seq": 1}},
        upsert=True,
        return_document=True,
    )
    if result is None or "seq" not in result:
        await db[_COUNTERS_COLL].update_one(
            {"_id": _COLL},
            {"$setOnInsert": {"seq": _ID_SEED}},
            upsert=True,
        )
        return _ID_SEED
    seq: int = result["seq"]
    if seq < _ID_SEED:
        await db[_COUNTERS_COLL].update_one(
            {"_id": _COLL},
            {"$set": {"seq": _ID_SEED}},
        )
        return _ID_SEED
    return seq


def _now_ms() -> int:
    return int(datetime.now(UTC).timestamp() * 1000)


def _ms_to_ts(ms: int) -> Timestamp:
    ts = Timestamp()
    ts.FromMilliseconds(ms)
    return ts


def _migration_to_doc(m: Migration) -> dict[str, Any]:
    doc: dict[str, Any] = {
        "repository_id": m.repository_id,
        "owner_user_id": m.owner_user_id,
        "source_branch": m.source_branch,
        "state": int(m.state),
        "analysis_summary_id": m.analysis_summary_id,
    }
    if m.HasField("target"):
        doc["target_bytes"] = m.target.SerializeToString()
    if m.HasField("plan"):
        doc["plan_bytes"] = m.plan.SerializeToString()
    if m.HasField("output"):
        doc["output_bytes"] = m.output.SerializeToString()
    return doc


def _doc_to_migration(doc: dict[str, Any]) -> Migration:
    m = Migration(
        identifier=doc["identifier"],
        repository_id=doc["repository_id"],
        owner_user_id=doc["owner_user_id"],
        source_branch=doc.get("source_branch", ""),
        state=MigrationState(doc["state"]),
        analysis_summary_id=doc.get("analysis_summary_id", 0),
    )
    target_bytes: bytes | None = doc.get("target_bytes")
    if target_bytes:
        tc = TargetConfig()
        tc.ParseFromString(target_bytes)
        m.target.CopyFrom(tc)
    plan_bytes: bytes | None = doc.get("plan_bytes")
    if plan_bytes:
        plan = RestructurePlan()
        plan.ParseFromString(plan_bytes)
        m.plan.CopyFrom(plan)
    output_bytes: bytes | None = doc.get("output_bytes")
    if output_bytes:
        output = MigrationOutput()
        output.ParseFromString(output_bytes)
        m.output.CopyFrom(output)
    create_ms: int | None = doc.get("create_time")
    if create_ms:
        m.create_time.CopyFrom(_ms_to_ts(create_ms))
    update_ms: int | None = doc.get("update_time")
    if update_ms:
        m.update_time.CopyFrom(_ms_to_ts(update_ms))
    delete_ms: int | None = doc.get("delete_time")
    if delete_ms is not None:
        m.delete_time.CopyFrom(_ms_to_ts(delete_ms))
    purge_ms: int | None = doc.get("purge_time")
    if purge_ms is not None:
        m.purge_time.CopyFrom(_ms_to_ts(purge_ms))
    return m


class MongoMigrationRepository:
    def __init__(self, db: AsyncIOMotorDatabase) -> None:  # type: ignore[type-arg]
        self._db = db
        self._coll = db[_COLL]

    async def create(self, m: Migration) -> Migration:
        try:
            identifier = await _next_identifier(self._db)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        doc = _migration_to_doc(m)
        doc["identifier"] = identifier
        doc["create_time"] = _now_ms()
        doc["delete_time"] = None
        try:
            await self._coll.insert_one(doc)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        return _doc_to_migration(doc)

    async def get_by_id(self, identifier: int, include_deleted: bool) -> Migration:
        query: dict[str, Any] = {"identifier": identifier}
        if not include_deleted:
            query["delete_time"] = None
        try:
            doc = await self._coll.find_one(query)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if doc is None:
            raise DomainError(ERR_MIGRATION_NOT_FOUND.code, ERR_MIGRATION_NOT_FOUND.message)
        return _doc_to_migration(doc)

    async def list(
        self,
        filter: MigrationsFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[Migration], Pagination]:
        query: dict[str, Any] = {"delete_time": None}
        if filter is not None:
            if filter.HasField("owner_user_id") and filter.owner_user_id != 0:
                query["owner_user_id"] = filter.owner_user_id
            if filter.HasField("repository_id") and filter.repository_id != 0:
                query["repository_id"] = filter.repository_id
            if (
                filter.HasField("state")
                and filter.state
                != MigrationState.MIGRATION_STATE_UNSPECIFIED
            ):
                query["state"] = int(filter.state)
        page_size = params.page_size or 10
        page_number = params.page_number or 1
        skip = (page_number - 1) * page_size
        try:
            cursor = self._coll.find(query).skip(skip).limit(page_size)
            docs = await cursor.to_list(length=page_size)
            total = await self._coll.count_documents(query)
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        items = [_doc_to_migration(d) for d in docs]
        pag = Pagination(
            page_number=page_number,
            page_size=page_size,
            total_size=total,
        )
        return items, pag

    async def update_state(self, identifier: int, state: MigrationState) -> None:
        try:
            result = await self._coll.update_one(
                {"identifier": identifier, "delete_time": None},
                {"$set": {"state": int(state), "update_time": _now_ms()}},
            )
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if result.matched_count == 0:
            raise DomainError(ERR_MIGRATION_NOT_FOUND.code, ERR_MIGRATION_NOT_FOUND.message)

    async def soft_delete(self, identifier: int) -> None:
        now = _now_ms()
        try:
            result = await self._coll.update_one(
                {"identifier": identifier, "delete_time": None},
                {"$set": {"delete_time": now, "update_time": now}},
            )
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        if result.matched_count == 0:
            raise DomainError(ERR_MIGRATION_NOT_FOUND.code, ERR_MIGRATION_NOT_FOUND.message)
