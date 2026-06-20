"""RepositoryRepository port — structural Protocol (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable

from services.repository.domain.domain import (
    ConnectionStatus,
    PageQueryParams,
    Pagination,
    RepositoriesFilter,
    Repository,
)


@runtime_checkable
class RepositoryRepository(Protocol):
    """Storage contract for Repository resources. Motor adapter implements this."""

    async def create(self, r: Repository) -> Repository: ...

    async def get_by_id(self, identifier: int, include_deleted: bool = False) -> Repository: ...

    async def list(
        self,
        filter: RepositoriesFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[Repository], Pagination]: ...

    async def update(self, r: Repository) -> None: ...

    async def soft_delete(self, identifier: int) -> None: ...

    async def update_connection_status(
        self, identifier: int, status: ConnectionStatus
    ) -> None: ...
