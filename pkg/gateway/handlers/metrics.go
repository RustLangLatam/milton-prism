package handlers

import (
	"net/http"
	"milton_prism/pkg/gateway/metrics_collector"
	"milton_prism/pkg/log"
	"time"
)

func HandlerMetricsMiddleware(
	collector metrics_collector.HttpApiMetrics,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		fn := func(w http.ResponseWriter, r *http.Request) {
			handleRequestWithMetrics(collector, next, w, r)
		}

		return http.HandlerFunc(fn)
	}
}

func handleRequestWithMetrics(
	collector metrics_collector.HttpApiMetrics,
	next http.Handler,
	w http.ResponseWriter,
	r *http.Request,
) {
	defer logRecovery(w)

	start := time.Now()
	wrapped := wrapResponseWriter(w)

	next.ServeHTTP(wrapped, r)

	collectResponseStatus(collector, wrapped.status)
	status := getStatusOrDefault(wrapped.status)
	collectRequestMetrics(collector, r, status, start)
}

func logRecovery(w http.ResponseWriter) {
	if err := recover(); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		log.Error("failed to handle request metrics")
	}
}

func collectResponseStatus(collector metrics_collector.HttpApiMetrics, status int) {
	if status < 500 && status >= 400 {
		collector.Collect400TotalRequests()
	} else if status >= 500 {
		collector.Collect500TotalRequests()
	}
}

func getStatusOrDefault(status int) int {
	if status == 0 {
		return http.StatusOK
	}

	return status
}

func collectRequestMetrics(
	collector metrics_collector.HttpApiMetrics,
	r *http.Request,
	status int,
	start time.Time,
) {
	collector.CollectTotalRequests()
	method, path := r.Method, r.URL.EscapedPath()
	duration := time.Since(start).Seconds()
	contentLength := float64(r.ContentLength)

	collector.CollectRequest(method, path, status)
	collector.CollectRequestDuration(method, path, duration)
	collector.CollectRequestContentLength(method, path, contentLength)
}
