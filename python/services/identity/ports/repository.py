"""UserRepository port — structural Protocol (A.3)."""
from __future__ import annotations

from typing import Protocol, runtime_checkable

from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams

from services.identity.domain.domain import Pagination, User, UsersFilter


@runtime_checkable
class UserRepository(Protocol):
    """Storage contract for User resources. Motor adapter implements this (A.3)."""

    async def create(self, user: User, password_hash: str) -> User: ...

    async def get_by_id(self, identifier: int, include_deleted: bool = False) -> User: ...

    async def get_by_email(self, email: str) -> tuple[User, str]: ...

    async def list_users(
        self,
        filter: UsersFilter | None,
        params: PageQueryParams,
    ) -> tuple[list[User], Pagination]: ...

    async def update(self, user: User) -> User: ...

    async def soft_delete(self, identifier: int) -> None: ...
