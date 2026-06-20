"""Composition root for the identity service (A.3).

This is the ONLY place that wires adapters to ports and assembles the
application service. No other module may import from both application/ and
infrastructure/ simultaneously.
"""
from __future__ import annotations

from services.identity.application.service import IdentityService
from services.identity.infrastructure.grpc_handlers.identity_handler import IdentityServicer
from services.identity.infrastructure.repositories.password_hasher import BcryptPasswordHasher
from services.identity.infrastructure.repositories.session_store import MongoSessionStore
from services.identity.infrastructure.repositories.token_manager import JwtTokenManager
from services.identity.infrastructure.repositories.user_repository import MongoUserRepository
from shared.config import BaseServiceConfig
from shared.mongo_client import MongoClientBuilder, MotorTransactionManager


class IdentityWire:
    """Assembled identity service graph. Call close() on shutdown."""

    def __init__(self, cfg: BaseServiceConfig, jwt_secret: str) -> None:
        self._mongo_builder = MongoClientBuilder(
            uri=cfg.mongo.uri,
            database=cfg.mongo.database,
        )
        db = self._mongo_builder.build()

        repo = MongoUserRepository(db)
        sessions = MongoSessionStore(db)
        hasher = BcryptPasswordHasher()
        tokens = JwtTokenManager(secret=jwt_secret)
        tx = MotorTransactionManager(db.client)

        self.service = IdentityService(
            repo=repo,
            tx=tx,
            hasher=hasher,
            tokens=tokens,
            sessions=sessions,
        )
        self.servicer = IdentityServicer(self.service)

    def close(self) -> None:
        self._mongo_builder.close()
