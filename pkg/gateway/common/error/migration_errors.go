package message_error

var migrationErrorMessages = map[string]string{
	// Validation errors (MIG1xx)
	"MIG101": "A migration identifier is required.",
	"MIG102": "The request payload is missing required fields.",
	"MIG103": "An owner user ID is required.",
	"MIG104": "A repository ID is required.",
	"MIG105": "The target configuration is invalid. Language and database must be specified.",
	"MIG106": "The root subdirectory is invalid. Use a path inside the repository (no absolute paths or \"..\").",
	"MIG107": "The selected target language has no code generator yet. Choose Go or Python.",

	// Domain errors (MIG2xx)
	"MIG201": "The requested migration was not found.",
	"MIG202": "The requested state transition is not allowed for the migration's current state.",
	"MIG203": "The specified source repository was not found.",
	"MIG204": "The specified owner user was not found.",
	"MIG205": "Access to this migration is not permitted.",
	"MIG222": "You've reached your plan's monthly migration limit. Upgrade your plan to start more migrations.",

	// Internal errors (MIG5xx)
	"MIG500": "An internal error occurred. Please try again later.",
}
