"""Structured logger — the only logging surface in the Python services.

Mirrors Go's applog package. All service code MUST use these functions.
Using bare print(), logging.*, or any ad-hoc logger is forbidden (A.7).
"""
from __future__ import annotations

import logging
import sys
from typing import Any

_logger = logging.getLogger("milton_prism")
_logger.setLevel(logging.DEBUG)


def _configure_stdout_handler() -> None:
    """Attach a stdout handler when running as a service (not under pytest)."""
    if not _logger.handlers:
        _handler = logging.StreamHandler(sys.stdout)
        _handler.setFormatter(logging.Formatter("%(message)s"))
        _logger.addHandler(_handler)


def _fmt(context: str, **kwargs: Any) -> str:
    """Build structured log line: '<context>: key=value key2=value2'."""
    if not kwargs:
        return context
    pairs = " ".join(f"{k}={v}" for k, v in kwargs.items())
    return f"{context}: {pairs}"


def infof(context: str, **kwargs: Any) -> None:
    """Log at INFO level with structured key=value pairs."""
    _logger.info(_fmt(context, **kwargs))


def warningf(context: str, **kwargs: Any) -> None:
    """Log at WARNING level with structured key=value pairs."""
    _logger.warning(_fmt(context, **kwargs))


def errorf(context: str, **kwargs: Any) -> None:
    """Log at ERROR level with structured key=value pairs."""
    _logger.error(_fmt(context, **kwargs))
