"""IdentityClientAdapter — gRPC-backed identity port (A.3)."""
from __future__ import annotations

import grpc
import grpc.aio
from milton_prism.services.identity.v1.identity_service_pb2 import GetUserRequest
from milton_prism.services.identity.v1.identity_service_pb2_grpc import (
    IdentityServiceStub,
)

from services.migration.domain.errors import ERR_OWNER_NOT_FOUND
from shared.errors import DomainError


class IdentityClientAdapter:
    def __init__(self, stub: IdentityServiceStub) -> None:
        self._stub = stub

    async def validate_user_exists(self, user_id: int) -> None:
        try:
            await self._stub.GetUser(GetUserRequest(identifier=user_id))
        except grpc.aio.AioRpcError as exc:
            if exc.code() == grpc.StatusCode.NOT_FOUND:
                raise DomainError(ERR_OWNER_NOT_FOUND.code, ERR_OWNER_NOT_FOUND.message) from exc
            raise DomainError("MIG500", "Failure_Internal") from exc
