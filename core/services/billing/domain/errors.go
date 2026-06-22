package domain

import "fmt"

// Error represents a domain-level error with a unique code and a stable,
// machine-resolvable message key.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

const (
	// Validation errors (BIL1xx)
	ErrCodeMissingPayload    = "BIL101"
	ErrCodeMissingUserID     = "BIL102"
	ErrCodeMissingIdentifier = "BIL103"

	// Domain errors (BIL2xx)
	ErrCodePlanNotFound = "BIL201"

	// Internal errors (BIL5xx)
	ErrCodeInternal = "BIL500"
)

var (
	ErrMissingPayload    = &Error{Code: ErrCodeMissingPayload, Message: "Failure_Missing_Payload"}
	ErrMissingUserID     = &Error{Code: ErrCodeMissingUserID, Message: "Failure_Missing_User_Id"}
	ErrMissingIdentifier = &Error{Code: ErrCodeMissingIdentifier, Message: "Failure_Missing_Identifier"}
	ErrPlanNotFound      = &Error{Code: ErrCodePlanNotFound, Message: "Failure_Plan_Not_Found"}
	ErrInternal          = &Error{Code: ErrCodeInternal, Message: "Failure_Internal"}
)
