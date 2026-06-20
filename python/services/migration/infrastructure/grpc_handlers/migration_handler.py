"""MigrationServicer — gRPC transport adapter (A.3).

Responsibilities:
- Authenticate caller via BearerAuthExtractor → (user_id, is_system)
- Ownership check: non-system callers may only access their own migrations
- Deserialize request → delegate to MigrationService → serialize response
- Map DomainError → gRPC status via map_error (A.4)

Does NOT import from repositories (enforced by import-linter contract, A.9).
"""
from __future__ import annotations

import grpc
import grpc.aio
from google.protobuf.empty_pb2 import Empty
from milton_prism.services.migration.v1 import migration_service_pb2 as pb
from milton_prism.services.migration.v1.migration_service_pb2_grpc import (
    MigrationServiceServicer,
)
from milton_prism.types.migration.v1.migration_pb2 import (
    Migration as MigrationPb,
)
from milton_prism.types.migration.v1.migration_pb2 import (
    MigrationsFilter,
)

from services.migration.application.service import MigrationService
from services.migration.domain.errors import ERR_FORBIDDEN_ACCESS
from shared.auth import AuthenticationError, BearerAuthExtractor
from shared.errors import DomainError
from shared.errors.mapper import map_error
from shared.logging import errorf, warningf


class MigrationServicer(MigrationServiceServicer):  # type: ignore[misc]
    """gRPC servicer delegating to MigrationService (A.3)."""

    def __init__(self, svc: MigrationService, auth: BearerAuthExtractor) -> None:
        self._svc = svc
        self._auth = auth

    def _extract_caller(
        self, context: grpc.aio.ServicerContext  # type: ignore[type-arg]
    ) -> tuple[int, bool]:
        return self._auth(context)

    async def CreateMigration(
        self,
        request: pb.CreateMigrationRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> MigrationPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: CreateMigration authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return MigrationPb()
        if not request.HasField("migration"):
            await self._abort(context, DomainError("MIG102", "Failure_Missing_Payload"))
            return MigrationPb()
        m = request.migration
        if not is_system:
            m.owner_user_id = caller_id
        try:
            out = await self._svc.create_migration(m)
            return out
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()

    async def GetMigration(
        self,
        request: pb.GetMigrationRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> MigrationPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: GetMigration authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return MigrationPb()
        try:
            m = await self._svc.get_migration(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()
        if not is_system and m.owner_user_id != caller_id:
            await self._abort(
                context, DomainError(ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message)
            )
            return MigrationPb()
        return m

    async def ListMigrations(
        self,
        request: pb.ListMigrationsRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.ListMigrationsResponse:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: ListMigrations authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return pb.ListMigrationsResponse()
        filter_ = request.filter if request.HasField("filter") else MigrationsFilter()
        if not is_system:
            filter_.owner_user_id = caller_id
        try:
            items, pagination = await self._svc.list_migrations(filter_, request.page_params)
            return pb.ListMigrationsResponse(migrations=items, pagination=pagination)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.ListMigrationsResponse()

    async def DeleteMigration(
        self,
        request: pb.DeleteMigrationRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> Empty:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: DeleteMigration authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return Empty()
        try:
            existing = await self._svc.get_migration(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(
                context, DomainError(ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message)
            )
            return Empty()
        try:
            await self._svc.delete_migration(request.identifier)
            return Empty()
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()

    async def StartMigration(
        self,
        request: pb.StartMigrationRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> MigrationPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: StartMigration authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return MigrationPb()
        try:
            existing = await self._svc.get_migration(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(
                context, DomainError(ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message)
            )
            return MigrationPb()
        try:
            out = await self._svc.start_migration(request.identifier)
            return out
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()

    async def ApproveDesign(
        self,
        request: pb.ApproveDesignRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> MigrationPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: ApproveDesign authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return MigrationPb()
        try:
            existing = await self._svc.get_migration(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(
                context, DomainError(ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message)
            )
            return MigrationPb()
        try:
            out = await self._svc.approve_design(request.identifier, request.approved)
            return out
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()

    async def CancelMigration(
        self,
        request: pb.CancelMigrationRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> MigrationPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("migration: CancelMigration authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return MigrationPb()
        try:
            existing = await self._svc.get_migration(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(
                context, DomainError(ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message)
            )
            return MigrationPb()
        try:
            out = await self._svc.cancel_migration(request.identifier)
            return out
        except Exception as exc:
            await self._abort(context, exc)
            return MigrationPb()

    @staticmethod
    async def _abort(
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
        exc: Exception,
    ) -> None:
        code, detail = map_error(exc)
        errorf("migration.handler", code=code, detail=detail)
        await context.abort(code, detail)
