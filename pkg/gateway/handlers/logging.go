package handlers

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	"milton_prism/pkg/log"
)

// responseWriter is a wrapper for http.ResponseWriter to capture the status code for logging.
type responseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// wrapResponseWriter creates a new responseWriter to track the status code.
func wrapResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{ResponseWriter: w}
}

// Status returns the HTTP status code. Defaults to 200 OK if no status has been set.
func (rw *responseWriter) Status() int {
	if !rw.wroteHeader {
		return http.StatusOK
	}
	return rw.status
}

// WriteHeader captures the HTTP status code before writing the header.
func (rw *responseWriter) WriteHeader(code int) {
	if !rw.wroteHeader {
		rw.status = code
		rw.ResponseWriter.WriteHeader(code)
		rw.wroteHeader = true
	}
}

// HandlerLoggingMiddleware logs the incoming HTTP request, its duration, and the response details.
func HandlerLoggingMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Start time to calculate the duration later
			start := time.Now()

			// Log the incoming request details
			logRequest(r)

			// Prepare to capture the status code via a wrapped response writer
			wrapped := wrapResponseWriter(w)

			// Execute the next handler in the chain
			next.ServeHTTP(wrapped, r)

			// Calculate the duration
			duration := time.Since(start)

			// Determine the log level based on the status code.
			statusCode := wrapped.Status()
			durationStr := duration.Round(time.Microsecond).String()
			statusText := http.StatusText(statusCode)

			clientIP := getClientIP(r)

			logMessage := fmt.Sprintf(
				"[src_ip=%s] Response handled: method=%s path=%s status=%d (%s) duration=%s",
				clientIP, r.Method, r.URL.Path, statusCode, statusText, durationStr,
			)

			// Log based on status code.
			switch {
			case statusCode >= 500:
				log.Error(logMessage)
			case statusCode >= 400:
				log.Warning(logMessage)
			default:
				log.Info(logMessage)
			}
		})
	}
}

// getClientIP retrieves the client's IP address, considering the X-Forwarded-For header.
func getClientIP(r *http.Request) string {
	// If the X-Forwarded-For header exists, use the first IP in the list.
	if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
		ips := strings.Split(forwarded, ",")
		return strings.TrimSpace(ips[0])
	}
	// Otherwise, use the remote IP directly.
	return strings.Split(r.RemoteAddr, ":")[0]
}

// logRequest logs the details of the incoming request, including client IP and User-Agent.
func logRequest(r *http.Request) {
	clientIP := getClientIP(r)
	userAgent := r.UserAgent()

	if r.URL.RawQuery != "" {
		log.Infof(
			"[src_ip=%s] Incoming request: method=%s path=%s query=%s [user_agent=%s]",
			clientIP, r.Method, r.URL.EscapedPath(), r.URL.RawQuery, userAgent,
		)
	} else {
		log.Infof(
			"[src_ip=%s] Incoming request: method=%s path=%s [user_agent=%s]",
			clientIP, r.Method, r.URL.EscapedPath(), userAgent,
		)
	}
}
