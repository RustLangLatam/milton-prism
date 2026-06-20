package coreerror

// Token validation errors (AUTH2XX)
var (
	TokenValidationErrorInvalid = NewUnauthenticatedError("AUTH202", "Failure_Token_Invalid")
)
