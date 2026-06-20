"""RepositoryClientAdapter — gRPC-backed repository port (A.3)."""
from __future__ import annotations

import grpc
import grpc.aio
from milton_prism.services.repository.v1.repository_service_pb2 import (
    GetRepositoryRequest,
)
from milton_prism.services.repository.v1.repository_service_pb2_grpc import (
    RepositoryServiceStub,
)

from services.migration.domain.errors import ERR_REPOSITORY_NOT_FOUND
from shared.errors import DomainError


class RepositoryClientAdapter:
    def __init__(self, stub: RepositoryServiceStub) -> None:
        self._stub = stub

    async def validate_repository_exists(self, repository_id: int) -> None:
        try:
            await self._stub.GetRepository(GetRepositoryRequest(identifier=repository_id))
        except grpc.aio.AioRpcError as exc:
            if exc.code() == grpc.StatusCode.NOT_FOUND:
                raise DomainError(
                    ERR_REPOSITORY_NOT_FOUND.code, ERR_REPOSITORY_NOT_FOUND.message
                ) from exc
            raise DomainError("MIG500", "Failure_Internal") from exc
