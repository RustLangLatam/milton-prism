"""Repository service entrypoint (A.2)."""
from __future__ import annotations

import asyncio
import os
import signal

import grpc
import grpc.aio
from milton_prism.services.repository.v1.repository_service_pb2_grpc import (
    add_RepositoryServiceServicer_to_server,
)

from services.repository.wire import RepositoryWire
from shared.config import BaseServiceConfig
from shared.logging import _configure_stdout_handler, infof


async def serve() -> None:
    _configure_stdout_handler()
    cfg = BaseServiceConfig()
    jwt_secret = os.environ.get("JWT_SECRET", "dev-secret-change-in-production")
    wire = RepositoryWire(cfg, jwt_secret)

    server = grpc.aio.server(
        options=[
            ("grpc.max_send_message_length", 10 * 1024 * 1024),
            ("grpc.max_receive_message_length", 10 * 1024 * 1024),
        ]
    )
    add_RepositoryServiceServicer_to_server(wire.servicer, server)
    listen_addr = f"{cfg.grpc.host}:{cfg.grpc.port}"
    server.add_insecure_port(listen_addr)

    loop = asyncio.get_event_loop()
    stop_event = asyncio.Event()

    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop_event.set)

    await server.start()
    infof("repository.__main__", event="server_started", addr=listen_addr)
    await stop_event.wait()
    await server.stop(grace=5)
    wire.close()
    infof("repository.__main__", event="server_stopped")


if __name__ == "__main__":
    asyncio.run(serve())
