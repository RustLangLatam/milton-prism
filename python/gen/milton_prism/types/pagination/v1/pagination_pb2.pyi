from google.api import field_behavior_pb2 as _field_behavior_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Optional as _Optional

DESCRIPTOR: _descriptor.FileDescriptor

class Pagination(_message.Message):
    __slots__ = ("current_page", "page_size", "total_size", "total_pages")
    CURRENT_PAGE_FIELD_NUMBER: _ClassVar[int]
    PAGE_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOTAL_SIZE_FIELD_NUMBER: _ClassVar[int]
    TOTAL_PAGES_FIELD_NUMBER: _ClassVar[int]
    current_page: int
    page_size: int
    total_size: int
    total_pages: int
    def __init__(self, current_page: _Optional[int] = ..., page_size: _Optional[int] = ..., total_size: _Optional[int] = ..., total_pages: _Optional[int] = ...) -> None: ...
