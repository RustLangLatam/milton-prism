package config

import (
	"errors"
	"strings"
)

// CORSCfg holds configuration for Cross-Origin Resource Sharing (CORS) settings.
//
// CORS allows servers to specify who can access resources across different origins,
// adding an extra layer of security to web applications.
//
// For more information about CORS, refer to the official documentation:
// https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS
type CORSCfg struct {
	// Enabled determines whether CORS should be enabled for the server.
	// If false, CORS headers will not be added to HTTP responses.
	Enabled bool `toml:"enabled"`

	// AllowedMethods specifies the HTTP methods that are permitted for CORS requests.
	// If not specified, a default set of standard methods is used.
	// Valid methods include: GET, POST, PUT, DELETE, PATCH, OPTIONS, HEAD, CONNECT, TRACE.
	//
	// Reference for HTTP methods: https://developer.mozilla.org/en-US/docs/Web/HTTP/Methods
	AllowedMethods []string `toml:"allowedMethods"`

	// AllowedHeaders lists the headers that can be used when making a CORS request.
	// If not specified, a default set of commonly used headers is allowed.
	//
	// For more on CORS headers, see:
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Headers
	AllowedHeaders []string `toml:"allowedHeaders"`

	// ExposeHeaders specifies the headers that are exposed to the browser in response to a CORS request.
	// By default, it includes "Authorization" and "Refresh" headers.
	//
	// This helps control what headers the client can access in cross-origin requests:
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Expose-Headers
	ExposeHeaders []string `toml:"exposeHeaders"`

	// AllowOrigin defines which origins are permitted to access the resources.
	// Use "*" to allow all origins, or specify a particular origin (e.g., "http://example.com").
	//
	// More on Access-Control-Allow-Origin:
	// https://developer.mozilla.org/en-US/docs/Web/HTTP/Headers/Access-Control-Allow-Origin
	AllowOrigin string `toml:"allowOrigin"`
}

// Default values for optional fields if they are not explicitly set.
var (
	defaultAllowedMethods = []string{"GET", "PUT", "POST", "PATCH", "DELETE", "OPTIONS"}
	defaultAllowedHeaders = []string{"Authorization", "Refresh", "Content-Type", "X-Requested-With"}
	defaultExposeHeaders  = []string{"Authorization", "Refresh"}
	defaultAllowOrigin    = "*"

	// Set of valid HTTP methods for faster lookups when validating user input.
	validHTTPMethods = map[string]struct{}{
		"GET": {}, "POST": {}, "PUT": {}, "DELETE": {}, "PATCH": {},
		"HEAD": {}, "OPTIONS": {}, "CONNECT": {}, "TRACE": {},
	}
)

// Validate checks each CORS configuration parameter for correctness and assigns default values where necessary.
// - `AllowOrigin` is checked to be either "*" or a valid URL format.
// - `AllowedMethods` are validated against standard HTTP methods.
// - If `AllowedHeaders` or `ExposeHeaders` are empty, they are assigned default values.
func (c *CORSCfg) Validate() error {
	// Skip validation if CORS is disabled.
	if !c.Enabled {
		return nil
	}

	// Verify or assign default for AllowOrigin.
	if c.AllowOrigin == "" {
		c.AllowOrigin = defaultAllowOrigin
	} else if c.AllowOrigin != "*" && !strings.HasPrefix(c.AllowOrigin, "http") {
		return errors.New("invalid allow_origin: must be '*' or a valid URL starting with http or https")
	}

	// Verify or set default for AllowedMethods.
	if len(c.AllowedMethods) == 0 {
		c.AllowedMethods = defaultAllowedMethods
	} else if err := validateHTTPMethods(c.AllowedMethods); err != nil {
		return err
	}

	// Set default for AllowedHeaders and ExposeHeaders if not specified.
	if len(c.AllowedHeaders) == 0 {
		c.AllowedHeaders = defaultAllowedHeaders
	}
	if len(c.ExposeHeaders) == 0 {
		c.ExposeHeaders = defaultExposeHeaders
	}

	return nil
}

// validateHTTPMethods checks that all methods in the list are valid HTTP methods.
func validateHTTPMethods(methods []string) error {
	for _, method := range methods {
		if _, valid := validHTTPMethods[strings.ToUpper(method)]; !valid {
			return errors.New("invalid allowed_methods: contains non-standard HTTP method")
		}
	}
	return nil
}
