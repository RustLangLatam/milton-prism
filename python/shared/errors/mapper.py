"""map_error — DomainError → gRPC status converter (A.4).

Only infrastructure (handlers) may import from this module. Domain and
application layers raise DomainError and must NOT import from here.
"""
from __future__ import annotations

import grpc

from shared.errors.domain_error import DomainError

# Validation (1xx) → INVALID_ARGUMENT; Domain (2xx) → NOT_FOUND default; Internal (5xx) → INTERNAL
_PREFIX_STATUS: dict[str, grpc.StatusCode] = {
    "1": grpc.StatusCode.INVALID_ARGUMENT,
    "2": grpc.StatusCode.NOT_FOUND,
    "5": grpc.StatusCode.INTERNAL,
}

_CODE_STATUS: dict[str, grpc.StatusCode] = {
    "IDN202": grpc.StatusCode.ALREADY_EXISTS,
    "IDN203": grpc.StatusCode.UNAUTHENTICATED,
    "IDN204": grpc.StatusCode.PERMISSION_DENIED,
    "IDN205": grpc.StatusCode.PERMISSION_DENIED,
    "IDN206": grpc.StatusCode.UNAUTHENTICATED,
    "IDN207": grpc.StatusCode.UNAUTHENTICATED,
    # Repository overrides: 2xx default is NOT_FOUND; exceptions below
    "REPO202": grpc.StatusCode.ALREADY_EXISTS,
    "REPO204": grpc.StatusCode.INTERNAL,
    "REPO205": grpc.StatusCode.PERMISSION_DENIED,
    # Migration overrides: MIG202 is FAILED_PRECONDITION (state machine violation)
    "MIG202": grpc.StatusCode.FAILED_PRECONDITION,
    "MIG205": grpc.StatusCode.PERMISSION_DENIED,
}


def map_error(err: Exception) -> tuple[grpc.StatusCode, str]:
    """Map a DomainError to (gRPC status code, detail string).

    For non-DomainError exceptions, returns INTERNAL.
    """
    if not isinstance(err, DomainError):
        return grpc.StatusCode.INTERNAL, "Failure_Internal"

    if err.code in _CODE_STATUS:
        return _CODE_STATUS[err.code], err.message

    if len(err.code) >= 4:
        prefix_digit = err.code[3]
        status = _PREFIX_STATUS.get(prefix_digit)
        if status is not None:
            return status, err.message

    return grpc.StatusCode.INTERNAL, err.message
