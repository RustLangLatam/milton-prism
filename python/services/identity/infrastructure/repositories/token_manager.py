"""JWT token manager adapter using python-jose (A.1).

Tokens carry:
  sub  — user_id (str)
  sid  — session_id
  sys  — is_system_user (bool)
  typ  — "access" | "refresh"
"""
from __future__ import annotations

import time
from datetime import datetime, timezone

from google.protobuf.timestamp_pb2 import Timestamp
from jose import jwt

from services.identity.domain.domain import AuthorizationTokens, Token

_ACCESS_TTL_SECONDS = 3600       # 1 hour
_REFRESH_TTL_SECONDS = 86400 * 7  # 7 days
_ALGORITHM = "HS256"


def _ts_from_unix(unix: int) -> Timestamp:
    ts = Timestamp()
    ts.FromDatetime(datetime.fromtimestamp(unix, tz=timezone.utc))
    return ts


class JwtTokenManager:
    """Issues and validates HS256 JWTs. Secret must be at least 32 chars."""

    def __init__(self, secret: str) -> None:
        self._secret = secret

    def new_tokens(
        self,
        user_id: int,
        is_system: bool,
        session_id: str,
    ) -> AuthorizationTokens:
        now = int(time.time())
        access_exp = now + _ACCESS_TTL_SECONDS
        refresh_exp = now + _REFRESH_TTL_SECONDS

        access_payload = {
            "sub": str(user_id),
            "sid": session_id,
            "sys": is_system,
            "typ": "access",
            "iat": now,
            "exp": access_exp,
        }
        refresh_payload = {
            "sub": str(user_id),
            "sid": session_id,
            "sys": is_system,
            "typ": "refresh",
            "iat": now,
            "exp": refresh_exp,
        }

        access_value = jwt.encode(access_payload, self._secret, algorithm=_ALGORITHM)
        refresh_value = jwt.encode(refresh_payload, self._secret, algorithm=_ALGORITHM)

        access_token = Token(value=access_value, expire_time=_ts_from_unix(access_exp))
        refresh_token = Token(value=refresh_value, expire_time=_ts_from_unix(refresh_exp))

        return AuthorizationTokens(
            access_token=access_token,
            refresh_token=refresh_token,
            expires_in=_ACCESS_TTL_SECONDS,
        )

    def extract_session_id(self, token: str) -> str:
        payload = jwt.decode(token, self._secret, algorithms=[_ALGORITHM])
        sid: str = payload["sid"]
        return sid

    def verify_access(self, token: str) -> tuple[int, str]:
        payload = jwt.decode(token, self._secret, algorithms=[_ALGORITHM])
        if payload.get("typ") != "access":
            raise ValueError("not an access token")
        return int(payload["sub"]), payload["sid"]
