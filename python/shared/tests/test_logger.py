"""Tests for the structured logger (A.7, A.8)."""
from __future__ import annotations

import logging

import pytest

from shared.logging import _fmt, errorf, infof, warningf


def test_fmt_no_kwargs_returns_context() -> None:
    assert _fmt("identity.create-user") == "identity.create-user"


def test_fmt_with_kwargs_formats_key_value() -> None:
    result = _fmt("identity.create-user", user_id=42, status="OK")
    assert result.startswith("identity.create-user: ")
    assert "user_id=42" in result
    assert "status=OK" in result


def test_infof_logs_at_info_level(caplog: pytest.LogCaptureFixture) -> None:
    with caplog.at_level(logging.INFO, logger="milton_prism"):
        infof("test-context", key="val")
    records = [r for r in caplog.records if r.name == "milton_prism"]
    assert any("test-context: key=val" in r.message for r in records)


def test_warningf_logs_at_warning_level(caplog: pytest.LogCaptureFixture) -> None:
    with caplog.at_level(logging.WARNING, logger="milton_prism"):
        warningf("test-warn", reason="x")
    records = [r for r in caplog.records if r.levelno == logging.WARNING]
    assert any("test-warn: reason=x" in r.message for r in records)


def test_errorf_logs_at_error_level(caplog: pytest.LogCaptureFixture) -> None:
    with caplog.at_level(logging.ERROR, logger="milton_prism"):
        errorf("test-err", code="IDN500")
    records = [r for r in caplog.records if r.levelno == logging.ERROR]
    assert any("test-err: code=IDN500" in r.message for r in records)
