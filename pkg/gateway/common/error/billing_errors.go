package message_error

var billingErrorMessages = map[string]string{
	// Validation errors (BIL1xx)
	"BIL101": "The request payload is missing required fields.",
	"BIL102": "A user identifier is required.",
	"BIL103": "An identifier is required.",

	// Domain errors (BIL2xx)
	"BIL201": "The requested plan was not found.",

	// Internal errors (BIL5xx)
	"BIL500": "An internal billing error occurred. Please try again later.",
}
