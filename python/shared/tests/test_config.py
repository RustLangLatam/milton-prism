"""Tests for typed config loading (A.1, A.8)."""
from __future__ import annotations

import pytest

from shared.config import BaseServiceConfig, GrpcServerConfig, MongoConfig


def test_mongo_config_defaults() -> None:
    cfg = MongoConfig()
    assert cfg.uri == "mongodb://localhost:27017"
    assert cfg.database == "milton_prism"


def test_grpc_config_defaults() -> None:
    cfg = GrpcServerConfig()
    assert cfg.host == "0.0.0.0"
    assert cfg.port == 50051
    assert cfg.max_workers == 10


def test_mongo_config_reads_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("MONGO_URI", "mongodb://db:27017")
    monkeypatch.setenv("MONGO_DATABASE", "test_db")
    cfg = MongoConfig()
    assert cfg.uri == "mongodb://db:27017"
    assert cfg.database == "test_db"


def test_grpc_config_reads_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("GRPC_PORT", "50099")
    cfg = GrpcServerConfig()
    assert cfg.port == 50099


def test_base_service_config_composes_sub_configs() -> None:
    cfg = BaseServiceConfig()
    assert isinstance(cfg.mongo, MongoConfig)
    assert isinstance(cfg.grpc, GrpcServerConfig)
