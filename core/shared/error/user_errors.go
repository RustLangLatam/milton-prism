package coreerror

// USER8XX - Session errors
var (
	UserErrorInvalidSession = NewUnauthenticatedError("USER818", "Failure_Invalid_Session")
)
