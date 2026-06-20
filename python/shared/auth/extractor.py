"""BearerAuthExtractor — validates JWT bearer tokens in gRPC handlers (A.4).

Handlers receive a BearerAuthExtractor instance and call it on each request
to get (user_id, is_system).  This lives in shared so any service handler
can use it without importing identity infrastructure.
"""
from __future__ import annotations

import grpc
import grpc.aio
from jose import JWTError, jwt


class AuthenticationError(Exception):
    """Raised when the bearer token is absent or invalid."""


_ALGORITHM = "HS256"
_AUTH_KEY = "authorization"


def _bearer_from_metadata(context: grpc.aio.ServicerContext) -> str:  # type: ignore[type-arg]
    raw = context.invocation_metadata() or []
    metadata: dict[str, str] = {str(k): str(v) for k, v in raw}
    bearer = metadata.get(_AUTH_KEY, "")
    if not bearer.lower().startswith("bearer "):
        return ""
    return bearer[7:]


class BearerAuthExtractor:
    """Callable that returns (user_id, is_system) from a gRPC request context.

    Wire.py provides one instance per service; handlers receive it in __init__.
    This mirrors the Go AuthExtractor func pattern (repository/migration handlers).
    """

    def __init__(self, jwt_secret: str) -> None:
        self._secret = jwt_secret

    def __call__(
        self, context: grpc.aio.ServicerContext  # type: ignore[type-arg]
    ) -> tuple[int, bool]:
        """Return (user_id, is_system). Raise AuthenticationError on failure."""
        token = _bearer_from_metadata(context)
        if not token:
            raise AuthenticationError("missing bearer token")
        try:
            payload = jwt.decode(token, self._secret, algorithms=[_ALGORITHM])
            user_id = int(payload["sub"])
            is_system = bool(payload.get("sys", False))
            return user_id, is_system
        except (JWTError, KeyError, ValueError) as exc:
            raise AuthenticationError("invalid token") from exc
