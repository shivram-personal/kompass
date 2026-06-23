// Package probe runs reachability probes — DNS / TCP / TLS / HTTP — against
// a target the caller already knows about from declared config. The package
// is narrow by design: it does not discover targets, crawl URLs, or run as
// a service. The caller passes a concrete address; this package answers
// "from where this binary is running right now, can I reach it?" with
// strict timeouts and an explicit vantage label on every result.
//
// Vantage matters: a probe failure from a laptop means "your laptop can't
// reach this", not "this is broken." The Result struct carries it so
// downstream consumers can frame the verdict for the operator accordingly.
//
// What this package does NOT do:
//
//   - Accept user-supplied URLs. Targets must come from observed cluster
//     config (Service ports, Ingress addresses, Gateway listeners). The
//     trace composer enforces this; this package trusts its callers.
//   - Follow redirects in HTTP probes. One shot per probe — multi-hop
//     traces should be modeled as separate probe targets, not as redirect
//     chains.
//   - Send a request body, auth headers, or cookies. Every probe is the
//     minimum signal: "did the layer succeed?"
//   - Retry. Time budget for the whole trace is bounded; let the caller
//     decide whether to re-run.
//
// See internal/trace/probes.go for how probes are orchestrated per entry
// kind, and docs/diagnose.md §"Active probes" for the vantage routing
// matrix.
package probe

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
)

// Vantage names where the running binary sits on the network. Detection is
// best-effort: in-cluster means "looks like we're a Pod" (KUBERNETES_SERVICE_HOST
// is set), local means "looks like we're on the operator's machine". A
// downstream caller can override via DetectVantage(... overrides ...).
type Vantage string

const (
	VantageInCluster Vantage = "in-cluster"
	VantageLocal     Vantage = "local"
)

// Layer names which network layer this Result attests to. Higher layers
// strictly imply lower layers succeeded — if HTTP returns ok, TCP and DNS
// did too.
type Layer string

const (
	LayerDNS  Layer = "dns"
	LayerTCP  Layer = "tcp"
	LayerTLS  Layer = "tls"
	LayerHTTP Layer = "http"
)

// Path discriminates which route a Service/Pod probe took when more than
// one was feasible. "data" means the probe went straight to the resource
// over the network (kube-proxy path for ClusterIP, direct dial for PodIP);
// "apiserver" means it tunnelled through the Kubernetes API server's proxy
// subresource. Empty for layers where the question doesn't apply (DNS,
// HTTP to an Ingress hostname, etc.). The graph visualization uses Path
// to place each result on the correct arrow.
type Path string

const (
	PathData      Path = "data"
	PathAPIServer Path = "apiserver"
)

// Tone classifies a Result for the UI when OK alone is too coarse. HTTP
// uses it to distinguish 2xx (healthy) from 3xx/4xx (degraded — responded
// but not at the expected route, or redirected without follow) and 5xx
// (unhealthy). Empty Tone is the default; consumers should infer from
// Skipped + OK in that case.
type Tone string

const (
	ToneHealthy   Tone = "healthy"
	ToneDegraded  Tone = "degraded"
	ToneUnhealthy Tone = "unhealthy"
)

// Result is one probe outcome. Skipped is true when the vantage routing
// matrix decided this probe wouldn't tell the truth (e.g. probing a
// ClusterIP from the local laptop). Empty Error + OK=true means success;
// a non-empty Error always means failure regardless of OK.
type Result struct {
	Layer    Layer         `json:"layer"`
	Target   string        `json:"target"`
	Vantage  Vantage       `json:"vantage"`
	Path     Path          `json:"path,omitempty"`
	OK       bool          `json:"ok"`
	Tone     Tone          `json:"tone,omitempty"`
	Skipped  bool          `json:"skipped,omitempty"`
	Reason   string        `json:"reason,omitempty"`
	Latency  time.Duration `json:"latencyNs,omitempty"`
	Detail   string        `json:"detail,omitempty"`
	Error    string        `json:"error,omitempty"`
}

// DetectVantage reads the process env on every call so tests can override
// deterministically via t.Setenv. The KUBERNETES_SERVICE_HOST sentinel is
// what kubelet injects into every pod; missing means "not a pod".
func DetectVantage() Vantage {
	if v := os.Getenv("KUBERNETES_SERVICE_HOST"); v != "" {
		return VantageInCluster
	}
	return VantageLocal
}

