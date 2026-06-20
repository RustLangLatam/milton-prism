"""Composition root for the repository service (A.3).

The ONLY place that wires adapters to ports and assembles the application
service. No other module may import from both application/ and infrastructure/.
"""
from __future__ import annotations

from services.repository.application.service import RepositoryService
from services.repository.infrastructure.grpc_handlers.repository_handler import (
    RepositoryServicer,
)
from services.repository.infrastructure.repositories.git_client import NoOpGitClient
from services.repository.infrastructure.repositories.repository_repository import (
    MongoRepositoryRepository,
)
from shared.auth import BearerAuthExtractor
from shared.config import BaseServiceConfig
from shared.mongo_client import MongoClientBuilder, MotorTransactionManager


class RepositoryWire:
    """Assembled repository service graph. Call close() on shutdown."""

    def __init__(self, cfg: BaseServiceConfig, jwt_secret: str) -> None:
        self._mongo_builder = MongoClientBuilder(
            uri=cfg.mongo.uri,
            database=cfg.mongo.database,
        )
        db = self._mongo_builder.build()

        repo = MongoRepositoryRepository(db)
        tx = MotorTransactionManager(db.client)
        git = NoOpGitClient()
        # Identity client: None unless caller wires an IdentityClientAdapter.
        # When None, CreateRepository skips the user-existence check (safe for dev).
        identity = None

        self.service = RepositoryService(
            repo=repo,
            tx=tx,
            identity=identity,
            git=git,
        )
        auth = BearerAuthExtractor(jwt_secret)
        self.servicer = RepositoryServicer(self.service, auth)

    def close(self) -> None:
        self._mongo_builder.close()
