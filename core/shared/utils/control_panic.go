// Package utils provides miscellaneous cross-cutting utilities shared
// across all microservices in the milton-prism platform.
package utils

import (
	"runtime/debug"
	"milton_prism/core/shared/grpc_health"
	"milton_prism/pkg/log"

	healthgrpc "google.golang.org/grpc/health/grpc_health_v1"
)

// panicID is an unbuffered channel used to send unique IDs for each panic event.
var panicID = make(chan uint16)

// PanicCounter starts generating unique IDs, and will invoke a custom callback or raise a panic if the specified limit is reached.
// If the onLimitReached callback is nil, it sets the HealthStatus to NOT_SERVING.
//
// Arguments:
//   - limit: The maximum number of panics allowed before triggering the callback or default behavior.
//   - onLimitReached: A function that executes when the panic limit is reached. If `nil`, HealthStatus is set to NOT_SERVING.
//
// Usage:
//   - Run as a goroutine to continuously generate IDs, sending them to the `panicID` channel.
//   - When `limit` is reached, `onLimitReached` is called, or HealthStatus changes to NOT_SERVING.
//
// Example:
//
//	go paniccontrol.PanicCounter(10, func() {
//	    fmt.Println("Reached panic limit! Server shutting down...")
//	})
func PanicCounter(limit uint16, onLimitReached func()) {
	defer RecoverFromPanic() // Ensure that panic is recovered and logged

	for i := uint16(1); ; i++ {
		panicID <- i

		// When the count reaches the defined limit, invoke the callback or set HealthStatus
		if limit > 0 && i == limit {
			if onLimitReached != nil {
				onLimitReached()
			} else {
				// Set HealthStatus to NOT_SERVING when limit is reached and no callback is provided
				grpc_health.HealthStatus = healthgrpc.HealthCheckResponse_NOT_SERVING
				//panic("Panic limit reached! Shutting down the server.")
			}
			return // Exit after limit handling
		}
	}
}

// RecoverFromPanic is a deferred function used to capture and log panic events.
// It logs the panic message along with the stack trace for debugging.
//
// Usage:
//   - Use `RecoverFromPanic` in a deferred statement in any function where you need to handle panics.
//   - Automatically logs the panic value and stack trace using the application logger.
//
// Example:
//
//	defer panic control.RecoverFromPanic()
func RecoverFromPanic() {
	if r := recover(); r != nil {
		log.Errorf("Recovered from panic: %v", r)
		log.Errorf("Stack trace:\n%s", debug.Stack())
	}
}
