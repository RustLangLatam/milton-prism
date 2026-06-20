"""gRPC channel builder for inter-service clients (A.3).

Services obtain channels from this builder; stubs are created per service.
"""
from __future__ import annotations

import grpc
import grpc.aio


class GrpcClientBuilder:
    """Builds an async gRPC channel for a downstream service.

    Example:
        builder = GrpcClientBuilder(host="identity-svc", port=50051)
        channel = builder.build()
        stub = IdentityServiceStub(channel)
    """

    def __init__(
        self,
        host: str,
        port: int,
        *,
        insecure: bool = True,
    ) -> None:
        self._host = host
        self._port = port
        self._insecure = insecure

    def build(self) -> grpc.aio.Channel:
        """Return an async gRPC channel. Caller owns lifecycle (await channel.close())."""
        target = f"{self._host}:{self._port}"
        if self._insecure:
            return grpc.aio.insecure_channel(target)
        return grpc.aio.secure_channel(target, grpc.ssl_channel_credentials())
