"""Typed config loader — services extend BaseServiceConfig (A.1, A.2).

Uses pydantic-settings; values come from environment variables or .env files.
"""
from __future__ import annotations

from pydantic import Field
from pydantic_settings import BaseSettings, SettingsConfigDict


class MongoConfig(BaseSettings):
    """MongoDB connection settings."""

    model_config = SettingsConfigDict(env_prefix="MONGO_", populate_by_name=True)

    uri: str = Field(default="mongodb://localhost:27017", alias="MONGO_URI")
    database: str = Field(default="milton_prism", alias="MONGO_DATABASE")


class GrpcServerConfig(BaseSettings):
    """gRPC server listen settings."""

    model_config = SettingsConfigDict(env_prefix="GRPC_", populate_by_name=True)

    host: str = Field(default="0.0.0.0", alias="GRPC_HOST")
    port: int = Field(default=50051, alias="GRPC_PORT")
    max_workers: int = Field(default=10, alias="GRPC_MAX_WORKERS")


class BaseServiceConfig(BaseSettings):
    """Base config inherited by every service. Extend with service-specific fields."""

    model_config = SettingsConfigDict(env_file=".env", env_file_encoding="utf-8")

    mongo: MongoConfig = Field(default_factory=MongoConfig)
    grpc: GrpcServerConfig = Field(default_factory=GrpcServerConfig)
