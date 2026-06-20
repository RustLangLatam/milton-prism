package handlers

import (
	"context"
	"net/http"
	commonerr "milton_prism/pkg/gateway/common/error"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/grpclog"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/status"
)

var loggerCtx = grpclog.Component("grpc")

func CustomHTTPError(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	// Convert the error to a status object
	s := status.Convert(err)

	// Determine the HTTP status code from the gRPC status code
	st := runtime.HTTPStatusFromCode(s.Code())

	// Log the error at the appropriate level
	logErrorBasedOnCode(s.Code(), s.Message())

	// 2xx requests are fine
	if st <= 300 {
		runtime.DefaultHTTPErrorHandler(ctx, mux, marshaler, w, r, err)
		return
	}

	// Check for valid HTTP status range (between 200 and 505)
	if st < 200 || st > 505 {
		runtime.DefaultHTTPErrorHandler(ctx, mux, marshaler, w, r, err)
		return
	}

	// Build the error message
	errorMessage := commonerr.HandlerErrorMessage(*s)

	// Extract additional details if available
	for _, d := range s.Details() {
		if errorResponse, ok := d.(*commonerr.ErrorMessage); ok {
			errorMessage.Status = errorResponse.Status
		}
	}

	// Set the response headers and status code
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(st)

	// Write the error details to the response body
	body, _ := marshaler.Marshal(commonerr.ErrorMessage{
		Detail: errorMessage.Detail,
		Status: errorMessage.Status,
		Title:  errorMessage.Title,
	})

	_, _ = w.Write(body)
}

// logErrorBasedOnCode logs the error with the appropriate log level based on the gRPC status code.
func logErrorBasedOnCode(code codes.Code, message string) {
	switch {
	case code == codes.Internal || code == codes.Unknown || code == codes.DataLoss || code == codes.Unavailable:
		loggerCtx.Errorf("[Status: %v] %v", code, message)
	case code == codes.InvalidArgument || code == codes.NotFound || code == codes.AlreadyExists || code == codes.PermissionDenied || code == codes.Unauthenticated:
		loggerCtx.Warningf("[Status: %v] %v", code, message)
	default:
		loggerCtx.Infof("[Status: %v] %v", code, message)
	}
}