// DNS resolves host with the system resolver, returning the discovered
// addresses on success. Timeout is enforced by ctx; callers should pass a
// timeout-scoped context (≤200ms is typical).
func DNS(ctx context.Context, host string, vantage Vantage) Result {
	r := Result{Layer: LayerDNS, Target: host, Vantage: vantage}
	if host == "" {
		r.Skipped = true
		r.Reason = "empty host"
		return r
	}
	start := time.Now()
	addrs, err := net.DefaultResolver.LookupHost(ctx, host)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = sanitizeError(err)
		return r
	}
	r.OK = true
	r.Detail = "resolved to " + strings.Join(addrs, ", ")
	return r
}

// TCP attempts a single connect+close against addr ("host:port" or
// "ip:port"). On success the connection is closed immediately — we only
// signal that the kernel accepted SYN/ACK, not that any application is
// reading. Timeout is enforced by ctx.
func TCP(ctx context.Context, addr string, vantage Vantage) Result {
	r := Result{Layer: LayerTCP, Target: addr, Vantage: vantage}
	if addr == "" {
		r.Skipped = true
		r.Reason = "empty addr"
		return r
	}
	d := net.Dialer{}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = sanitizeError(err)
		return r
	}
	_ = conn.Close()
	r.OK = true
	return r
}

// TLS does TCP + a TLS handshake with SNI=serverName. Cert verification is
// the default Go behavior (validates against system roots, checks SNI).
// The Detail field carries the cert's CommonName for one-line
// diagnosability.
func TLS(ctx context.Context, addr, serverName string, vantage Vantage) Result {
	r := Result{Layer: LayerTLS, Target: addr, Vantage: vantage}
	if addr == "" {
		r.Skipped = true
		r.Reason = "empty addr"
		return r
	}
	d := tls.Dialer{Config: &tls.Config{ServerName: serverName, MinVersion: tls.VersionTLS12}}
	start := time.Now()
	conn, err := d.DialContext(ctx, "tcp", addr)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = sanitizeError(err)
		return r
	}
	defer func() { _ = conn.Close() }()
	tlsConn, ok := conn.(*tls.Conn)
	if !ok {
		r.OK = true
		return r
	}
	state := tlsConn.ConnectionState()
	if len(state.PeerCertificates) > 0 {
		r.Detail = "cert CN=" + state.PeerCertificates[0].Subject.CommonName
	}
	r.OK = true
	return r
}

// HTTP performs one GET against url. Redirects are not followed: a 301
// response and the destination it points at are independent results, and
// chasing the redirect would conflate them. The Host header is set to host
// when non-empty so callers can probe via IP while presenting a hostname;
// the same value is set as TLS ServerName so SNI matches what the
// certificate names, not the IP in the URL. Without ServerName Go derives
// SNI from the URL host, so a Gateway HTTPS probe dialed by IP would fail
// cert verify even when the server is healthy.
func HTTP(ctx context.Context, url, host string, vantage Vantage) Result {
	r := Result{Layer: LayerHTTP, Target: url, Vantage: vantage}
	if url == "" {
		r.Skipped = true
		r.Reason = "empty url"
		return r
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		r.Error = sanitizeError(err)
		return r
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Set("User-Agent", "radar-trace-probe/1 (https://github.com/skyhook-io/radar)")
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
	if host != "" {
		tlsCfg.ServerName = host
	}
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: tlsCfg},
	}
	start := time.Now()
	resp, err := client.Do(req)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = sanitizeError(err)
		return r
	}
	defer func() { _ = resp.Body.Close() }()
	r.Detail = fmt.Sprintf("HTTP %d", resp.StatusCode)
	switch {
	case resp.StatusCode >= 500:
		// Populate Error too so the row renders the status inline in red
		// rather than only in the muted detail line.
		r.OK = false
		r.Tone = ToneUnhealthy
		r.Error = fmt.Sprintf("HTTP %d", resp.StatusCode)
	case resp.StatusCode >= 400:
		// Path is reachable but the server answered at an unexpected route
		// (a path-routed Ingress probed at `/` is the common case).
		r.OK = true
		r.Tone = ToneDegraded
	case resp.StatusCode >= 300:
		// Redirects are not followed by design; end-to-end reachability
		// of the redirect target is therefore unproven.
		r.OK = true
		r.Tone = ToneDegraded
	default:
		r.OK = true
		r.Tone = ToneHealthy
	}
	return r
}

