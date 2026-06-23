package message_error

var analysisErrorMessages = map[string]string{
	// Validation errors (ANL1xx)
	"ANL101": "An analysis summary identifier is required.",
	"ANL102": "A repository ID is required to run an analysis.",
	"ANL103": "The root subdirectory is invalid. Use a path inside the repository (no absolute paths or \"..\").",
	"ANL104": "The selected root is not valid for this analysis.",
	"ANL105": "A source branch is required to run an analysis.",

	// Domain errors (ANL2xx)
	"ANL201": "The requested analysis summary was not found.",
	"ANL202": "The specified source repository was not found.",
	"ANL206": "An analysis already exists for this repository and branch.",
	"ANL207": "This analysis can't be deleted while a migration is still using it. Cancel or finish the migration first.",
	"ANL208": "This action isn't allowed in the analysis's current state.",

	// Quota / plan errors (ANL3xx)
	"ANL301": "You've reached your plan's monthly analysis limit. Upgrade your plan to run more analyses.",

	// Internal errors (ANL5xx)
	"ANL500": "An internal error occurred. Please try again later.",
}
