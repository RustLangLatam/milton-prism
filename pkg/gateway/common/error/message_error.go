package message_error

import (
	"encoding/json"
	"strings"
	"unicode"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"
	"google.golang.org/grpc/status"
)

// ErrorMessage represents an error message with details, status code, and title.
type ErrorMessage struct {
	Detail string `json:"detail,omitempty"` // Human-readable error message
	Status int    `json:"status,omitempty"` // HTTP status code
	Title  string `json:"title,omitempty"`  // Short, human-readable error title
}

// Error returns the JSON representation of the ErrorMessage.
func (e *ErrorMessage) Error() string {
	errStr, _ := json.Marshal(e)
	return string(errStr)
}

// HandlerErrorMessage converts a gRPC status to an ErrorMessage.
func HandlerErrorMessage(st status.Status) ErrorMessage {
	statusCode := runtime.HTTPStatusFromCode(st.Code())
	detail := st.Message()

	// Split the detail to extract the error code and the associated message
	parts := strings.SplitN(detail, ": ", 2)
	code := parts[0]  // Assume the code is the first part before ": "
	message := detail // Default to using the whole detail as the message
	if len(parts) > 1 {
		message = formatErrorMessage(parts[1])
	}

	// Try to get a custom error message based on the extracted error code
	if customMsg, ok := lookupErrorMessage(code); ok {
		message = customMsg
	}

	return ErrorMessage{
		Detail: message,
		Status: statusCode,
		Title:  st.Code().String(),
	}
}

// formatErrorMessage converts Failure_X_Y style messages into readable form.
//
// Examples:
//
//	formatErrorMessage("Failure_Missing_Identifier") → "Failure missing identifier."
//	formatErrorMessage("Failure_Company_Not_Found")  → "Failure company not found."
func formatErrorMessage(msg string) string {
	parts := strings.Split(msg, "_")
	for i, part := range parts {
		if containsInternalUppercase(part) {
			parts[i] = part
		} else {
			parts[i] = strings.ToLower(part)
		}
	}
	if len(parts) > 0 {
		parts[0] = cases.Title(language.Und).String(parts[0])
	}
	return strings.Join(parts, " ") + "."
}

// containsInternalUppercase checks if a word contains uppercase letters other than the first letter.
func containsInternalUppercase(word string) bool {
	for i, r := range word {
		if i > 0 && unicode.IsUpper(r) {
			return true
		}
	}
	return false
}

// lookupErrorMessage searches for an API-friendly message by error code across all service maps.
func lookupErrorMessage(code string) (string, bool) {
	maps := []map[string]string{
		authErrorMessages,
		dbErrorMessages,
		commonErrorMessages,
		identityErrorMessages,
		repositoryErrorMessages,
		migrationErrorMessages,
		analysisErrorMessages,
		articlesErrorMessages,
	}
	for _, m := range maps {
		if msg, ok := m[code]; ok {
			return msg, true
		}
	}
	return "", false
}
