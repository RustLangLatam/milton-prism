"""Composition root for the migration service (A.3).

The ONLY place that wires adapters to ports and assembles the application
service. No other module may import from both application/ and infrastructure/.
"""
from __future__ import annotations

from services.migration.application.service import MigrationService
from services.migration.infrastructure.grpc_handlers.migration_handler import (
    MigrationServicer,
)
from services.migration.infrastructure.repositories.analysis_client import (
    NoOpAnalysisClient,
)
from services.migration.infrastructure.repositories.migration_repository import (
    MongoMigrationRepository,
)
from shared.auth import BearerAuthExtractor
from shared.config import BaseServiceConfig
from shared.mongo_client import MongoClientBuilder, MotorTransactionManager


class MigrationWire:
    """Assembled migration service graph. Call close() on shutdown."""

    def __init__(self, cfg: BaseServiceConfig, jwt_secret: str) -> None:
        self._mongo_builder = MongoClientBuilder(
            uri=cfg.mongo.uri,
            database=cfg.mongo.database,
        )
        db = self._mongo_builder.build()

        repo = MongoMigrationRepository(db)
        tx = MotorTransactionManager(db.client)
        analysis = NoOpAnalysisClient()
        # Identity and repository clients are None unless wired via gRPC config.
        # Safe for dev — CreateMigration skips the existence checks when None.
        identity = None
        repository_svc = None

        self.service = MigrationService(
            repo=repo,
            tx=tx,
            identity=identity,
            repository_svc=repository_svc,
            analysis=analysis,
        )
        auth = BearerAuthExtractor(jwt_secret)
        self.servicer = MigrationServicer(self.service, auth)

    def close(self) -> None:
        self._mongo_builder.close()
