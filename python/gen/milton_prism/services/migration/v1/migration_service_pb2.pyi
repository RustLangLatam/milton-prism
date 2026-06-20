from google.api import annotations_pb2 as _annotations_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import empty_pb2 as _empty_pb2
from milton_prism.types.migration.v1 import migration_pb2 as _migration_pb2
from milton_prism.types.pagination.v1 import pagination_pb2 as _pagination_pb2
from milton_prism.types.query_params.v1 import query_params_pb2 as _query_params_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2_1
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateMigrationRequest(_message.Message):
    __slots__ = ("migration",)
    MIGRATION_FIELD_NUMBER: _ClassVar[int]
    migration: _migration_pb2.Migration
    def __init__(self, migration: _Optional[_Union[_migration_pb2.Migration, _Mapping]] = ...) -> None: ...

class GetMigrationRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class ListMigrationsRequest(_message.Message):
    __slots__ = ("filter", "page_params")
    FILTER_FIELD_NUMBER: _ClassVar[int]
    PAGE_PARAMS_FIELD_NUMBER: _ClassVar[int]
    filter: _migration_pb2.MigrationsFilter
    page_params: _query_params_pb2.PageQueryParams
    def __init__(self, filter: _Optional[_Union[_migration_pb2.MigrationsFilter, _Mapping]] = ..., page_params: _Optional[_Union[_query_params_pb2.PageQueryParams, _Mapping]] = ...) -> None: ...

class ListMigrationsResponse(_message.Message):
    __slots__ = ("migrations", "pagination")
    MIGRATIONS_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    migrations: _containers.RepeatedCompositeFieldContainer[_migration_pb2.Migration]
    pagination: _pagination_pb2.Pagination
    def __init__(self, migrations: _Optional[_Iterable[_Union[_migration_pb2.Migration, _Mapping]]] = ..., pagination: _Optional[_Union[_pagination_pb2.Pagination, _Mapping]] = ...) -> None: ...

class DeleteMigrationRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class StartMigrationRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class ApproveDesignRequest(_message.Message):
    __slots__ = ("identifier", "approved")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    APPROVED_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    approved: bool
    def __init__(self, identifier: _Optional[int] = ..., approved: bool = ...) -> None: ...

class CancelMigrationRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...
