package domain

import "fmt"

// Error represents a domain-level error with a unique code and a human-readable message.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

const (
	// Validation errors (IDN1xx)
	ErrCodeMissingIdentifier = "IDN101"
	ErrCodeMissingPayload    = "IDN102"
	ErrCodeInvalidEmail      = "IDN103"
	ErrCodeInvalidPassword   = "IDN104"
	ErrCodeMissingEmail      = "IDN105"
	ErrCodeMissingPassword   = "IDN106"

	// Domain errors (IDN2xx)
	ErrCodeUserNotFound       = "IDN201"
	ErrCodeEmailAlreadyExists = "IDN202"
	ErrCodeInvalidCredentials = "IDN203"
	ErrCodeUserNotActive      = "IDN204"
	ErrCodeAccountSuspended   = "IDN205"
	ErrCodeInvalidToken       = "IDN206"
	ErrCodeInvalidSession     = "IDN207"

	// Internal errors (IDN5xx)
	ErrCodeInternal         = "IDN500"
	ErrCodeTokenGeneration  = "IDN501"
	ErrCodeTokenRefresh     = "IDN502"
)

var (
	ErrMissingIdentifier  = &Error{Code: ErrCodeMissingIdentifier, Message: "Failure_Missing_Identifier"}
	ErrMissingPayload     = &Error{Code: ErrCodeMissingPayload, Message: "Failure_Missing_Payload"}
	ErrInvalidEmail       = &Error{Code: ErrCodeInvalidEmail, Message: "Failure_Invalid_Email"}
	ErrInvalidPassword    = &Error{Code: ErrCodeInvalidPassword, Message: "Failure_Invalid_Password"}
	ErrMissingEmail       = &Error{Code: ErrCodeMissingEmail, Message: "Failure_Missing_Email"}
	ErrMissingPassword    = &Error{Code: ErrCodeMissingPassword, Message: "Failure_Missing_Password"}
	ErrUserNotFound       = &Error{Code: ErrCodeUserNotFound, Message: "Failure_User_Not_Found"}
	ErrEmailAlreadyExists = &Error{Code: ErrCodeEmailAlreadyExists, Message: "Failure_Email_Already_Exists"}
	ErrInvalidCredentials = &Error{Code: ErrCodeInvalidCredentials, Message: "Failure_Invalid_Credentials"}
	ErrUserNotActive      = &Error{Code: ErrCodeUserNotActive, Message: "Failure_User_Not_Active"}
	ErrAccountSuspended   = &Error{Code: ErrCodeAccountSuspended, Message: "Failure_Account_Suspended"}
	ErrInvalidToken       = &Error{Code: ErrCodeInvalidToken, Message: "Failure_Invalid_Token"}
	ErrInvalidSession     = &Error{Code: ErrCodeInvalidSession, Message: "Failure_Invalid_Session"}
	ErrInternal           = &Error{Code: ErrCodeInternal, Message: "Failure_Internal"}
	ErrTokenGeneration    = &Error{Code: ErrCodeTokenGeneration, Message: "Failure_Token_Generation"}
	ErrTokenRefresh       = &Error{Code: ErrCodeTokenRefresh, Message: "Failure_Token_Refresh"}
)
