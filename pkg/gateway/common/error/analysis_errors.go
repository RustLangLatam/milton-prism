package message_error

var analysisErrorMessages = map[string]string{
	// Validation errors (ANL1xx)
	"ANL101": "An analysis summary identifier is required.",
	"ANL102": "A repository ID is required to run an analysis.",

	// Domain errors (ANL2xx)
	"ANL201": "The requested analysis summary was not found.",
	"ANL202": "The specified source repository was not found.",

	// Internal errors (ANL5xx)
	"ANL500": "An internal error occurred. Please try again later.",
}
