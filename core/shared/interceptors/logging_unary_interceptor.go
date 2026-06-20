package interceptors

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"milton_prism/pkg/log"
	"net"
	"reflect"
	"runtime/debug"
	"strings"
	"time"

	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

// sensitiveFieldNames are substrings that identify fields whose values must not be logged.
var sensitiveFieldNames = []string{"password", "token", "secret", "otp", "hash"}

// isSensitiveField returns true when the field name contains any sensitive keyword.
func isSensitiveField(name string) bool {
	lower := strings.ToLower(name)
	for _, kw := range sensitiveFieldNames {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// sanitizeRequest processes a request using reflection and replaces any []byte fields with a message indicating their size in bytes.
// sanitizeRequest processes a request using reflection and returns a string with key=value pairs.
func sanitizeRequest(request interface{}) string {
	value := reflect.ValueOf(request)
	sanitizedValue := sanitizeValue(value)

	if sanitizedMap, ok := sanitizedValue.(map[string]interface{}); ok {
		return formatKeyValuePairs(sanitizedMap)
	}

	return fmt.Sprintf("%v", sanitizedValue)
}

// formatKeyValuePairs converts a map to a key=value string format.
func formatKeyValuePairs(data map[string]interface{}) string {
	var pairs []string
	for key, value := range data {
		pairs = append(pairs, fmt.Sprintf("%s=%v", key, value))
	}
	return strings.Join(pairs, ", ")
}

// sanitizeValue recursively sanitizes a reflected value, replacing []byte fields and Base64 strings with their size.
func sanitizeValue(value reflect.Value) interface{} {
	if !value.IsValid() {
		return nil
	}

	switch value.Kind() {
	case reflect.Ptr:
		if value.IsNil() {
			return nil
		}
		return sanitizeValue(value.Elem())

	case reflect.Struct:
		sanitizedFields := make(map[string]interface{})
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			if field.PkgPath != "" {
				continue
			}
			if isSensitiveField(field.Name) {
				sanitizedFields[field.Name] = "<redacted>"
				continue
			}
			fieldValue := sanitizeValue(value.Field(i))
			if fieldValue != nil && fieldValue != "" {
				sanitizedFields[field.Name] = fieldValue
			}
		}
		return sanitizedFields

	case reflect.Slice:
		if value.IsNil() || value.Len() == 0 {
			return nil
		}
		if value.Type().Elem().Kind() == reflect.Uint8 {
			return fmt.Sprintf("<byte-size: %d bytes>", value.Len())
		}
		sanitizedElements := make([]interface{}, value.Len())
		for i := 0; i < value.Len(); i++ {
			sanitizedElements[i] = sanitizeValue(value.Index(i))
		}
		return sanitizedElements

	case reflect.Map:
		if value.IsNil() || value.Len() == 0 {
			return nil
		}
		sanitizedMap := make(map[interface{}]interface{})
		for _, key := range value.MapKeys() {
			sanitizedValue := sanitizeValue(value.MapIndex(key))
			if sanitizedValue != nil {
				sanitizedMap[key.Interface()] = sanitizedValue
			}
		}
		return sanitizedMap

	case reflect.Interface:
		if value.IsNil() {
			return nil
		}
		return sanitizeValue(value.Elem())

	case reflect.String:
		str := value.String()
		if len(str) == 0 {
			return nil
		}

		if decoded, err := base64.StdEncoding.DecodeString(str); err == nil {
			return fmt.Sprintf("<base64-size: %d bytes>", len(decoded))
		}
		return str

	default:
		return value.Interface()
	}
}

// getClientIP retrieves the original client IP from headers or falls back to peer info.
func getClientIP(ctx context.Context) string {
	// Check if metadata is available in the context.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// Check for the X-Forwarded-For header.
		if forwardedFor := md.Get("x-forwarded-for"); len(forwardedFor) > 0 {
			// The first IP in the X-Forwarded-For list is the original client IP.
			ips := strings.Split(forwardedFor[0], ",")
			return strings.TrimSpace(ips[0])
		}
		// Check for the X-Real-IP header.
		if realIP := md.Get("x-real-ip"); len(realIP) > 0 {
			return strings.TrimSpace(realIP[0])
		}
	}

	// Fallback to the peer's address if no headers are found.
	if p, ok := peer.FromContext(ctx); ok {
		host, _, err := net.SplitHostPort(p.Addr.String())
		if err == nil {
			return host
		}
	}
	return "unknown"
}

