package message_error

var authErrorMessages = map[string]string{
	// Authentication errors (AUTH1XX)
	"AUTH101": "The provided credentials are incorrect.",
	"AUTH102": "This account has been disabled. Please contact support for assistance.",
	"AUTH103": "This account has been locked due to multiple failed login attempts.",
	"AUTH104": "Insufficient permissions to perform this operation.",

	// Token validation errors (AUTH2XX)
	"AUTH201": "The session has expired. Please authenticate again.",
	"AUTH203": "The session is invalid or inactive. Please re-authenticate.",
	"AUTH206": "Required authentication details are missing.",
	"AUTH207": "The authentication service is temporarily unavailable. Please try again later.",

	// Token refresh errors (AUTH3XX)
	"AUTH301": "The session token is invalid. Please obtain a new one.",
	"AUTH302": "The session token has expired. Please re-authenticate.",
	"AUTH303": "The session token has been revoked. Please request a new one.",

	// Token creation errors (AUTH4XX)
	"AUTH401": "An error occurred while generating the authentication token. Please try again later.",

	// Authorization errors (AUTH5XX)
	"AUTH501": "Access denied. You do not have the required permissions to perform this action.",
	"AUTH503": "The provided administrative credentials are not valid.",
}
