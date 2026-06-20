from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class MigrationState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MIGRATION_STATE_UNSPECIFIED: _ClassVar[MigrationState]
    MIGRATION_STATE_PENDING: _ClassVar[MigrationState]
    MIGRATION_STATE_ANALYZING: _ClassVar[MigrationState]
    MIGRATION_STATE_DESIGNING: _ClassVar[MigrationState]
    MIGRATION_STATE_AWAITING_APPROVAL: _ClassVar[MigrationState]
    MIGRATION_STATE_GENERATING: _ClassVar[MigrationState]
    MIGRATION_STATE_TESTING: _ClassVar[MigrationState]
    MIGRATION_STATE_READY: _ClassVar[MigrationState]
    MIGRATION_STATE_PUSHED: _ClassVar[MigrationState]
    MIGRATION_STATE_FAILED: _ClassVar[MigrationState]
    MIGRATION_STATE_CANCELLED: _ClassVar[MigrationState]

class TargetLanguage(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TARGET_LANGUAGE_UNSPECIFIED: _ClassVar[TargetLanguage]
    TARGET_LANGUAGE_GO: _ClassVar[TargetLanguage]
    TARGET_LANGUAGE_RUST: _ClassVar[TargetLanguage]
    TARGET_LANGUAGE_PYTHON: _ClassVar[TargetLanguage]

class TargetDatabase(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TARGET_DATABASE_UNSPECIFIED: _ClassVar[TargetDatabase]
    TARGET_DATABASE_MONGODB: _ClassVar[TargetDatabase]
    TARGET_DATABASE_POSTGRES: _ClassVar[TargetDatabase]
    TARGET_DATABASE_MARIADB: _ClassVar[TargetDatabase]

class Transport(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TRANSPORT_UNSPECIFIED: _ClassVar[Transport]
    TRANSPORT_GRPC: _ClassVar[Transport]
    TRANSPORT_HTTP: _ClassVar[Transport]
    TRANSPORT_NATS: _ClassVar[Transport]

class OutputTarget(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    OUTPUT_TARGET_UNSPECIFIED: _ClassVar[OutputTarget]
    OUTPUT_TARGET_NEW_BRANCH: _ClassVar[OutputTarget]
    OUTPUT_TARGET_NEW_REPOSITORY: _ClassVar[OutputTarget]
MIGRATION_STATE_UNSPECIFIED: MigrationState
MIGRATION_STATE_PENDING: MigrationState
MIGRATION_STATE_ANALYZING: MigrationState
MIGRATION_STATE_DESIGNING: MigrationState
MIGRATION_STATE_AWAITING_APPROVAL: MigrationState
MIGRATION_STATE_GENERATING: MigrationState
MIGRATION_STATE_TESTING: MigrationState
MIGRATION_STATE_READY: MigrationState
MIGRATION_STATE_PUSHED: MigrationState
MIGRATION_STATE_FAILED: MigrationState
MIGRATION_STATE_CANCELLED: MigrationState
TARGET_LANGUAGE_UNSPECIFIED: TargetLanguage
TARGET_LANGUAGE_GO: TargetLanguage
TARGET_LANGUAGE_RUST: TargetLanguage
TARGET_LANGUAGE_PYTHON: TargetLanguage
TARGET_DATABASE_UNSPECIFIED: TargetDatabase
TARGET_DATABASE_MONGODB: TargetDatabase
TARGET_DATABASE_POSTGRES: TargetDatabase
TARGET_DATABASE_MARIADB: TargetDatabase
TRANSPORT_UNSPECIFIED: Transport
TRANSPORT_GRPC: Transport
TRANSPORT_HTTP: Transport
TRANSPORT_NATS: Transport
OUTPUT_TARGET_UNSPECIFIED: OutputTarget
OUTPUT_TARGET_NEW_BRANCH: OutputTarget
OUTPUT_TARGET_NEW_REPOSITORY: OutputTarget

class Migration(_message.Message):
    __slots__ = ("identifier", "repository_id", "owner_user_id", "source_branch", "state", "target", "analysis_summary_id", "plan", "output", "create_time", "update_time", "delete_time", "purge_time")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    REPOSITORY_ID_FIELD_NUMBER: _ClassVar[int]
    OWNER_USER_ID_FIELD_NUMBER: _ClassVar[int]
    SOURCE_BRANCH_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    TARGET_FIELD_NUMBER: _ClassVar[int]
    ANALYSIS_SUMMARY_ID_FIELD_NUMBER: _ClassVar[int]
    PLAN_FIELD_NUMBER: _ClassVar[int]
    OUTPUT_FIELD_NUMBER: _ClassVar[int]
    CREATE_TIME_FIELD_NUMBER: _ClassVar[int]
    UPDATE_TIME_FIELD_NUMBER: _ClassVar[int]
    DELETE_TIME_FIELD_NUMBER: _ClassVar[int]
    PURGE_TIME_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    repository_id: int
    owner_user_id: int
    source_branch: str
    state: MigrationState
    target: TargetConfig
    analysis_summary_id: int
    plan: RestructurePlan
    output: MigrationOutput
    create_time: _timestamp_pb2.Timestamp
    update_time: _timestamp_pb2.Timestamp
    delete_time: _timestamp_pb2.Timestamp
    purge_time: _timestamp_pb2.Timestamp
    def __init__(self, identifier: _Optional[int] = ..., repository_id: _Optional[int] = ..., owner_user_id: _Optional[int] = ..., source_branch: _Optional[str] = ..., state: _Optional[_Union[MigrationState, str]] = ..., target: _Optional[_Union[TargetConfig, _Mapping]] = ..., analysis_summary_id: _Optional[int] = ..., plan: _Optional[_Union[RestructurePlan, _Mapping]] = ..., output: _Optional[_Union[MigrationOutput, _Mapping]] = ..., create_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., update_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., delete_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., purge_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class TargetConfig(_message.Message):
    __slots__ = ("language", "database", "inter_service_transport", "use_api_gateway")
    LANGUAGE_FIELD_NUMBER: _ClassVar[int]
    DATABASE_FIELD_NUMBER: _ClassVar[int]
    INTER_SERVICE_TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    USE_API_GATEWAY_FIELD_NUMBER: _ClassVar[int]
    language: TargetLanguage
    database: TargetDatabase
    inter_service_transport: Transport
    use_api_gateway: bool
    def __init__(self, language: _Optional[_Union[TargetLanguage, str]] = ..., database: _Optional[_Union[TargetDatabase, str]] = ..., inter_service_transport: _Optional[_Union[Transport, str]] = ..., use_api_gateway: bool = ...) -> None: ...

class RestructurePlan(_message.Message):
    __slots__ = ("services", "rationale")
    SERVICES_FIELD_NUMBER: _ClassVar[int]
    RATIONALE_FIELD_NUMBER: _ClassVar[int]
    services: _containers.RepeatedCompositeFieldContainer[ProposedService]
    rationale: str
    def __init__(self, services: _Optional[_Iterable[_Union[ProposedService, _Mapping]]] = ..., rationale: _Optional[str] = ...) -> None: ...

class ProposedService(_message.Message):
    __slots__ = ("name", "error_prefix", "owned_resources", "inter_service_deps")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ERROR_PREFIX_FIELD_NUMBER: _ClassVar[int]
    OWNED_RESOURCES_FIELD_NUMBER: _ClassVar[int]
    INTER_SERVICE_DEPS_FIELD_NUMBER: _ClassVar[int]
    name: str
    error_prefix: str
    owned_resources: _containers.RepeatedScalarFieldContainer[str]
    inter_service_deps: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, name: _Optional[str] = ..., error_prefix: _Optional[str] = ..., owned_resources: _Optional[_Iterable[str]] = ..., inter_service_deps: _Optional[_Iterable[str]] = ...) -> None: ...

class MigrationOutput(_message.Message):
    __slots__ = ("output_target", "branch_name", "new_repository_url")
    OUTPUT_TARGET_FIELD_NUMBER: _ClassVar[int]
    BRANCH_NAME_FIELD_NUMBER: _ClassVar[int]
    NEW_REPOSITORY_URL_FIELD_NUMBER: _ClassVar[int]
    output_target: OutputTarget
    branch_name: str
    new_repository_url: str
    def __init__(self, output_target: _Optional[_Union[OutputTarget, str]] = ..., branch_name: _Optional[str] = ..., new_repository_url: _Optional[str] = ...) -> None: ...

class MigrationsFilter(_message.Message):
    __slots__ = ("owner_user_id", "repository_id", "state")
    OWNER_USER_ID_FIELD_NUMBER: _ClassVar[int]
    REPOSITORY_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    owner_user_id: int
    repository_id: int
    state: MigrationState
    def __init__(self, owner_user_id: _Optional[int] = ..., repository_id: _Optional[int] = ..., state: _Optional[_Union[MigrationState, str]] = ...) -> None: ...
