"""Tests for DomainError and map_error (A.4, A.8)."""
from __future__ import annotations

import grpc

from shared.errors import DomainError
from shared.errors.mapper import map_error


def test_domain_error_str_contains_code_and_message() -> None:
    err = DomainError("IDN201", "Failure_User_Not_Found")
    assert "IDN201" in str(err)
    assert "Failure_User_Not_Found" in str(err)


def test_domain_error_attributes() -> None:
    err = DomainError("IDN102", "Failure_Missing_Payload")
    assert err.code == "IDN102"
    assert err.message == "Failure_Missing_Payload"


def test_map_error_validation_returns_invalid_argument() -> None:
    err = DomainError("IDN101", "Failure_Missing_Identifier")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.INVALID_ARGUMENT


def test_map_error_not_found_returns_not_found() -> None:
    err = DomainError("IDN201", "Failure_User_Not_Found")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.NOT_FOUND


def test_map_error_email_exists_returns_already_exists() -> None:
    err = DomainError("IDN202", "Failure_Email_Already_Exists")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.ALREADY_EXISTS


def test_map_error_invalid_credentials_returns_unauthenticated() -> None:
    err = DomainError("IDN203", "Failure_Invalid_Credentials")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.UNAUTHENTICATED


def test_map_error_suspended_returns_permission_denied() -> None:
    err = DomainError("IDN205", "Failure_Account_Suspended")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.PERMISSION_DENIED


def test_map_error_internal_returns_internal() -> None:
    err = DomainError("IDN500", "Failure_Internal")
    code, _ = map_error(err)
    assert code == grpc.StatusCode.INTERNAL


def test_map_error_non_domain_returns_internal() -> None:
    code, detail = map_error(ValueError("unexpected"))
    assert code == grpc.StatusCode.INTERNAL
    assert detail == "Failure_Internal"


def test_map_error_returns_message_as_detail() -> None:
    err = DomainError("IDN201", "Failure_User_Not_Found")
    _, detail = map_error(err)
    assert detail == "Failure_User_Not_Found"
