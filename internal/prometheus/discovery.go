package prometheus

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/skyhook-io/radar/internal/errorlog"
	"github.com/skyhook-io/radar/internal/portforward"
	"github.com/skyhook-io/radar/pkg/prom"
)

// discover finds and connects to Prometheus using a multi-layer approach:
//  1. Manual URL override (--prometheus-url)
//  2. Existing traffic system port-forward
//  3. Well-known service locations (via pkg/prom.Discover)
//  4. Dynamic cluster-wide discovery with scoring (via pkg/prom.Discover)
//
// Well-known + dynamic candidate enumeration lives in pkg/prom.Discover so
// it can be shared with non-desktop callers. This function owns the
// *desktop-specific* port-forward fallback behavior.
//
// The lock is only held briefly to read/write state, not during network I/O.
func (c *Client) discover(ctx context.Context) (string, string, error) {
	// Layer 1: Manual URL override
	c.mu.RLock()
	manualURL := c.manualURL
	contextName := c.contextName
	k8sClient := c.k8sClient
	c.mu.RUnlock()

	if manualURL != "" {
		addr := strings.TrimRight(manualURL, "/")
		if c.probe(ctx, addr) {
			log.Printf("[prometheus] Using manual URL: %s", addr)
			c.markConnected(addr, "")
			return addr, "", nil
		}
		errorlog.Record("prometheus", "error", "manual Prometheus URL %s not reachable", addr)
		return "", "", fmt.Errorf("manual Prometheus URL %s not reachable", addr)
	}

	// Layer 2: Reuse traffic system's existing port-forward if present
	if pfAddr := portforward.GetAddress(contextName); pfAddr != "" {
		if c.probe(ctx, pfAddr) {
			log.Printf("[prometheus] Using traffic system port-forward: %s", pfAddr)
			c.markConnected(pfAddr, "")
			return pfAddr, "", nil
		}
	}

	if k8sClient == nil {
		return "", "", fmt.Errorf("no Kubernetes client available for discovery")
	}

	// Layers 3 + 4: Enumerate candidates via the shared pkg/prom discovery
	// logic. Well-known first, then dynamic fallbacks.
	candidates, err := prom.Discover(ctx, k8sClient, prom.DiscoverOptions{
		IncludeDynamic: true,
		Logger: func(format string, args ...interface{}) {
			log.Printf("[prometheus] "+format, args...)
		},
	})
	if err != nil {
		log.Printf("[prometheus] Discover error: %v", err)
	}
	if len(candidates) == 0 {
		errorlog.Record("prometheus", "warning", "no Prometheus service found in cluster")
		return "", "", fmt.Errorf("no Prometheus service found in cluster")
	}

	log.Printf("[prometheus] Found %d candidate(s), probing...", len(candidates))

	// First pass: probe each candidate at its in-cluster address. Works when
	// radar is running in-cluster OR when the user's shell can route to the
	// cluster DNS (rare, but cheap to try).
	for _, cand := range candidates {
		addr := cand.ClusterAddr + cand.BasePath
		if c.probe(ctx, addr) {
			log.Printf("[prometheus] Connected to %s/%s at %s (source=%s, score=%d)",
				cand.Namespace, cand.Name, cand.ClusterAddr, cand.Source, cand.Score)
			c.setDiscoveryServiceFromCandidate(cand)
			c.markConnected(cand.ClusterAddr, cand.BasePath)
			return cand.ClusterAddr, cand.BasePath, nil
		}
	}

	// Fallback: start a port-forward to the highest-priority candidate and
	// retry. This is the desktop-specific path — connector and backend have
	// in-cluster network access and therefore never reach this code.
	best := candidates[0]
	log.Printf("[prometheus] No candidate reachable in-cluster, starting port-forward to %s/%s...",
		best.Namespace, best.Name)
	c.setDiscoveryServiceFromCandidate(best)

	connInfo, pfErr := portforward.Start(ctx, best.Namespace, best.Name, best.TargetPort, contextName)
	if pfErr != nil {
		errorlog.Record("prometheus", "error", "port-forward to %s/%s failed: %v", best.Namespace, best.Name, pfErr)
		return "", "", fmt.Errorf("port-forward to %s/%s failed: %w", best.Namespace, best.Name, pfErr)
	}

	addr := connInfo.Address
	if c.probe(ctx, addr+best.BasePath) {
		c.markConnected(addr, best.BasePath)
		return addr, best.BasePath, nil
	}

	portforward.Stop()
	errorlog.Record("prometheus", "error", "Prometheus at %s/%s not responding after port-forward", best.Namespace, best.Name)
	return "", "", fmt.Errorf("Prometheus at %s/%s not responding after port-forward", best.Namespace, best.Name)
}

// setDiscoveryServiceFromCandidate records the discovered service metadata
// from a pkg/prom.Candidate.
func (c *Client) setDiscoveryServiceFromCandidate(cand prom.Candidate) {
	c.mu.Lock()
	c.discoveryService = &prom.ServiceInfo{
		Namespace: cand.Namespace,
		Name:      cand.Name,
		Port:      cand.Port,
		BasePath:  cand.BasePath,
	}
	c.mu.Unlock()
}

// markConnected records the active connection and marks discovery as complete.
func (c *Client) markConnected(addr, basePath string) {
	c.mu.Lock()
	c.baseURL = addr
	c.basePath = basePath
	c.discovered = true
	c.mu.Unlock()
}
