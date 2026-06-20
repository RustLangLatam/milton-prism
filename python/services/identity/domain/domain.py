"""Domain type aliases — proto messages are the single source of truth (A.3, Canon §3).

Import from here, never directly from gen/. This makes the boundary explicit and
lets us update proto paths in one place if the generated layout changes.
"""
from __future__ import annotations

from milton_prism.types.identity.v1.user_pb2 import User, UsersFilter, UserState
from milton_prism.types.pagination.v1.pagination_pb2 import Pagination
from milton_prism.types.token.v1.token_pb2 import AuthorizationTokens, Token

__all__ = [
    "AuthorizationTokens",
    "Pagination",
    "Token",
    "User",
    "UserState",
    "UsersFilter",
]
