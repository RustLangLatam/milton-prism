"""DomainError — typed error value for service application layers (A.4).

Domain and application layers import only DomainError from here (no grpc).
Handlers import map_error from shared.errors.mapper which is allowed to
reference grpc.StatusCode (infrastructure layer only).
"""
from __future__ import annotations


class DomainError(Exception):
    """Typed domain error carrying an orchestrator-assigned code (e.g. IDN201)."""

    def __init__(self, code: str, message: str) -> None:
        super().__init__(f"[{code}] {message}")
        self.code = code
        self.message = message
