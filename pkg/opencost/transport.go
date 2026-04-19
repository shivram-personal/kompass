package opencost

import (
	"context"
	"net/url"
)

// Transport is the HTTP transport used by RESTClient to reach OpenCost's
// REST API. Same shape as pkg/prom.Transport (path + params in, body out)
// so a single concrete type in a caller can satisfy both interfaces.
//
// Implementations:
//
//   - radar desktop: direct HTTP against a port-forwarded localhost URL
//   - koala-backend: routed through CAC proxy → skyhook-connector → in-cluster OpenCost
//   - tests:         httptest server
type Transport interface {
	// Do issues a request to path (e.g. "/allocation") with query
	// parameters and returns the raw response body. Non-2xx responses
	// should be returned as errors so callers don't have to re-check.
	Do(ctx context.Context, method, path string, params url.Values) ([]byte, error)

	// Address returns a diagnostic identifier for this transport (the
	// upstream URL, or a human-readable description).
	Address() string
}
