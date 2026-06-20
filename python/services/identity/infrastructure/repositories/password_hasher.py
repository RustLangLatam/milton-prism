"""bcrypt password hasher adapter (A.1)."""
from __future__ import annotations

import bcrypt


class BcryptPasswordHasher:
    """PasswordHasher implementation using bcrypt (rounds=12 by default)."""

    def __init__(self, rounds: int = 12) -> None:
        self._rounds = rounds

    def hash(self, plain: str) -> str:
        salt = bcrypt.gensalt(rounds=self._rounds)
        return bcrypt.hashpw(plain.encode(), salt).decode()

    def verify(self, hashed: str, plain: str) -> bool:
        try:
            return bcrypt.checkpw(plain.encode(), hashed.encode())
        except Exception:
            return False
