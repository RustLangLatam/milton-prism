"""IdentityServicer — gRPC transport adapter (A.3).

Responsibilities:
- Deserialize request → delegate to IdentityService → serialize response
- Map DomainError → gRPC status via map_error (A.4)
- Extract bearer token from metadata; delegate session extraction to service

Does NOT import from repositories (enforced by import-linter contract, A.9).
"""
from __future__ import annotations

import grpc
import grpc.aio
from google.protobuf.empty_pb2 import Empty
from milton_prism.services.identity.v1 import identity_service_pb2 as pb
from milton_prism.services.identity.v1.identity_service_pb2_grpc import (
    IdentityServiceServicer,
)
from milton_prism.types.identity.v1.user_pb2 import User as UserPb

from services.identity.application.service import IdentityService
from shared.errors.mapper import map_error
from shared.logging import errorf

_AUTHORIZATION_KEY = "authorization"


def _bearer_token_from_metadata(
    context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
) -> str:
    """Extract raw JWT from Bearer Authorization metadata. Returns '' if absent."""
    raw_metadata = context.invocation_metadata()
    metadata: dict[str, str] = {str(k): str(v) for k, v in (raw_metadata or [])}
    bearer = metadata.get(_AUTHORIZATION_KEY, "")
    if not bearer.lower().startswith("bearer "):
        return ""
    return bearer[7:]


class IdentityServicer(IdentityServiceServicer):  # type: ignore[misc]
    """gRPC servicer delegating to IdentityService (A.3)."""

    def __init__(self, svc: IdentityService) -> None:
        self._svc = svc

    async def CreateUser(
        self,
        request: pb.CreateUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> UserPb:
        try:
            return await self._svc.create_user(request.user, request.password)
        except Exception as exc:
            await self._abort(context, exc)
            return UserPb()

    async def GetUser(
        self,
        request: pb.GetUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> UserPb:
        try:
            return await self._svc.get_user(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return UserPb()

    async def ListUsers(
        self,
        request: pb.ListUsersRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.ListUsersResponse:
        try:
            users, pagination = await self._svc.list_users(
                request.filter if request.HasField("filter") else None,
                request.page_params,
            )
            return pb.ListUsersResponse(users=users, pagination=pagination)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.ListUsersResponse()

    async def UpdateUser(
        self,
        request: pb.UpdateUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> UserPb:
        try:
            mask_paths = (
                list(request.update_mask.paths)
                if request.HasField("update_mask")
                else []
            )
            return await self._svc.update_user(request.user, mask_paths)
        except Exception as exc:
            await self._abort(context, exc)
            return UserPb()

    async def DeleteUser(
        self,
        request: pb.DeleteUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> Empty:
        try:
            await self._svc.delete_user(request.identifier)
            return Empty()
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()

    async def AuthenticateUser(
        self,
        request: pb.AuthenticateUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> object:
        try:
            return await self._svc.authenticate_user(request.email, request.password)
        except Exception as exc:
            await self._abort(context, exc)
            return None

    async def RefreshToken(
        self,
        request: pb.RefreshTokenRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> object:
        try:
            return await self._svc.refresh_token(request.refresh_token)
        except Exception as exc:
            await self._abort(context, exc)
            return None

    async def Logout(
        self,
        request: pb.LogoutRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> Empty:
        token = _bearer_token_from_metadata(context)
        session_id = self._svc.extract_session_id_from_access_token(token)
        try:
            await self._svc.logout(session_id)
            return Empty()
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()

    async def GetCurrentUser(
        self,
        request: pb.GetCurrentUserRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> UserPb:
        token = _bearer_token_from_metadata(context)
        session_id = self._svc.extract_session_id_from_access_token(token)
        try:
            return await self._svc.get_current_user(session_id)
        except Exception as exc:
            await self._abort(context, exc)
            return UserPb()

    @staticmethod
    async def _abort(
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
        exc: Exception,
    ) -> None:
        code, detail = map_error(exc)
        errorf("identity.handler", code=code, detail=detail)
        await context.abort(code, detail)
