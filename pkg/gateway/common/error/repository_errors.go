package message_error

var repositoryErrorMessages = map[string]string{
	// Validation errors (REPO1xx)
	"REPO101": "A repository identifier is required.",
	"REPO102": "The request payload is missing required fields.",
	"REPO103": "An owner user ID is required.",
	"REPO104": "The provided remote URL is invalid.",

	// Domain errors (REPO2xx)
	"REPO201": "The requested repository was not found.",
	"REPO202": "A repository with this remote URL already exists.",
	"REPO203": "The specified owner user was not found.",
	"REPO204": "The connection to the git remote failed.",
	"REPO205": "Access to this repository is not permitted.",

	// Internal errors (REPO5xx)
	"REPO500": "An internal error occurred. Please try again later.",
}
