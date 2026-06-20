"""IdentityClientAdapter — gRPC adapter for the IdentityClient port (A.3)."""
from __future__ import annotations

import grpc
from milton_prism.services.identity.v1.identity_service_pb2 import GetUserRequest
from milton_prism.services.identity.v1.identity_service_pb2_grpc import (
    IdentityServiceStub,
)

from services.repository.domain.errors import ERR_INTERNAL, ERR_OWNER_NOT_FOUND
from shared.errors import DomainError


class IdentityClientAdapter:
    """Calls the identity service to validate that a user exists (A.3)."""

    def __init__(self, stub: IdentityServiceStub) -> None:
        self._stub = stub

    async def validate_user_exists(self, user_id: int) -> None:
        """Return None on success.  Raise ERR_OWNER_NOT_FOUND or ERR_INTERNAL."""
        try:
            await self._stub.GetUser(GetUserRequest(identifier=user_id))
        except grpc.aio.AioRpcError as exc:
            if exc.code() == grpc.StatusCode.NOT_FOUND:
                raise DomainError(
                    ERR_OWNER_NOT_FOUND.code, ERR_OWNER_NOT_FOUND.message
                ) from exc
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
        except Exception as exc:
            raise DomainError(ERR_INTERNAL.code, ERR_INTERNAL.message) from exc
