"""Typed error model for Python services (A.4).

Domain and application layers: import DomainError from here.
Handlers: import map_error from shared.errors.mapper (grpc lives there only).
"""
from __future__ import annotations

from .domain_error import DomainError

__all__ = ["DomainError"]
