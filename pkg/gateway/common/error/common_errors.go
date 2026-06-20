package message_error

var dbErrorMessages = map[string]string{
	"DB001": "A database error occurred. Please try again later.",
	"DB002": "A database transaction error occurred. Please try again later.",
}

var commonErrorMessages = map[string]string{
	"COMMON001": "One or more request parameters are invalid.",
	"GEN001":    "The request is malformed or missing required information.",

	// Filtering-related errors
	"FILTER001": "Both follower and following filters require a domain identifier.",
	"FILTER002": "The suggest filter cannot be used together with follower or following filters.",
	"FILTER003": "Follower and following filters are incompatible with the specified role.",
	"FILTER004": "The suggest filter is incompatible with the specified role.",
	"FILTER005": "Follower and following filters cannot be used at the same time.",
	"FILTER006": "The mandatory follow filter cannot be used with follower or following filters.",
	"FILTER007": "The audience identifier filter cannot be used with follower or following filters.",

	// Category-related errors
	"CAT001": "An error occurred while creating the category.",
	"CAT002": "The requested category could not be found.",
}
