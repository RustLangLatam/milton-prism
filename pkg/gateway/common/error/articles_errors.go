package message_error

var articlesErrorMessages = map[string]string{
	// Validation errors (ART1xx)
	"ART101": "An article identifier is required.",
	"ART102": "The request payload is missing required fields.",
	"ART103": "An author identifier is required.",

	// Domain errors (ART2xx)
	"ART201": "The requested article was not found.",
	"ART202": "An article with this slug already exists for this author.",
	"ART203": "The requested tag was not found.",
	"ART204": "The specified author profile was not found.",
	"ART205": "Access to this article is not permitted.",

	// Internal errors (ART5xx)
	"ART500": "An internal error occurred. Please try again later.",
}
