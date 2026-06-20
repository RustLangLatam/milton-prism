"""Identity service entrypoint — starts the async gRPC server (A.2)."""
from __future__ import annotations

import asyncio
import os
import signal

import grpc.aio
from milton_prism.services.identity.v1.identity_service_pb2_grpc import (
    add_IdentityServiceServicer_to_server,
)

from services.identity.wire import IdentityWire
from shared.config import BaseServiceConfig
from shared.logging import _configure_stdout_handler, infof


async def serve() -> None:
    _configure_stdout_handler()

    cfg = BaseServiceConfig()
    jwt_secret = os.environ.get("JWT_SECRET", "change-me-in-production-minimum-32-chars!!")

    wire = IdentityWire(cfg, jwt_secret)
    server = grpc.aio.server()
    add_IdentityServiceServicer_to_server(wire.servicer, server)

    listen_addr = f"{cfg.grpc.host}:{cfg.grpc.port}"
    server.add_insecure_port(listen_addr)

    infof("identity.server", status="starting", addr=listen_addr)
    await server.start()
    infof("identity.server", status="started", addr=listen_addr)

    loop = asyncio.get_running_loop()
    stop_event = asyncio.Event()

    def _shutdown() -> None:
        infof("identity.server", status="shutting-down")
        stop_event.set()

    loop.add_signal_handler(signal.SIGTERM, _shutdown)
    loop.add_signal_handler(signal.SIGINT, _shutdown)

    await stop_event.wait()
    await server.stop(grace=5)
    wire.close()
    infof("identity.server", status="stopped")


if __name__ == "__main__":
    asyncio.run(serve())