// ServiceProxy probes a Service via the Kubernetes API server proxy
// (the same path `kubectl proxy` and `kubectl port-forward` use). This is
// what makes "test my Service" work from a laptop: even though the
// ClusterIP isn't routable directly, the kube-apiserver is, and it will
// forward the HTTP request to a backend pod through kube-proxy.
//
// The signal is real but partial: it proves the Service has endpoints and
// at least one backend responds to HTTP. It does NOT prove that traffic
// from a workload in the cluster would reach the same pod the same way —
// the apiserver-proxy and the data-path are different code paths. The
// orchestrator tags the Result detail so operators reading the trace know
// which one ran.
//
// Authentication: the client's identity is the kubeconfig identity. RBAC
// needs `get services/proxy` in the namespace; common in dev/admin
// contexts, sometimes denied in production-restricted ones.
func ServiceProxy(ctx context.Context, client kubernetes.Interface, namespace, name string, port int32, vantage Vantage) Result {
	r := Result{Layer: LayerHTTP, Target: fmt.Sprintf("port %d", port), Vantage: vantage, Path: PathAPIServer}
	if client == nil {
		r.Error = "couldn't reach cluster API"
		return r
	}
	portName := fmt.Sprintf("%d", port)
	start := time.Now()
	_, err := client.CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("services").
		Name(name + ":" + portName).
		SubResource("proxy").
		Suffix("/").
		DoRaw(ctx)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = translateAPIError(err)
		return r
	}
	r.OK = true
	return r
}

// PodProxy probes a Pod via the Kubernetes API server proxy. The apiserver
// routes the request through a kubelet hop rather than kube-proxy, so the
// result reflects "the cluster API can reach this pod" — not necessarily
// the same as pod-to-pod direct dial.
func PodProxy(ctx context.Context, client kubernetes.Interface, namespace, name string, port int32, vantage Vantage) Result {
	r := Result{Layer: LayerHTTP, Target: fmt.Sprintf("%s port %d", name, port), Vantage: vantage, Path: PathAPIServer}
	if client == nil {
		r.Error = "couldn't reach cluster API"
		return r
	}
	portName := fmt.Sprintf("%d", port)
	start := time.Now()
	_, err := client.CoreV1().RESTClient().Get().
		Namespace(namespace).
		Resource("pods").
		Name(name + ":" + portName).
		SubResource("proxy").
		Suffix("/").
		DoRaw(ctx)
	r.Latency = time.Since(start)
	if err != nil {
		r.Error = translateAPIError(err)
		return r
	}
	r.OK = true
	return r
}

// Skipped returns a structured "not attempted, here's why" record so the
// orchestrator can surface the reason instead of silently dropping the
// probe when the current vantage can't route to the target.
func Skipped(layer Layer, target string, vantage Vantage, reason string) Result {
	return Result{Layer: layer, Target: target, Vantage: vantage, Skipped: true, Reason: reason}
}

func sanitizeError(err error) string {
	// Strip the OS-specific prefix that net.Error often adds — operators
	// don't care about "dial tcp" framing, they care about "connection
	// refused" or "i/o timeout". Keep the message short and parseable.
	s := err.Error()
	if i := strings.LastIndex(s, ": "); i > 0 && i < len(s)-2 {
		return s[i+2:]
	}
	return s
}

// translateAPIError rewrites the most common Kubernetes apiserver error
// strings into operator English. The default `err.Error()` leaks wire
// internals like "(get pods my-pod:8080)" that mean nothing to someone
// debugging connectivity. Anything we don't recognize falls back to the
// generic sanitizer.
func translateAPIError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	low := strings.ToLower(s)
	switch {
	case strings.Contains(low, "could not find the requested resource"):
		return "No backend pod is answering on this port via the Kubernetes API."
	case strings.Contains(low, "forbidden"):
		return "Permission denied. Your kubeconfig identity lacks get services/proxy or get pods/proxy in this namespace."
	case strings.Contains(low, "no route to host"):
		return "No route to host. Pod is scheduled but unreachable from the apiserver node."
	case strings.Contains(low, "connection refused"):
		return "Connection refused. Nothing is listening on the port."
	case strings.Contains(low, "i/o timeout"), strings.Contains(low, "context deadline exceeded"):
		return "Timed out. Port accepted no connection within the probe budget."
	case strings.Contains(low, "eof"):
		return "Connection closed before response. Backend likely doesn't speak HTTP/1.1 on this port."
	case strings.Contains(low, "tls:"), strings.Contains(low, "x509:"):
		return "TLS handshake failed. Port may require HTTPS or a specific cipher."
	case strings.Contains(low, "bad request"), strings.Contains(low, "503"), strings.Contains(low, "service unavailable"):
		return "Kubernetes API rejected the proxy request. Backend may have crashed mid-response."
	}
	return sanitizeError(err)
}
