from google.api import annotations_pb2 as _annotations_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import empty_pb2 as _empty_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from milton_prism.types.repository.v1 import repository_pb2 as _repository_pb2
from milton_prism.types.pagination.v1 import pagination_pb2 as _pagination_pb2
from milton_prism.types.query_params.v1 import query_params_pb2 as _query_params_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2_1
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateRepositoryRequest(_message.Message):
    __slots__ = ("repository",)
    REPOSITORY_FIELD_NUMBER: _ClassVar[int]
    repository: _repository_pb2.Repository
    def __init__(self, repository: _Optional[_Union[_repository_pb2.Repository, _Mapping]] = ...) -> None: ...

class GetRepositoryRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class ListRepositoriesRequest(_message.Message):
    __slots__ = ("filter", "page_params")
    FILTER_FIELD_NUMBER: _ClassVar[int]
    PAGE_PARAMS_FIELD_NUMBER: _ClassVar[int]
    filter: _repository_pb2.RepositoriesFilter
    page_params: _query_params_pb2.PageQueryParams
    def __init__(self, filter: _Optional[_Union[_repository_pb2.RepositoriesFilter, _Mapping]] = ..., page_params: _Optional[_Union[_query_params_pb2.PageQueryParams, _Mapping]] = ...) -> None: ...

class ListRepositoriesResponse(_message.Message):
    __slots__ = ("repositories", "pagination")
    REPOSITORIES_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    repositories: _containers.RepeatedCompositeFieldContainer[_repository_pb2.Repository]
    pagination: _pagination_pb2.Pagination
    def __init__(self, repositories: _Optional[_Iterable[_Union[_repository_pb2.Repository, _Mapping]]] = ..., pagination: _Optional[_Union[_pagination_pb2.Pagination, _Mapping]] = ...) -> None: ...

class UpdateRepositoryRequest(_message.Message):
    __slots__ = ("repository", "update_mask")
    REPOSITORY_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    repository: _repository_pb2.Repository
    update_mask: _field_mask_pb2.FieldMask
    def __init__(self, repository: _Optional[_Union[_repository_pb2.Repository, _Mapping]] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ...) -> None: ...

class DeleteRepositoryRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class TestConnectionRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class TestConnectionResponse(_message.Message):
    __slots__ = ("status",)
    STATUS_FIELD_NUMBER: _ClassVar[int]
    status: _repository_pb2.ConnectionStatus
    def __init__(self, status: _Optional[_Union[_repository_pb2.ConnectionStatus, str]] = ...) -> None: ...

class ListBranchesRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class ListBranchesResponse(_message.Message):
    __slots__ = ("branches",)
    BRANCHES_FIELD_NUMBER: _ClassVar[int]
    branches: _containers.RepeatedCompositeFieldContainer[_repository_pb2.Branch]
    def __init__(self, branches: _Optional[_Iterable[_Union[_repository_pb2.Branch, _Mapping]]] = ...) -> None: ...

class PushResultRequest(_message.Message):
    __slots__ = ("identifier", "target_branch", "create_new_repository")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    TARGET_BRANCH_FIELD_NUMBER: _ClassVar[int]
    CREATE_NEW_REPOSITORY_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    target_branch: str
    create_new_repository: bool
    def __init__(self, identifier: _Optional[int] = ..., target_branch: _Optional[str] = ..., create_new_repository: bool = ...) -> None: ...

class PushResultResponse(_message.Message):
    __slots__ = ("pushed_branch", "new_repository_url")
    PUSHED_BRANCH_FIELD_NUMBER: _ClassVar[int]
    NEW_REPOSITORY_URL_FIELD_NUMBER: _ClassVar[int]
    pushed_branch: str
    new_repository_url: str
    def __init__(self, pushed_branch: _Optional[str] = ..., new_repository_url: _Optional[str] = ...) -> None: ...
