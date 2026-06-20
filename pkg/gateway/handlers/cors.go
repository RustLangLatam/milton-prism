// Package handlers provides HTTP middleware and utility handlers for the
// milton-prism API gateway, including CORS, logging, metrics, and error formatting.
package handlers

import (
	"milton_prism/pkg/config" // Adjust as needed
	"net/http"
	"strings"
)

// HandlerEnableCors applies CORS settings based on the given configuration.
func HandlerEnableCors(h http.Handler, cfg *config.CORSCfg) http.Handler {
	if !cfg.Enabled {
		return h // Return the handler without any CORS modifications if CORS is disabled
	}

	allowedMethods := strings.Join(cfg.AllowedMethods, ", ")
	allowedHeaders := strings.Join(cfg.AllowedHeaders, ", ")
	exposeHeaders := strings.Join(cfg.ExposeHeaders, ", ")

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cfg.AllowOrigin == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else {
			if origin := r.Header.Get("Origin"); origin != "" {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			} else {
				w.Header().Set("Access-Control-Allow-Origin", cfg.AllowOrigin)
			}
		}

		w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
		w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
		w.Header().Set("Access-Control-Expose-Headers", exposeHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		h.ServeHTTP(w, r)
	})
}
