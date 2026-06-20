"""Migration domain error sentinels (A.3).

Codes follow the MIG registry in the platform decomposition doc.
"""
from __future__ import annotations

from shared.errors import DomainError

# ── Validation (MIG1xx) ───────────────────────────────────────────────────────

ERR_MISSING_IDENTIFIER = DomainError("MIG101", "Failure_Missing_Identifier")
ERR_MISSING_PAYLOAD = DomainError("MIG102", "Failure_Missing_Payload")
ERR_MISSING_OWNER_USER_ID = DomainError("MIG103", "Failure_Missing_Owner_User_ID")
ERR_MISSING_REPOSITORY_ID = DomainError("MIG104", "Failure_Missing_Repository_ID")
ERR_INVALID_TARGET_CONFIG = DomainError("MIG105", "Failure_Invalid_Target_Config")

# ── Domain (MIG2xx) ───────────────────────────────────────────────────────────

ERR_MIGRATION_NOT_FOUND = DomainError("MIG201", "Failure_Migration_Not_Found")
ERR_INVALID_STATE_TRANSITION = DomainError("MIG202", "Failure_Invalid_State_Transition")
ERR_REPOSITORY_NOT_FOUND = DomainError("MIG203", "Failure_Repository_Not_Found")
ERR_OWNER_NOT_FOUND = DomainError("MIG204", "Failure_Owner_Not_Found")
ERR_FORBIDDEN_ACCESS = DomainError("MIG205", "Failure_Access_Forbidden")

# ── Internal (MIG5xx) ─────────────────────────────────────────────────────────

ERR_INTERNAL = DomainError("MIG500", "Failure_Internal")
