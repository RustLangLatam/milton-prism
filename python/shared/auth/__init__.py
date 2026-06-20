"""Bearer JWT auth extractor for gRPC handlers (A.4)."""
from __future__ import annotations

from .extractor import AuthenticationError, BearerAuthExtractor

__all__ = ["AuthenticationError", "BearerAuthExtractor"]
