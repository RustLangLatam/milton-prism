"""RepositoryServicer — gRPC transport adapter (A.3).

Responsibilities:
- Authenticate caller via BearerAuthExtractor → (user_id, is_system)
- Ownership check: non-system callers may only access their own repositories
- Deserialize request → delegate to RepositoryService → serialize response
- Strip credential_ref from every outgoing Repository message
- Map DomainError → gRPC status via map_error (A.4)

Does NOT import from repositories (enforced by import-linter contract, A.9).
"""
from __future__ import annotations

import grpc
import grpc.aio
from google.protobuf.empty_pb2 import Empty
from milton_prism.services.repository.v1 import repository_service_pb2 as pb
from milton_prism.services.repository.v1.repository_service_pb2_grpc import (
    RepositoryServiceServicer,
)
from milton_prism.types.repository.v1.repository_pb2 import (
    RepositoriesFilter,
)
from milton_prism.types.repository.v1.repository_pb2 import (
    Repository as RepositoryPb,
)

from services.repository.application.service import RepositoryService
from services.repository.domain.errors import ERR_FORBIDDEN_ACCESS
from shared.auth import AuthenticationError, BearerAuthExtractor
from shared.errors import DomainError
from shared.errors.mapper import map_error
from shared.logging import errorf, warningf


def _strip_credential(r: RepositoryPb) -> RepositoryPb:
    """Remove credential_ref before returning a repository to the caller."""
    r.credential_ref = ""
    return r


class RepositoryServicer(RepositoryServiceServicer):  # type: ignore[misc]
    """gRPC servicer delegating to RepositoryService (A.3)."""

    def __init__(self, svc: RepositoryService, auth: BearerAuthExtractor) -> None:
        self._svc = svc
        self._auth = auth

    def _extract_caller(
        self, context: grpc.aio.ServicerContext  # type: ignore[type-arg]
    ) -> tuple[int, bool]:
        """Return (user_id, is_system). Abort with UNAUTHENTICATED on failure."""
        return self._auth(context)

    async def CreateRepository(
        self,
        request: pb.CreateRepositoryRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> RepositoryPb:
        try:
            caller_id, _ = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: CreateRepository authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return RepositoryPb()
        r = request.repository
        if r.owner_user_id == 0:
            r.owner_user_id = caller_id
        try:
            out = await self._svc.create_repository(r)
            return _strip_credential(out)
        except Exception as exc:
            await self._abort(context, exc)
            return RepositoryPb()

    async def GetRepository(
        self,
        request: pb.GetRepositoryRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> RepositoryPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: GetRepository authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return RepositoryPb()
        try:
            r = await self._svc.get_repository(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return RepositoryPb()
        if not is_system and r.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return RepositoryPb()
        return _strip_credential(r)

    async def ListRepositories(
        self,
        request: pb.ListRepositoriesRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.ListRepositoriesResponse:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: ListRepositories authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return pb.ListRepositoriesResponse()
        filter_ = request.filter if request.HasField("filter") else RepositoriesFilter()
        if not is_system:
            filter_.owner_user_id = caller_id
        try:
            items, pagination = await self._svc.list_repositories(filter_, request.page_params)
            for item in items:
                item.credential_ref = ""
            return pb.ListRepositoriesResponse(repositories=items, pagination=pagination)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.ListRepositoriesResponse()

    async def UpdateRepository(
        self,
        request: pb.UpdateRepositoryRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> RepositoryPb:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: UpdateRepository authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return RepositoryPb()
        try:
            existing = await self._svc.get_repository(request.repository.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return RepositoryPb()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return RepositoryPb()
        mask = list(request.update_mask.paths) if request.HasField("update_mask") else []
        try:
            out = await self._svc.update_repository(request.repository, mask)
            return _strip_credential(out)
        except Exception as exc:
            await self._abort(context, exc)
            return RepositoryPb()

    async def DeleteRepository(
        self,
        request: pb.DeleteRepositoryRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> Empty:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: DeleteRepository authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return Empty()
        try:
            existing = await self._svc.get_repository(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return Empty()
        try:
            await self._svc.delete_repository(request.identifier)
            return Empty()
        except Exception as exc:
            await self._abort(context, exc)
            return Empty()

    async def TestConnection(
        self,
        request: pb.TestConnectionRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.TestConnectionResponse:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: TestConnection authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return pb.TestConnectionResponse()
        try:
            existing = await self._svc.get_repository(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.TestConnectionResponse()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return pb.TestConnectionResponse()
        try:
            status = await self._svc.test_connection(request.identifier)
            return pb.TestConnectionResponse(status=status)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.TestConnectionResponse()

    async def ListBranches(
        self,
        request: pb.ListBranchesRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.ListBranchesResponse:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: ListBranches authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return pb.ListBranchesResponse()
        try:
            existing = await self._svc.get_repository(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.ListBranchesResponse()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return pb.ListBranchesResponse()
        try:
            branches = await self._svc.list_branches(request.identifier)
            return pb.ListBranchesResponse(branches=branches)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.ListBranchesResponse()

    async def PushResult(
        self,
        request: pb.PushResultRequest,
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
    ) -> pb.PushResultResponse:
        try:
            caller_id, is_system = self._extract_caller(context)
        except AuthenticationError:
            warningf("repository: PushResult authentication failed")
            await context.abort(grpc.StatusCode.UNAUTHENTICATED, "Failure_Unauthenticated")
            return pb.PushResultResponse()
        try:
            existing = await self._svc.get_repository(request.identifier)
        except Exception as exc:
            await self._abort(context, exc)
            return pb.PushResultResponse()
        if not is_system and existing.owner_user_id != caller_id:
            await self._abort(context, DomainError(
                ERR_FORBIDDEN_ACCESS.code, ERR_FORBIDDEN_ACCESS.message
            ))
            return pb.PushResultResponse()
        try:
            pushed_branch, new_repo_url = await self._svc.push_result(
                request.identifier, request.target_branch, request.create_new_repository
            )
            return pb.PushResultResponse(
                pushed_branch=pushed_branch, new_repository_url=new_repo_url
            )
        except Exception as exc:
            await self._abort(context, exc)
            return pb.PushResultResponse()

    @staticmethod
    async def _abort(
        context: grpc.aio.ServicerContext,  # type: ignore[type-arg]
        exc: Exception,
    ) -> None:
        code, detail = map_error(exc)
        errorf("repository.handler", code=code, detail=detail)
        await context.abort(code, detail)
