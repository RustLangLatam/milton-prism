"""Typed config loading — thin wrapper over pydantic-settings (A.1)."""
from __future__ import annotations

from .loader import BaseServiceConfig, GrpcServerConfig, MongoConfig

__all__ = ["BaseServiceConfig", "GrpcServerConfig", "MongoConfig"]
