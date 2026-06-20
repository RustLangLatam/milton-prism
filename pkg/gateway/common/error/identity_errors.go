package message_error

var identityErrorMessages = map[string]string{
	// Validation errors (IDN1xx)
	"IDN101": "A user identifier is required.",
	"IDN102": "The request payload is missing required fields.",
	"IDN103": "The provided email address is invalid.",
	"IDN104": "The provided password does not meet the requirements.",
	"IDN105": "An email address is required.",
	"IDN106": "A password is required.",

	// Domain errors (IDN2xx)
	"IDN201": "The requested user was not found.",
	"IDN202": "An account with this email address already exists.",
	"IDN203": "The provided credentials are incorrect.",
	"IDN204": "This account is not active.",
	"IDN205": "This account has been suspended. Please contact support.",
	"IDN206": "The provided token is invalid or has expired.",
	"IDN207": "The session is invalid or has expired. Please authenticate again.",

	// Internal errors (IDN5xx)
	"IDN500": "An internal error occurred. Please try again later.",
	"IDN501": "An error occurred while generating the authentication token.",
	"IDN502": "An error occurred while refreshing the token. Please authenticate again.",
}
