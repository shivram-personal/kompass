package prometheus

import (
	"context"
	"maps"
	"net/http"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/skyhook-io/radar/internal/errorlog"
	"github.com/skyhook-io/radar/pkg/prom"
)

// Client is radar's application-scoped Prometheus client. It holds the
// K8s-aware state required for kubectl-like port-forward discovery, along
// with a pkg/prom.Client that performs the actual HTTP calls once an
// endpoint has been discovered.
type Client struct {
	mu sync.RWMutex

	// Effective connection (populated after discover succeeds).
	baseURL  string
	basePath string
	prom     *prom.Client // rebuilt whenever baseURL/basePath changes

	// Discovery state
	discovered       bool
	discoveryService *ServiceInfo // discovered service info for port-forward
	manualURL        string       // --prometheus-url override
	headers          map[string]string

	// K8s clients for discovery
	k8sClient   kubernetes.Interface
	k8sConfig   *rest.Config
	contextName string

	// Shared HTTP client used when constructing the underlying pkg/prom.Client.
	httpClient *http.Client
}

// Global client instance
var (
	globalClient *Client
	clientMu     sync.RWMutex
)

// Initialize creates the global Prometheus client.
func Initialize(client kubernetes.Interface, config *rest.Config, contextName string) {
	clientMu.Lock()
	defer clientMu.Unlock()

	globalClient = &Client{
		k8sClient:   client,
		k8sConfig:   config,
		contextName: contextName,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// SetManualURL sets the --prometheus-url override on the global client.
func SetManualURL(rawURL string) {
	clientMu.Lock()
	defer clientMu.Unlock()
	if globalClient != nil {
		globalClient.manualURL = strings.TrimRight(rawURL, "/")
	}
}

// SetHeaders sets HTTP headers attached to every Prometheus request on the
// global client. Pass nil or an empty map to clear.
func SetHeaders(h map[string]string) {
	clientMu.RLock()
	c := globalClient
	clientMu.RUnlock()
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.headers = copyHeaders(h)
	// Drop the cached prom.Client so the next request rebuilds its transport
	// with the new headers.
	c.prom = nil
}

func copyHeaders(h map[string]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	maps.Copy(out, h)
	return out
}

// SetURL overrides discovery with a specific Prometheus URL.
// Clears existing connection state so the next EnsureConnected uses this URL.
func (c *Client) SetURL(rawURL string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.manualURL = strings.TrimRight(rawURL, "/")
	c.baseURL = ""
	c.basePath = ""
	c.prom = nil
	c.discovered = false
	c.discoveryService = nil
}

// GetClient returns the global Prometheus client (may be nil).
func GetClient() *Client {
	clientMu.RLock()
	defer clientMu.RUnlock()
	return globalClient
}

// Reset clears connection state so the next query triggers rediscovery (used on context switch).
func Reset() {
	clientMu.Lock()
	defer clientMu.Unlock()
	if globalClient != nil {
		globalClient.mu.Lock()
		globalClient.baseURL = ""
		globalClient.basePath = ""
		globalClient.prom = nil
		globalClient.discovered = false
		globalClient.discoveryService = nil
		globalClient.mu.Unlock()
	}
}

// Reinitialize recreates the client with new K8s connection info.
func Reinitialize(client kubernetes.Interface, config *rest.Config, contextName string) {
	clientMu.Lock()
	defer clientMu.Unlock()

	manualURL := ""
	var headers map[string]string
	if globalClient != nil {
		// SetURL / SetHeaders write these under the per-client mutex after
		// dropping clientMu, so reading without c.mu here would race even
		// though we hold clientMu exclusively. copyHeaders also detaches the
		// map from the old client so a late mutation can't bleed through.
		globalClient.mu.RLock()
		manualURL = globalClient.manualURL
		headers = copyHeaders(globalClient.headers)
		globalClient.mu.RUnlock()
	}

	globalClient = &Client{
		k8sClient:   client,
		k8sConfig:   config,
		contextName: contextName,
		manualURL:   manualURL,
		headers:     headers,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

// GetStatus returns the current Prometheus connection status.
func (c *Client) GetStatus() Status {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var svc *ServiceInfo
	if c.discoveryService != nil {
		cp := *c.discoveryService
		svc = &cp
	}

	return Status{
		Available:   c.baseURL != "",
		Connected:   c.baseURL != "",
		Address:     c.baseURL,
		Service:     svc,
		ContextName: c.contextName,
	}
}

// EnsureConnected attempts to discover and connect to Prometheus if not already connected.
// Returns the base URL and base path, or an error.
func (c *Client) EnsureConnected(ctx context.Context) (string, string, error) {
	c.mu.RLock()
	base := c.baseURL
	bp := c.basePath
	cached := c.prom
	c.mu.RUnlock()

	if base != "" && cached != nil {
		ok, _ := cached.Probe(ctx)
		if ok {
			return base, bp, nil
		}
		// Stale — clear and rediscover
		c.mu.Lock()
		c.baseURL = ""
		c.basePath = ""
		c.prom = nil
		c.discovered = false
		c.mu.Unlock()
	}

	return c.discover(ctx)
}

// Prom returns the underlying pkg/prom.Client for callers that compose cost
// math on top of raw Query/QueryRange (e.g. pkg/opencost.ComputeCostSummaryFromProm).
// Callers must have run EnsureConnected first; returns nil if discovery has
// not produced a baseURL.
func (c *Client) Prom() *prom.Client {
	return c.getPromClient()
}

// getPromClient returns a pkg/prom.Client pointed at the current baseURL/basePath,
// building (and caching) one if necessary. Callers must hold the read or
// write lock appropriately; see QueryRange/Query.
func (c *Client) getPromClient() *prom.Client {
	c.mu.RLock()
	if c.prom != nil {
		p := c.prom
		c.mu.RUnlock()
		return p
	}
	base, bp, httpC := c.baseURL, c.basePath, c.httpClient
	headers := copyHeaders(c.headers)
	c.mu.RUnlock()

	if base == "" {
		return nil
	}

	tr := prom.NewHTTPTransport(base, bp, httpC)
	tr.Headers = headers
	p := prom.NewClient(tr)
	c.mu.Lock()
	// Double-check in case another goroutine built one.
	if c.prom == nil {
		c.prom = p
	} else {
		p = c.prom
	}
	c.mu.Unlock()
	return p
}

// probe checks if a Prometheus endpoint at `addr` is reachable and has at
// least one active scrape target, using pkg/prom.Client.Probe. Retained as
// a method on *Client so existing discovery call sites (which test many
// candidate addresses before committing) can continue to use `c.probe(...)`.
// Also records an errorlog warning when a candidate is skipped because the
// instance has no active scrape targets, so operators can see why discovery
// moved past an otherwise-reachable endpoint.
func (c *Client) probe(ctx context.Context, addr string) bool {
	c.mu.RLock()
	httpC := c.httpClient
	headers := copyHeaders(c.headers)
	c.mu.RUnlock()
	tr := prom.NewHTTPTransport(addr, "", httpC)
	tr.Headers = headers
	ok, reason := prom.NewClient(tr).Probe(ctx)
	if !ok && reason == prom.ProbeReasonEmptyInstance {
		errorlog.Record("prometheus", "warning",
			"endpoint %s has no active scrape targets (empty instance), skipping", addr)
	}
	return ok
}

// QueryRange executes a Prometheus range query via the underlying pkg/prom.Client.
func (c *Client) QueryRange(ctx context.Context, query string, start, end time.Time, step time.Duration) (*QueryResult, error) {
	if _, _, err := c.EnsureConnected(ctx); err != nil {
		return nil, err
	}
	return c.getPromClient().QueryRange(ctx, query, start, end, step)
}

// Query executes a Prometheus instant query via the underlying pkg/prom.Client.
func (c *Client) Query(ctx context.Context, query string) (*QueryResult, error) {
	if _, _, err := c.EnsureConnected(ctx); err != nil {
		return nil, err
	}
	return c.getPromClient().Query(ctx, query)
}
