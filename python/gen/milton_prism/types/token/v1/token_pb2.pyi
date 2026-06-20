from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AuthorizationTokens(_message.Message):
    __slots__ = ("access_token", "refresh_token", "expires_in")
    ACCESS_TOKEN_FIELD_NUMBER: _ClassVar[int]
    REFRESH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_IN_FIELD_NUMBER: _ClassVar[int]
    access_token: Token
    refresh_token: Token
    expires_in: int
    def __init__(self, access_token: _Optional[_Union[Token, _Mapping]] = ..., refresh_token: _Optional[_Union[Token, _Mapping]] = ..., expires_in: _Optional[int] = ...) -> None: ...

class Token(_message.Message):
    __slots__ = ("value", "expire_time")
    VALUE_FIELD_NUMBER: _ClassVar[int]
    EXPIRE_TIME_FIELD_NUMBER: _ClassVar[int]
    value: str
    expire_time: _timestamp_pb2.Timestamp
    def __init__(self, value: _Optional[str] = ..., expire_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...
