"""Motor client builder with lifecycle management (A.1, A.2)."""
from __future__ import annotations

from .client import MongoClientBuilder, get_database
from .transaction_manager import MotorTransactionManager

__all__ = ["MongoClientBuilder", "MotorTransactionManager", "get_database"]
