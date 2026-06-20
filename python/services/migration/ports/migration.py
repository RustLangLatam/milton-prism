"""MigrationRepository port — structural Protocol (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable

from services.migration.domain.domain import (
    Migration,
    MigrationsFilter,
    MigrationState,
    PageQueryParams,
    Pagination,
)


@runtime_checkable
class MigrationRepository(Protocol):
    async def create(self, m: Migration) -> Migration: ...
    async def get_by_id(self, identifier: int, include_deleted: bool) -> Migration: ...
    async def list(
        self,
        filter: MigrationsFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[Migration], Pagination]: ...
    async def update_state(self, identifier: int, state: MigrationState) -> None: ...
    async def soft_delete(self, identifier: int) -> None: ...
