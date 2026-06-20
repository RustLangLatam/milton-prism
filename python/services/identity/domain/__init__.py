"""Identity service domain layer.

Domain types are thin aliases of the generated proto messages (A.3).
No parallel models — the proto is the single source of truth (Canon §3).
"""
from __future__ import annotations

from .domain import AuthorizationTokens, Pagination, Token, User, UsersFilter, UserState
from .errors import (
    ERR_ACCOUNT_SUSPENDED,
    ERR_EMAIL_ALREADY_EXISTS,
    ERR_INTERNAL,
    ERR_INVALID_CREDENTIALS,
    ERR_INVALID_SESSION,
    ERR_INVALID_TOKEN,
    ERR_MISSING_EMAIL,
    ERR_MISSING_IDENTIFIER,
    ERR_MISSING_PASSWORD,
    ERR_MISSING_PAYLOAD,
    ERR_TOKEN_GENERATION,
    ERR_TOKEN_REFRESH,
    ERR_USER_NOT_ACTIVE,
    ERR_USER_NOT_FOUND,
)

__all__ = [
    "ERR_ACCOUNT_SUSPENDED",
    "ERR_EMAIL_ALREADY_EXISTS",
    "ERR_INTERNAL",
    "ERR_INVALID_CREDENTIALS",
    "ERR_INVALID_SESSION",
    "ERR_INVALID_TOKEN",
    "ERR_MISSING_EMAIL",
    "ERR_MISSING_IDENTIFIER",
    "ERR_MISSING_PASSWORD",
    "ERR_MISSING_PAYLOAD",
    "ERR_TOKEN_GENERATION",
    "ERR_TOKEN_REFRESH",
    "ERR_USER_NOT_ACTIVE",
    "ERR_USER_NOT_FOUND",
    "AuthorizationTokens",
    "Pagination",
    "Token",
    "User",
    "UserState",
    "UsersFilter",
]
