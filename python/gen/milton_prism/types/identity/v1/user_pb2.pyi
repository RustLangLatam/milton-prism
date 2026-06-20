from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class UserState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    USER_STATE_UNSPECIFIED: _ClassVar[UserState]
    USER_STATE_ACTIVE: _ClassVar[UserState]
    USER_STATE_SUSPENDED: _ClassVar[UserState]
    USER_STATE_DELETED: _ClassVar[UserState]
USER_STATE_UNSPECIFIED: UserState
USER_STATE_ACTIVE: UserState
USER_STATE_SUSPENDED: UserState
USER_STATE_DELETED: UserState

class User(_message.Message):
    __slots__ = ("identifier", "email", "display_name", "system_user", "state", "create_time", "update_time", "delete_time", "purge_time")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    EMAIL_FIELD_NUMBER: _ClassVar[int]
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_USER_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    CREATE_TIME_FIELD_NUMBER: _ClassVar[int]
    UPDATE_TIME_FIELD_NUMBER: _ClassVar[int]
    DELETE_TIME_FIELD_NUMBER: _ClassVar[int]
    PURGE_TIME_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    email: str
    display_name: str
    system_user: bool
    state: UserState
    create_time: _timestamp_pb2.Timestamp
    update_time: _timestamp_pb2.Timestamp
    delete_time: _timestamp_pb2.Timestamp
    purge_time: _timestamp_pb2.Timestamp
    def __init__(self, identifier: _Optional[int] = ..., email: _Optional[str] = ..., display_name: _Optional[str] = ..., system_user: bool = ..., state: _Optional[_Union[UserState, str]] = ..., create_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., update_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., delete_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., purge_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class UsersFilter(_message.Message):
    __slots__ = ("state", "email")
    STATE_FIELD_NUMBER: _ClassVar[int]
    EMAIL_FIELD_NUMBER: _ClassVar[int]
    state: UserState
    email: str
    def __init__(self, state: _Optional[_Union[UserState, str]] = ..., email: _Optional[str] = ...) -> None: ...
