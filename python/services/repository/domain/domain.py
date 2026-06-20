"""Repository domain types — proto aliases (A.3).

Domain and application layers import from here. No parallel structs.
"""
from __future__ import annotations

from milton_prism.types.pagination.v1.pagination_pb2 import Pagination
from milton_prism.types.query_params.v1.query_params_pb2 import PageQueryParams
from milton_prism.types.repository.v1.repository_pb2 import (
    Branch,
    ConnectionStatus,
    GitProvider,
    RepositoriesFilter,
    Repository,
    RepositoryState,
)

GitProviderUnspecified = GitProvider.GIT_PROVIDER_UNSPECIFIED
ConnectionStatusOK = ConnectionStatus.CONNECTION_STATUS_OK
ConnectionStatusUnspecified = ConnectionStatus.CONNECTION_STATUS_UNSPECIFIED
ConnectionStatusUnreachable = ConnectionStatus.CONNECTION_STATUS_UNREACHABLE
RepositoryStateDisconnected = RepositoryState.REPOSITORY_STATE_DISCONNECTED

__all__ = [
    "Branch",
    "ConnectionStatus",
    "ConnectionStatusOK",
    "ConnectionStatusUnreachable",
    "ConnectionStatusUnspecified",
    "GitProvider",
    "GitProviderUnspecified",
    "PageQueryParams",
    "Pagination",
    "RepositoriesFilter",
    "Repository",
    "RepositoryState",
    "RepositoryStateDisconnected",
]
