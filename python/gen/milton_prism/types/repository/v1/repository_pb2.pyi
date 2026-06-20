from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class GitProvider(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    GIT_PROVIDER_UNSPECIFIED: _ClassVar[GitProvider]
    GIT_PROVIDER_GITHUB: _ClassVar[GitProvider]
    GIT_PROVIDER_GITLAB: _ClassVar[GitProvider]
    GIT_PROVIDER_BITBUCKET: _ClassVar[GitProvider]
    GIT_PROVIDER_GENERIC: _ClassVar[GitProvider]

class RepositoryState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    REPOSITORY_STATE_UNSPECIFIED: _ClassVar[RepositoryState]
    REPOSITORY_STATE_CONNECTED: _ClassVar[RepositoryState]
    REPOSITORY_STATE_DISCONNECTED: _ClassVar[RepositoryState]
    REPOSITORY_STATE_ERROR: _ClassVar[RepositoryState]

class ConnectionStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CONNECTION_STATUS_UNSPECIFIED: _ClassVar[ConnectionStatus]
    CONNECTION_STATUS_OK: _ClassVar[ConnectionStatus]
    CONNECTION_STATUS_AUTH_FAILED: _ClassVar[ConnectionStatus]
    CONNECTION_STATUS_UNREACHABLE: _ClassVar[ConnectionStatus]
GIT_PROVIDER_UNSPECIFIED: GitProvider
GIT_PROVIDER_GITHUB: GitProvider
GIT_PROVIDER_GITLAB: GitProvider
GIT_PROVIDER_BITBUCKET: GitProvider
GIT_PROVIDER_GENERIC: GitProvider
REPOSITORY_STATE_UNSPECIFIED: RepositoryState
REPOSITORY_STATE_CONNECTED: RepositoryState
REPOSITORY_STATE_DISCONNECTED: RepositoryState
REPOSITORY_STATE_ERROR: RepositoryState
CONNECTION_STATUS_UNSPECIFIED: ConnectionStatus
CONNECTION_STATUS_OK: ConnectionStatus
CONNECTION_STATUS_AUTH_FAILED: ConnectionStatus
CONNECTION_STATUS_UNREACHABLE: ConnectionStatus

class Repository(_message.Message):
    __slots__ = ("identifier", "owner_user_id", "provider", "remote_url", "default_branch", "state", "connection_status", "credential_ref", "create_time", "update_time", "delete_time", "purge_time")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    OWNER_USER_ID_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    REMOTE_URL_FIELD_NUMBER: _ClassVar[int]
    DEFAULT_BRANCH_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_STATUS_FIELD_NUMBER: _ClassVar[int]
    CREDENTIAL_REF_FIELD_NUMBER: _ClassVar[int]
    CREATE_TIME_FIELD_NUMBER: _ClassVar[int]
    UPDATE_TIME_FIELD_NUMBER: _ClassVar[int]
    DELETE_TIME_FIELD_NUMBER: _ClassVar[int]
    PURGE_TIME_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    owner_user_id: int
    provider: GitProvider
    remote_url: str
    default_branch: str
    state: RepositoryState
    connection_status: ConnectionStatus
    credential_ref: str
    create_time: _timestamp_pb2.Timestamp
    update_time: _timestamp_pb2.Timestamp
    delete_time: _timestamp_pb2.Timestamp
    purge_time: _timestamp_pb2.Timestamp
    def __init__(self, identifier: _Optional[int] = ..., owner_user_id: _Optional[int] = ..., provider: _Optional[_Union[GitProvider, str]] = ..., remote_url: _Optional[str] = ..., default_branch: _Optional[str] = ..., state: _Optional[_Union[RepositoryState, str]] = ..., connection_status: _Optional[_Union[ConnectionStatus, str]] = ..., credential_ref: _Optional[str] = ..., create_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., update_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., delete_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., purge_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Branch(_message.Message):
    __slots__ = ("name", "commit_sha", "is_default")
    NAME_FIELD_NUMBER: _ClassVar[int]
    COMMIT_SHA_FIELD_NUMBER: _ClassVar[int]
    IS_DEFAULT_FIELD_NUMBER: _ClassVar[int]
    name: str
    commit_sha: str
    is_default: bool
    def __init__(self, name: _Optional[str] = ..., commit_sha: _Optional[str] = ..., is_default: bool = ...) -> None: ...

class RepositoriesFilter(_message.Message):
    __slots__ = ("owner_user_id", "state", "provider")
    OWNER_USER_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    PROVIDER_FIELD_NUMBER: _ClassVar[int]
    owner_user_id: int
    state: RepositoryState
    provider: GitProvider
    def __init__(self, owner_user_id: _Optional[int] = ..., state: _Optional[_Union[RepositoryState, str]] = ..., provider: _Optional[_Union[GitProvider, str]] = ...) -> None: ...
