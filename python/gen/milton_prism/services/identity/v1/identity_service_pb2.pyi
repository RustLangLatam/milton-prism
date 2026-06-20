from google.api import annotations_pb2 as _annotations_pb2
from google.api import client_pb2 as _client_pb2
from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import empty_pb2 as _empty_pb2
from google.protobuf import field_mask_pb2 as _field_mask_pb2
from milton_prism.types.identity.v1 import user_pb2 as _user_pb2
from milton_prism.types.pagination.v1 import pagination_pb2 as _pagination_pb2
from milton_prism.types.query_params.v1 import query_params_pb2 as _query_params_pb2
from milton_prism.types.token.v1 import token_pb2 as _token_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2_1
from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class CreateUserRequest(_message.Message):
    __slots__ = ("user", "password")
    USER_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    user: _user_pb2.User
    password: str
    def __init__(self, user: _Optional[_Union[_user_pb2.User, _Mapping]] = ..., password: _Optional[str] = ...) -> None: ...

class GetUserRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class ListUsersRequest(_message.Message):
    __slots__ = ("filter", "page_params")
    FILTER_FIELD_NUMBER: _ClassVar[int]
    PAGE_PARAMS_FIELD_NUMBER: _ClassVar[int]
    filter: _user_pb2.UsersFilter
    page_params: _query_params_pb2.PageQueryParams
    def __init__(self, filter: _Optional[_Union[_user_pb2.UsersFilter, _Mapping]] = ..., page_params: _Optional[_Union[_query_params_pb2.PageQueryParams, _Mapping]] = ...) -> None: ...

class ListUsersResponse(_message.Message):
    __slots__ = ("users", "pagination")
    USERS_FIELD_NUMBER: _ClassVar[int]
    PAGINATION_FIELD_NUMBER: _ClassVar[int]
    users: _containers.RepeatedCompositeFieldContainer[_user_pb2.User]
    pagination: _pagination_pb2.Pagination
    def __init__(self, users: _Optional[_Iterable[_Union[_user_pb2.User, _Mapping]]] = ..., pagination: _Optional[_Union[_pagination_pb2.Pagination, _Mapping]] = ...) -> None: ...

class UpdateUserRequest(_message.Message):
    __slots__ = ("user", "update_mask")
    USER_FIELD_NUMBER: _ClassVar[int]
    UPDATE_MASK_FIELD_NUMBER: _ClassVar[int]
    user: _user_pb2.User
    update_mask: _field_mask_pb2.FieldMask
    def __init__(self, user: _Optional[_Union[_user_pb2.User, _Mapping]] = ..., update_mask: _Optional[_Union[_field_mask_pb2.FieldMask, _Mapping]] = ...) -> None: ...

class DeleteUserRequest(_message.Message):
    __slots__ = ("identifier",)
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    def __init__(self, identifier: _Optional[int] = ...) -> None: ...

class AuthenticateUserRequest(_message.Message):
    __slots__ = ("email", "password")
    EMAIL_FIELD_NUMBER: _ClassVar[int]
    PASSWORD_FIELD_NUMBER: _ClassVar[int]
    email: str
    password: str
    def __init__(self, email: _Optional[str] = ..., password: _Optional[str] = ...) -> None: ...

class RefreshTokenRequest(_message.Message):
    __slots__ = ("refresh_token",)
    REFRESH_TOKEN_FIELD_NUMBER: _ClassVar[int]
    refresh_token: str
    def __init__(self, refresh_token: _Optional[str] = ...) -> None: ...

class LogoutRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class GetCurrentUserRequest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...