func IsForwarded(ctx context.Context) bool {
	// Check if metadata is available in the context.
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		// Check for the X-Forwarded-For header.
		if forwardedFor := md.Get("x-forwarded"); len(forwardedFor) > 0 {
			return forwardedFor[0] == "true"
		} else {
			return false
		}
	}
	return false
}

// shortenMethodName extracts and shortens the gRPC full method path by removing the package name.
//
// This function takes a gRPC method path that typically includes the package name, service name,
// and method name, and returns only the service and method name.
//
// For example:
//
//	Input:  "/celebrities.protobuf.services.posts.CelebritiesPostsService/GetFetchSettingsData"
//	Output: "/CelebritiesPostsService/GetFetchSettingsData"
//
// Parameters:
// - fullMethod: The full gRPC method path (e.g., "/package.Service/Method").
//
// Returns:
// - A shortened method path with only the service and method name (e.g., "/Service/Method").
func shortenMethodName(fullMethod string) string {
	// Split the full method path into parts.
	parts := strings.Split(fullMethod, "/")
	// Check if the full method path has at least two parts.
	if len(parts) < 2 {
		// If the full method path has less than two parts, return the full method path.
		return fullMethod
	}
	// Split the second part into sub-parts.
	subParts := strings.Split(parts[1], ".")
	// Return the last two parts of the full method path.
	return fmt.Sprintf("/%s/%s", subParts[len(subParts)-1], parts[2])
}

// LogUnaryInterceptor is a gRPC interceptor that logs information about incoming gRPC calls.
//
// This interceptor performs the following tasks:
// - Extracts the request Identifier from the context (or assigns a default value if missing).
// - Logs the start of a new gRPC call with the method name and request details.
// - Tracks and logs the time taken for the call's execution (referred to as executionTime).
// - Differentiates between successful and failed calls, logging appropriate messages for each.
//
// Parameters:
// - ctx: The context associated with the incoming gRPC call.
// - req: The gRPC request message.
// - info: ExtendedAttributes about the gRPC call (e.g., method name).
// - handler: The handler that processes the gRPC request.
//
// Returns:
// - The response from the handler and any error that occurred during the call.
func LogUnaryInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	if strings.HasSuffix(info.FullMethod, "Health/Check") {
		return handler(ctx, req)
	}

	clientIP := getClientIP(ctx)
	// Shorten the method name.
	shortMethod := shortenMethodName(info.FullMethod)

	// Log the start of the gRPC call.
	log.Infof("[src_ip=%s] [is_forwarded=%t] [gRPC new call] method=%s", clientIP, IsForwarded(ctx), shortMethod)

	// Record the start time of the gRPC call.
	startTime := time.Now()

	// Sanitize the request to replace byte slices with their size.
	requestDetails := sanitizeRequest(req)
	log.Infof("[gRPC call request] %v", requestDetails)

	// Call the handler to handle the gRPC call.
	resp, err := handler(ctx, req)

	// Calculate the execution time of the gRPC call.
	executionTime := time.Since(startTime).Milliseconds()

	// Check if the gRPC call failed.
	if err != nil {
		log.Errorf("[gRPC call failed] executionTime=%dms, error=%v, grpc_status=%s", executionTime, err, status.Code(err).String())
	} else {
		log.Infof("[gRPC call succeeded] executionTime=%dms", executionTime)
	}

	// Return the response and error.
	return resp, err
}

func PanicRecoveryInterceptor(
	reg prometheus.Registerer,
) grpc.UnaryServerInterceptor {
	// Setup panic recovery metric
	panicsTotal := promauto.With(reg).NewCounter(prometheus.CounterOpts{
		Name: "grpc_req_panics_recovered_total",
		Help: "Total number of gRPC requests recovered from internal panic.",
		ConstLabels: prometheus.Labels{
			"service": "grpc", // Additional context
		},
	})

	// Enhanced panic recovery handler
	grpcPanicRecoveryHandler := func(p any) (err error) {
		// Increment panic counter
		panicsTotal.Inc()

		// Convert panic to error
		var panicErr error
		switch v := p.(type) {
		case error:
			panicErr = v
		case string:
			panicErr = errors.New(v)
		default:
			panicErr = fmt.Errorf("panic: %v", p)
		}

		// Log with context
		log.Error("recovered from gRPC panic",
			"panic", panicErr,
			"stack", string(debug.Stack()),
			"timestamp", time.Now().UTC(),
		)

		// Return gRPC status error
		return status.Errorf(codes.Internal, "internal server error")
	}

	// Return the interceptor
	return recovery.UnaryServerInterceptor(
		recovery.WithRecoveryHandler(grpcPanicRecoveryHandler),
	)
}
