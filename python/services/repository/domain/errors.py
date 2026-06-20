"""Repository service error sentinels (A.4).

Codes match the REPO registry in the decomposition doc and the Go implementation.
  REPO1xx — validation errors
  REPO2xx — domain errors
  REPO5xx — internal errors
"""
from __future__ import annotations

from shared.errors import DomainError

# Validation errors (REPO1xx)
ERR_MISSING_IDENTIFIER   = DomainError("REPO101", "Failure_Missing_Identifier")
ERR_MISSING_PAYLOAD      = DomainError("REPO102", "Failure_Missing_Payload")
ERR_MISSING_OWNER_USER_ID = DomainError("REPO103", "Failure_Missing_Owner_User_ID")
ERR_INVALID_REMOTE_URL   = DomainError("REPO104", "Failure_Invalid_Remote_URL")

# Domain errors (REPO2xx)
ERR_REPOSITORY_NOT_FOUND      = DomainError("REPO201", "Failure_Repository_Not_Found")
ERR_REPOSITORY_ALREADY_EXISTS = DomainError("REPO202", "Failure_Repository_Already_Exists")
ERR_OWNER_NOT_FOUND           = DomainError("REPO203", "Failure_Owner_Not_Found")
ERR_CONNECTION_FAILED         = DomainError("REPO204", "Failure_Connection_Failed")
ERR_FORBIDDEN_ACCESS          = DomainError("REPO205", "Failure_Access_Forbidden")

# Internal errors (REPO5xx)
ERR_INTERNAL = DomainError("REPO500", "Failure_Internal")
