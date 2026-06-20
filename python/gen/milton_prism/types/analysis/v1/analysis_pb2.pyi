from google.api import field_behavior_pb2 as _field_behavior_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from openapiv3 import annotations_pb2 as _annotations_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class AnalysisState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ANALYSIS_STATE_UNSPECIFIED: _ClassVar[AnalysisState]
    ANALYSIS_STATE_RUNNING: _ClassVar[AnalysisState]
    ANALYSIS_STATE_COMPLETED: _ClassVar[AnalysisState]
    ANALYSIS_STATE_FAILED: _ClassVar[AnalysisState]

class TechnologyStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    TECHNOLOGY_STATUS_UNSPECIFIED: _ClassVar[TechnologyStatus]
    TECHNOLOGY_STATUS_CURRENT: _ClassVar[TechnologyStatus]
    TECHNOLOGY_STATUS_OUTDATED: _ClassVar[TechnologyStatus]
    TECHNOLOGY_STATUS_END_OF_LIFE: _ClassVar[TechnologyStatus]

class Severity(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SEVERITY_UNSPECIFIED: _ClassVar[Severity]
    SEVERITY_LOW: _ClassVar[Severity]
    SEVERITY_MEDIUM: _ClassVar[Severity]
    SEVERITY_HIGH: _ClassVar[Severity]
    SEVERITY_CRITICAL: _ClassVar[Severity]
ANALYSIS_STATE_UNSPECIFIED: AnalysisState
ANALYSIS_STATE_RUNNING: AnalysisState
ANALYSIS_STATE_COMPLETED: AnalysisState
ANALYSIS_STATE_FAILED: AnalysisState
TECHNOLOGY_STATUS_UNSPECIFIED: TechnologyStatus
TECHNOLOGY_STATUS_CURRENT: TechnologyStatus
TECHNOLOGY_STATUS_OUTDATED: TechnologyStatus
TECHNOLOGY_STATUS_END_OF_LIFE: TechnologyStatus
SEVERITY_UNSPECIFIED: Severity
SEVERITY_LOW: Severity
SEVERITY_MEDIUM: Severity
SEVERITY_HIGH: Severity
SEVERITY_CRITICAL: Severity

class AnalysisSummary(_message.Message):
    __slots__ = ("identifier", "repository_id", "migration_id", "state", "technologies", "vulnerabilities", "dependency_graph", "total_files", "total_lines", "create_time", "update_time", "delete_time", "purge_time")
    IDENTIFIER_FIELD_NUMBER: _ClassVar[int]
    REPOSITORY_ID_FIELD_NUMBER: _ClassVar[int]
    MIGRATION_ID_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    TECHNOLOGIES_FIELD_NUMBER: _ClassVar[int]
    VULNERABILITIES_FIELD_NUMBER: _ClassVar[int]
    DEPENDENCY_GRAPH_FIELD_NUMBER: _ClassVar[int]
    TOTAL_FILES_FIELD_NUMBER: _ClassVar[int]
    TOTAL_LINES_FIELD_NUMBER: _ClassVar[int]
    CREATE_TIME_FIELD_NUMBER: _ClassVar[int]
    UPDATE_TIME_FIELD_NUMBER: _ClassVar[int]
    DELETE_TIME_FIELD_NUMBER: _ClassVar[int]
    PURGE_TIME_FIELD_NUMBER: _ClassVar[int]
    identifier: int
    repository_id: int
    migration_id: int
    state: AnalysisState
    technologies: _containers.RepeatedCompositeFieldContainer[Technology]
    vulnerabilities: _containers.RepeatedCompositeFieldContainer[Vulnerability]
    dependency_graph: _containers.RepeatedCompositeFieldContainer[DependencyEdge]
    total_files: int
    total_lines: int
    create_time: _timestamp_pb2.Timestamp
    update_time: _timestamp_pb2.Timestamp
    delete_time: _timestamp_pb2.Timestamp
    purge_time: _timestamp_pb2.Timestamp
    def __init__(self, identifier: _Optional[int] = ..., repository_id: _Optional[int] = ..., migration_id: _Optional[int] = ..., state: _Optional[_Union[AnalysisState, str]] = ..., technologies: _Optional[_Iterable[_Union[Technology, _Mapping]]] = ..., vulnerabilities: _Optional[_Iterable[_Union[Vulnerability, _Mapping]]] = ..., dependency_graph: _Optional[_Iterable[_Union[DependencyEdge, _Mapping]]] = ..., total_files: _Optional[int] = ..., total_lines: _Optional[int] = ..., create_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., update_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., delete_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., purge_time: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ...) -> None: ...

class Technology(_message.Message):
    __slots__ = ("name", "detected_version", "latest_version", "status", "category")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DETECTED_VERSION_FIELD_NUMBER: _ClassVar[int]
    LATEST_VERSION_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    CATEGORY_FIELD_NUMBER: _ClassVar[int]
    name: str
    detected_version: str
    latest_version: str
    status: TechnologyStatus
    category: str
    def __init__(self, name: _Optional[str] = ..., detected_version: _Optional[str] = ..., latest_version: _Optional[str] = ..., status: _Optional[_Union[TechnologyStatus, str]] = ..., category: _Optional[str] = ...) -> None: ...

class Vulnerability(_message.Message):
    __slots__ = ("identifier_ref", "severity", "component", "description", "fixed_in_version")
    IDENTIFIER_REF_FIELD_NUMBER: _ClassVar[int]
    SEVERITY_FIELD_NUMBER: _ClassVar[int]
    COMPONENT_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    FIXED_IN_VERSION_FIELD_NUMBER: _ClassVar[int]
    identifier_ref: str
    severity: Severity
    component: str
    description: str
    fixed_in_version: str
    def __init__(self, identifier_ref: _Optional[str] = ..., severity: _Optional[_Union[Severity, str]] = ..., component: _Optional[str] = ..., description: _Optional[str] = ..., fixed_in_version: _Optional[str] = ...) -> None: ...

class DependencyEdge(_message.Message):
    __slots__ = ("from_module", "to_module", "weight")
    FROM_MODULE_FIELD_NUMBER: _ClassVar[int]
    TO_MODULE_FIELD_NUMBER: _ClassVar[int]
    WEIGHT_FIELD_NUMBER: _ClassVar[int]
    from_module: str
    to_module: str
    weight: int
    def __init__(self, from_module: _Optional[str] = ..., to_module: _Optional[str] = ..., weight: _Optional[int] = ...) -> None: ...
