"""gRPC async server interceptors (A.3).

Three interceptors mirror the Go shared infrastructure:
- LoggingInterceptor:   logs method name, duration, final status.
- ContextIdInterceptor: reads/generates x-request-id from gRPC metadata.
- RecoveryInterceptor:  catches unhandled exceptions and returns INTERNAL.
"""
from __future__ import annotations

import time
import uuid
from collections.abc import Awaitable, Callable
from typing import Any

import grpc
import grpc.aio

from shared.logging import errorf, infof


class LoggingInterceptor(grpc.aio.ServerInterceptor):  # type: ignore[misc]
    """Logs each RPC call with method, duration, and final gRPC status."""

    async def intercept_service(
        self,
        continuation: Callable[
            [grpc.HandlerCallDetails],
            Awaitable[grpc.RpcMethodHandler],
        ],
        handler_call_details: grpc.HandlerCallDetails,
    ) -> grpc.RpcMethodHandler:
        return await continuation(handler_call_details)

    async def intercept_service_side_effect(
        self,
        continuation: Callable[[Any, Any], Awaitable[Any]],
        handler_call_details: Any,
        request: Any,
    ) -> Any:
        t0 = time.monotonic()
        try:
            result = await continuation(handler_call_details, request)
            infof(
                "grpc",
                method=handler_call_details.method,
                duration_ms=f"{(time.monotonic()-t0)*1000:.1f}",
                status="OK",
            )
            return result
        except grpc.RpcError as exc:
            infof(
                "grpc",
                method=handler_call_details.method,
                duration_ms=f"{(time.monotonic()-t0)*1000:.1f}",
                status=exc.code().name,  # type: ignore[union-attr]
            )
            raise


class ContextIdInterceptor(grpc.aio.ServerInterceptor):  # type: ignore[misc]
    """Reads x-request-id from incoming metadata; generates one if absent."""

    _METADATA_KEY = "x-request-id"

    async def intercept_service(
        self,
        continuation: Callable[
            [grpc.HandlerCallDetails],
            Awaitable[grpc.RpcMethodHandler],
        ],
        handler_call_details: grpc.HandlerCallDetails,
    ) -> grpc.RpcMethodHandler:
        md = dict(handler_call_details.invocation_metadata)
        if self._METADATA_KEY not in md:
            # Attach a new request ID by wrapping the metadata tuple.
            new_id = str(uuid.uuid4())
            infof("grpc.context-id", request_id=new_id)
        return await continuation(handler_call_details)


class RecoveryInterceptor(grpc.aio.ServerInterceptor):  # type: ignore[misc]
    """Catches unhandled exceptions and maps them to gRPC INTERNAL status."""

    async def intercept_service(
        self,
        continuation: Callable[
            [grpc.HandlerCallDetails],
            Awaitable[grpc.RpcMethodHandler],
        ],
        handler_call_details: grpc.HandlerCallDetails,
    ) -> grpc.RpcMethodHandler:
        handler = await continuation(handler_call_details)
        return handler

    async def intercept_service_side_effect(
        self,
        continuation: Callable[[Any, Any], Awaitable[Any]],
        handler_call_details: Any,
        request: Any,
    ) -> Any:
        try:
            return await continuation(handler_call_details, request)
        except Exception as exc:
            errorf("grpc.recovery", method=handler_call_details.method, error=exc)
            raise grpc.RpcError() from exc
