"""Identity service error sentinels (A.4).

Error codes from the registry in the decomposition doc:
  IDN1xx — validation errors
  IDN2xx — domain errors
  IDN5xx — internal errors

Application raises these; handlers call map_error() to convert to gRPC status.
"""
from __future__ import annotations

from shared.errors import DomainError

# Validation errors (IDN1xx)
ERR_MISSING_IDENTIFIER = DomainError("IDN101", "Failure_Missing_Identifier")
ERR_MISSING_PAYLOAD    = DomainError("IDN102", "Failure_Missing_Payload")
ERR_INVALID_EMAIL      = DomainError("IDN103", "Failure_Invalid_Email")
ERR_INVALID_PASSWORD   = DomainError("IDN104", "Failure_Invalid_Password")
ERR_MISSING_EMAIL      = DomainError("IDN105", "Failure_Missing_Email")
ERR_MISSING_PASSWORD   = DomainError("IDN106", "Failure_Missing_Password")

# Domain errors (IDN2xx)
ERR_USER_NOT_FOUND       = DomainError("IDN201", "Failure_User_Not_Found")
ERR_EMAIL_ALREADY_EXISTS = DomainError("IDN202", "Failure_Email_Already_Exists")
ERR_INVALID_CREDENTIALS  = DomainError("IDN203", "Failure_Invalid_Credentials")
ERR_USER_NOT_ACTIVE      = DomainError("IDN204", "Failure_User_Not_Active")
ERR_ACCOUNT_SUSPENDED    = DomainError("IDN205", "Failure_Account_Suspended")
ERR_INVALID_TOKEN        = DomainError("IDN206", "Failure_Invalid_Token")
ERR_INVALID_SESSION      = DomainError("IDN207", "Failure_Invalid_Session")

# Internal errors (IDN5xx)
ERR_INTERNAL        = DomainError("IDN500", "Failure_Internal")
ERR_TOKEN_GENERATION = DomainError("IDN501", "Failure_Token_Generation")
ERR_TOKEN_REFRESH   = DomainError("IDN502", "Failure_Token_Refresh")
