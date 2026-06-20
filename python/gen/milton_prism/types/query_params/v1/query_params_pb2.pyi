from google.api import field_behavior_pb2 as _field_behavior_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class PageQueryParams(_message.Message):
    __slots__ = ("order", "page_number", "page_size", "sort_by")
    class Order(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
        __slots__ = ()
        ORDER_DESC_UNSPECIFIED: _ClassVar[PageQueryParams.Order]
        ORDER_ASC: _ClassVar[PageQueryParams.Order]
    ORDER_DESC_UNSPECIFIED: PageQueryParams.Order
    ORDER_ASC: PageQueryParams.Order
    ORDER_FIELD_NUMBER: _ClassVar[int]
    PAGE_NUMBER_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    SORT_BY_FIELD_NUMBER: _ClassVar[int]
    order: PageQueryParams.Order
    page_number: int
    page_size: int
    sort_by: str
    def __init__(self, order: _Optional[_Union[PageQueryParams.Order, str]] = ..., page_number: _Optional[int] = ..., page_size: _Optional[int] = ..., sort_by: _Optional[str] = ...) -> None: ...
