package trace

import (
	"context"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/skyhook-io/radar/pkg/probe"
)

const (
	defaultProbeBudget = 3 * time.Second
	dnsTimeout         = 250 * time.Millisecond
	tcpTimeout         = 700 * time.Millisecond
	tlsTimeout         = time.Second
	httpTimeout        = time.Second
)

// detectVantage is an internal alias so the trace package can mock vantage
// in tests via package-level override without touching pkg/probe's API.
var detectVantage = probe.DetectVantage

// serviceProxyProbe and podProxyProbe are call-site indirections so tests
// can stub the apiserver-proxy path without exercising client-go's REST
// stack (the typed fake client returns a non-nil RESTClient interface
// holding a nil pointer, which panics deep inside client-go).
var (
	serviceProxyProbe = probe.ServiceProxy
	podProxyProbe     = probe.PodProxy
)

// runProbes augments the static trace with reachability probes, sized to
// the caller's budget. Hops are probed in parallel because they target
// independent resources — a slow Gateway listener shouldn't delay the
// Service hop's TCP probe. Within each hop, probes stay sequential because
// later layers depend on earlier ones (TLS only matters if TCP succeeded).
// The overall budget enforces that even a misbehaving fanout cannot exceed
// the operator's patience.
func runProbes(ctx context.Context, t *Trace, opts Options, client kubernetes.Interface) {
	if t == nil || ctx.Err() != nil {
		return
	}
	budget := opts.ProbeBudget
	if budget <= 0 {
		budget = defaultProbeBudget
	}
	deadline := time.Now().Add(budget)
	probeCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	vantage := detectVantage()

	var wg sync.WaitGroup
	probeHops := func(hops []Hop) {
		for i := range hops {
			i := i
			if time.Now().After(deadline) {
				hops[i].Probes = append(hops[i].Probes, probe.Skipped(
					probe.LayerTCP, "", vantage, "probe budget exhausted before this hop",
				))
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				hops[i].Probes = probeHop(probeCtx, &hops[i], vantage, client)
			}()
		}
	}
	probeHops(t.Downstream)
	probeHops(t.Upstreams)
	wg.Wait()

	// Dual-path divergence is the most operator-actionable signal the active
	// layer produces: when an in-cluster probe succeeds via one path and
	// fails via the other, the failure is isolated to that path's subsystem
	// (NetworkPolicy / kube-proxy / sidecar vs apiserver / RBAC).
	attachPathDivergenceFindings(t)
}

// attachPathDivergenceFindings walks every hop and, when the in-cluster
// data path and the apiserver path produced contradictory results for the
// same target, attaches a Finding that names which path failed and points
// at the likely subsystems to investigate. The finding flows through the
// same Finding pipeline the rest of the UI already renders.
//
// Hop findings are re-sorted worst-first after the append: the UI treats
// findings[0] as the hop's overall severity, so a freshly-appended warning
// divergence row mustn't sit after a pre-existing info finding.
func attachPathDivergenceFindings(t *Trace) {
	visit := func(hops []Hop) {
		for i := range hops {
			extras := pathDivergenceFindings(hops[i].Probes)
			if len(extras) == 0 {
				continue
			}
			hops[i].Findings = append(hops[i].Findings, extras...)
			sortFindingsBySeverity(hops[i].Findings)
		}
	}
	visit(t.Downstream)
	visit(t.Upstreams)
}

// pathDivergenceFindings inspects one hop's probe results and emits one
// Finding per port where the data-path and apiserver-path verdicts
// disagree. Same-port same-result rows are silent. Returns an empty slice
// when there's no divergence to flag.
//
// Bucket key is the port — the two paths label their targets differently
// ("10.0.0.5:80" vs "port 80"), but a hop is one logical resource, so the
// trailing port number is enough to pair up data-path vs apiserver-path
// results that refer to the same backend.
//
// Buckets are visited in sorted-key order so output is deterministic; map
// iteration would otherwise hide some divergences and randomise which one
// surfaces between requests.
//
// On a multi-replica Pods hop, several probes share a port key. Mixed
// results on one side (one pod OK, one pod fail) are NOT divergence —
// that's a partial-fleet failure the per-row severities already surface.
// Divergence requires unanimous failure on one side and unanimous success
// on the other for the same port; anything else stays silent.
func pathDivergenceFindings(probes []probe.Result) []Finding {
	type sides struct {
		dataOK, dataFail int
		apiOK, apiFail   int
	}
	by := map[string]*sides{}
	for _, p := range probes {
		if p.Skipped || p.Target == "" {
			continue
		}
		key := portKey(p.Target)
		if key == "" {
			continue
		}
		s := by[key]
		if s == nil {
			s = &sides{}
			by[key] = s
		}
		switch p.Path {
		case probe.PathData:
			if p.OK {
				s.dataOK++
			} else {
				s.dataFail++
			}
		case probe.PathAPIServer:
			if p.OK {
				s.apiOK++
			} else {
				s.apiFail++
			}
		}
	}
	keys := make([]string, 0, len(by))
	for k := range by {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []Finding
	for _, k := range keys {
		s := by[k]
		dataAllFail := s.dataFail > 0 && s.dataOK == 0
		dataAllOK := s.dataOK > 0 && s.dataFail == 0
		apiAllFail := s.apiFail > 0 && s.apiOK == 0
		apiAllOK := s.apiOK > 0 && s.apiFail == 0
		if dataAllFail && apiAllOK {
			out = append(out, Finding{
				Code:     "probe:data-path-only-broken",
				Severity: SeverityWarning,
				Message:  "Pod-to-pod path failed while the Kubernetes API reached the same target. Real workload traffic may be blocked even though the apiserver-side check succeeded.",
				Cause:    "Data-path failure isolated to the in-cluster route. NetworkPolicy, kube-proxy, or service-mesh interception are the usual suspects.",
				Action:   "Check NetworkPolicy in the namespace, kube-proxy health on the receiving node, and any sidecar that may be intercepting the port.",
			})
			continue
		}
		if dataAllOK && apiAllFail {
			out = append(out, Finding{
				Code:     "probe:apiserver-path-only-broken",
				Severity: SeverityInfo,
				Message:  "Pod-to-pod path succeeded but the Kubernetes API could not reach the same target. Workload traffic is probably fine; the apiserver path failure is likely not real-world impacting.",
				Cause:    "Apiserver proxy refused or the port speaks a non-HTTP protocol the proxy can't relay.",
				Action:   "Confirm the user identity holds get services/proxy or get pods/proxy in this namespace, and that the port serves HTTP if you expected the apiserver path to work.",
			})
		}
	}
	return out
}

// probeHop dispatches by hop kind. The primitive used depends on what works
// from the current vantage — in-cluster gets direct TCP for ClusterIPs;
// local falls back to the Kubernetes API server proxy when a kubeconfig is
// available. The user never sees this distinction at the primary level;
// it surfaces only in the Detail tag of each result.
func probeHop(ctx context.Context, h *Hop, vantage probe.Vantage, client kubernetes.Interface) []probe.Result {
	switch h.Resource.Kind {
	case "Service":
		return probeService(ctx, h, vantage, client)
	case "Pods":
		return probePods(ctx, h, vantage, client)
	case "Ingress":
		return probeIngress(ctx, h, vantage)
	case "Gateway":
		return probeGateway(ctx, h, vantage)
	case "HTTPRoute", "GRPCRoute":
		// Routes don't carry their own routable address; reachability
		// belongs on the Gateway listener and on the backend Service.
		return []probe.Result{probe.Skipped(probe.LayerTCP, "", vantage,
			"route has no own address; reachability lives on parent Gateway and backend Service")}
	}
	return nil
}

// probeService runs every feasible path for each port. In-cluster + client
// gets both direct TCP (the data path through kube-proxy) and ServiceProxy
// (through the apiserver) — divergence between them isolates a NetworkPolicy
// or kube-proxy issue from an apiserver-side issue. From a laptop only the
// apiserver path is reachable; in-cluster without a client falls back to
// direct TCP only. The apiserver path is HTTP-only, so non-HTTP ports are
// skipped with a reason instead of producing a false fail.
func probeService(ctx context.Context, h *Hop, vantage probe.Vantage, client kubernetes.Interface) []probe.Result {
	if h.Config == nil || len(h.Config.Ports) == 0 {
		return nil
	}
	headless := h.Config.ClusterIP == "" || h.Config.ClusterIP == "None"

	var out []probe.Result
	for _, p := range h.Config.Ports {
		if ctx.Err() != nil {
			break
		}
		dataReachable := !headless && vantage == probe.VantageInCluster
		if dataReachable {
			pctx, cancel := context.WithTimeout(ctx, tcpTimeout)
			r := probe.TCP(pctx, net.JoinHostPort(h.Config.ClusterIP, strconv.Itoa(int(p.Port))), vantage)
			r.Path = probe.PathData
			out = append(out, r)
			cancel()
		}
		if client != nil {
			target := fmt.Sprintf("port %d", p.Port)
			if !isHTTPProbablePort(p.Name, p.AppProtocol, p.Port) {
				skip := probe.Skipped(probe.LayerHTTP, target, vantage,
					nonHTTPSkipReason(p.Name, p.AppProtocol, p.Port, vantage))
				skip.Path = probe.PathAPIServer
				out = append(out, skip)
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, httpTimeout)
			out = append(out, serviceProxyProbe(pctx, client, h.Resource.Namespace, h.Resource.Name, p.Port, vantage))
			cancel()
			continue
		}
		if !dataReachable {
			// A nil client here has two distinct causes that the probe
			// layer can't otherwise distinguish: from laptop vantage
			// it means no kubeconfig is wired up for proxying; from
			// in-cluster vantage it means the per-request impersonated
			// identity could not be constructed (an auth/RBAC failure).
			// Use vantage to pick the message so the operator
			// investigates the right layer.
			reason := "Kubernetes API isn't reachable from here, so the apiserver path can't be tested."
			if vantage == probe.VantageInCluster {
				reason = "Apiserver path couldn't be tested for this request - your identity may lack permission to proxy."
			}
			skip := probe.Skipped(probe.LayerHTTP, fmt.Sprintf("port %d", p.Port), vantage, reason)
			skip.Path = probe.PathAPIServer
			out = append(out, skip)
		}
	}
	return out
}

// isHTTPProbablePort decides whether the API server proxy is likely to
// succeed against the given port. Signals checked in priority order:
//
//  1. appProtocol (k8s 1.20+) — authoritative when set.
//  2. Conventional port name — many Helm charts label ports "http",
//     "postgres", etc.
//  3. Well-known port number — catches the common no-metadata case (a Helm
//     chart that ships Redis on 6379 without setting name or appProtocol).
//
// gRPC and HTTP/2 (h2c/h2) are explicitly excluded: probe.HTTP uses the
// standard net/http client which speaks HTTP/1.1, so an HTTP/2-only upstream
// would report a false fail.
//
// No-signal default is true — most Service ports are HTTP-shaped — so an
// unclassified web service still gets probed rather than unexplained-skipped.
func isHTTPProbablePort(name, appProtocol string, port int32) bool {
	if ap := strings.ToLower(strings.TrimSpace(appProtocol)); ap != "" {
		switch ap {
		case "http", "https", "ws", "wss", "kubernetes.io/ws", "kubernetes.io/wss":
			return true
		}
		return false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "grpc", "grpc-web", "h2", "h2c",
		"postgres", "postgresql", "pg",
		"mysql", "mariadb",
		"redis",
		"mongo", "mongodb",
		"kafka",
		"amqp", "rabbitmq",
		"smtp", "imap", "pop3",
		"ssh", "ftp", "sftp",
		"dns",
		"udp", "tcp",
		"mqtt",
		"memcached",
		"cassandra",
		"elasticsearch-transport":
		return false
	}
	switch port {
	case 5432, 5433, // postgres
		3306, 3307, // mysql / mariadb
		6379, 6380, // redis
		27017, 27018, 27019, // mongodb
		9042, // cassandra
		9092, // kafka
		5672, // amqp / rabbitmq
		25, 465, 587, // smtp
		22,                // ssh
		21,                // ftp
		53,                // dns
		11211,             // memcached
		1883, 8883,        // mqtt
		2181,              // zookeeper
		7000, 7001:        // misc TCP defaults
		return false
	}
	return true
}

// nonHTTPSkipReason explains in plain operator English why we didn't run the
// apiserver-path probe against this port, and (from a laptop) why no TCP
// probe ran either. The laptop case is load-bearing: dataReachable is false,
// so the apiserver skip is the ONLY row the operator sees; without the
// "run in-cluster" hint they may assume TCP was checked when it wasn't.
// Avoid Kubernetes spec syntax — the reader is debugging connectivity,
// not editing YAML.
func nonHTTPSkipReason(portName, appProtocol string, port int32, vantage probe.Vantage) string {
	base := nonHTTPBaseReason(portName, appProtocol, port)
	if vantage == probe.VantageLocal {
		return base + " Run Radar from in-cluster to verify TCP reachability."
	}
	// In-cluster, direct TCP probes ran in parallel; the gRPC case can
	// truthfully say so. From a laptop the caller has appended the
	// "run in-cluster" hint instead.
	if isGRPCLike(portName, appProtocol) {
		return base + " Reachability still checked at the TCP level."
	}
	return base
}

func nonHTTPBaseReason(portName, appProtocol string, port int32) string {
	if isGRPCLike(portName, appProtocol) {
		return "Port speaks gRPC or HTTP/2; the probe only knows HTTP/1.1."
	}
	if appProtocol != "" {
		return fmt.Sprintf("Port is declared as %q, not HTTP. The Kubernetes API path only carries HTTP traffic.", appProtocol)
	}
	if portName != "" {
		return fmt.Sprintf("Port named %q looks non-HTTP. The Kubernetes API path only carries HTTP traffic.", portName)
	}
	return fmt.Sprintf("Port %d is a well-known non-HTTP port. The Kubernetes API path only carries HTTP traffic.", port)
}

// portKey extracts the trailing port number from a probe target so
// divergence detection can pair "10.0.0.5:80" with "port 80" or
// "httpbin-abc port 8080" with "10.244.1.5:8080". Returns "" when no
// trailing integer is present.
func portKey(target string) string {
	// Try "...:N" first (IP-style targets).
	if i := strings.LastIndexByte(target, ':'); i >= 0 && i < len(target)-1 {
		rest := target[i+1:]
		if isAllDigits(rest) {
			return rest
		}
	}
	// Fall back to "...space N" (friendly "port N" / "name port N" targets).
	if i := strings.LastIndexByte(target, ' '); i >= 0 && i < len(target)-1 {
		rest := target[i+1:]
		if isAllDigits(rest) {
			return rest
		}
	}
	return ""
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isGRPCLike(name, appProtocol string) bool {
	switch strings.ToLower(strings.TrimSpace(appProtocol)) {
	case "grpc", "grpc-web", "h2", "h2c", "kubernetes.io/h2c":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "grpc", "grpc-web", "h2", "h2c":
		return true
	}
	return false
}

const maxPodsToProbe = 3

// probePods runs every feasible path against each sampled pod's container
// ports. In-cluster + PodIPs gets direct TCP (data path); a client + pod
// names gets PodProxy (apiserver path). Both run when both are feasible —
// divergence shows whether kube-proxy / NetworkPolicy is blocking pod-to-pod
// while the apiserver's proxy still reaches.
func probePods(ctx context.Context, h *Hop, vantage probe.Vantage, client kubernetes.Interface) []probe.Result {
	if h.Config == nil || len(h.Config.ContainerPorts) == 0 {
		return nil
	}
	dataReachable := vantage == probe.VantageInCluster && len(h.Config.PodIPs) > 0
	apiReachable := client != nil && len(h.Config.PodNames) > 0
	if !dataReachable && !apiReachable {
		return []probe.Result{probe.Skipped(probe.LayerTCP, "", vantage,
			"no ready pods identified for probing")}
	}
	var out []probe.Result
	if dataReachable {
		out = append(out, probePodsByIP(ctx, h, vantage)...)
	}
	if apiReachable {
		out = append(out, probePodsByName(ctx, h, vantage, client)...)
	}
	return out
}

func probePodsByIP(ctx context.Context, h *Hop, vantage probe.Vantage) []probe.Result {
	ips := h.Config.PodIPs
	if len(ips) > maxPodsToProbe {
		ips = ips[:maxPodsToProbe]
	}
	var out []probe.Result
	for _, ip := range ips {
		for _, cp := range h.Config.ContainerPorts {
			if ctx.Err() != nil {
				break
			}
			pctx, cancel := context.WithTimeout(ctx, tcpTimeout)
			r := probe.TCP(pctx, net.JoinHostPort(ip, strconv.Itoa(int(cp.Port))), vantage)
			r.Path = probe.PathData
			out = append(out, r)
			cancel()
		}
	}
	if len(h.Config.PodIPs) > len(ips) {
		out = append(out, probe.Skipped(probe.LayerTCP, "", vantage,
			fmt.Sprintf("sampled %d of %d ready pods", len(ips), len(h.Config.PodIPs))))
	}
	return out
}

func probePodsByName(ctx context.Context, h *Hop, vantage probe.Vantage, client kubernetes.Interface) []probe.Result {
	names := h.Config.PodNames
	if len(names) > maxPodsToProbe {
		names = names[:maxPodsToProbe]
	}
	var out []probe.Result
	for _, name := range names {
		for _, cp := range h.Config.ContainerPorts {
			if ctx.Err() != nil {
				break
			}
			// PodProxy is HTTP-only via the API server, same constraint as
			// ServiceProxy. Container ports don't carry appProtocol, so the
			// port name is the only signal for non-HTTP detection.
			target := fmt.Sprintf("%s port %d", name, cp.Port)
			if !isHTTPProbablePort(cp.Name, "", cp.Port) {
				skip := probe.Skipped(probe.LayerHTTP, target, vantage, nonHTTPSkipReason(cp.Name, "", cp.Port, vantage))
				skip.Path = probe.PathAPIServer
				out = append(out, skip)
				continue
			}
			pctx, cancel := context.WithTimeout(ctx, httpTimeout)
			out = append(out, podProxyProbe(pctx, client, h.Resource.Namespace, name, cp.Port, vantage))
			cancel()
		}
	}
	if len(h.Config.PodNames) > len(names) {
		out = append(out, probe.Skipped(probe.LayerHTTP, "", vantage,
			fmt.Sprintf("sampled %d of %d ready pods", len(names), len(h.Config.PodNames))))
	}
	return out
}

// probeIngress walks rules + spec hosts and runs the ladder against each
// host. The interesting failure mode is "DNS resolves but TCP doesn't
// connect" — that's a routing problem operators routinely chase by hand.
// Each host gets one DNS probe + one TCP+HTTP probe per port (80 + 443).
func probeIngress(ctx context.Context, h *Hop, vantage probe.Vantage) []probe.Result {
	if h.Config == nil {
		return nil
	}
	hosts := uniqueHosts(h.Config.Hostnames, h.Config.Rules)
	if len(hosts) == 0 {
		return []probe.Result{probe.Skipped(probe.LayerDNS, "", vantage,
			"Ingress declares no hostnames; reachability test would have no target")}
	}
	var out []probe.Result
	for _, host := range hosts {
		if ctx.Err() != nil {
			break
		}
		dctx, dcancel := context.WithTimeout(ctx, dnsTimeout)
		dnsRes := probe.DNS(dctx, host, vantage)
		dcancel()
		out = append(out, dnsRes)
		if !dnsRes.OK {
			continue
		}
		for _, port := range []int{80, 443} {
			if ctx.Err() != nil {
				break
			}
			addr := net.JoinHostPort(host, strconv.Itoa(port))
			tctx, tcancel := context.WithTimeout(ctx, tcpTimeout)
			tcpRes := probe.TCP(tctx, addr, vantage)
			tcancel()
			out = append(out, tcpRes)
			if !tcpRes.OK {
				continue
			}
			if port == 443 {
				lctx, lcancel := context.WithTimeout(ctx, tlsTimeout)
				out = append(out, probe.TLS(lctx, addr, host, vantage))
				lcancel()
			}
			hctx, hcancel := context.WithTimeout(ctx, httpTimeout)
			scheme := "http"
			if port == 443 {
				scheme = "https"
			}
			out = append(out, probe.HTTP(hctx, fmt.Sprintf("%s://%s/", scheme, host), host, vantage))
			hcancel()
		}
	}
	return out
}

// probeGateway probes each listener against the Gateway's status.addresses.
// A Gateway with no addresses (controller hasn't programmed it) yields a
// skip — the path is unreachable but that's already a critical static
// finding; the probe would just echo it.
func probeGateway(ctx context.Context, h *Hop, vantage probe.Vantage) []probe.Result {
	if h.Config == nil {
		return nil
	}
	if len(h.Config.Addresses) == 0 {
		return []probe.Result{probe.Skipped(probe.LayerTCP, "", vantage,
			"Gateway has no programmed addresses yet; static findings already cover this case")}
	}
	if len(h.Config.Listeners) == 0 {
		return nil
	}
	var out []probe.Result
	for _, addr := range h.Config.Addresses {
		for _, l := range h.Config.Listeners {
			if ctx.Err() != nil {
				break
			}
			target := net.JoinHostPort(addr, strconv.Itoa(int(l.Port)))
			tctx, tcancel := context.WithTimeout(ctx, tcpTimeout)
			tcpRes := probe.TCP(tctx, target, vantage)
			tcancel()
			out = append(out, tcpRes)
			if !tcpRes.OK {
				continue
			}
			isHTTPS := strings.EqualFold(l.Protocol, "HTTPS")
			if isHTTPS || strings.EqualFold(l.Protocol, "TLS") {
				lctx, lcancel := context.WithTimeout(ctx, tlsTimeout)
				out = append(out, probe.TLS(lctx, target, l.Hostname, vantage))
				lcancel()
			}
			// HTTP-level probe for HTTP/HTTPS listeners — TCP success
			// alone would let a Gateway whose controller returns 5xx
			// read as verified at the chip level, while probeIngress
			// on the same shape surfaces the failure. Dial the
			// Gateway's programmed address (the IP the TCP probe just
			// succeeded against) and pass the listener Hostname as the
			// Host header. Using the hostname in the URL would let
			// split-horizon DNS resolve to a different IP (e.g. a CDN
			// in front of the Gateway), masking cluster-side
			// misconfigurations.
			if isHTTPS || strings.EqualFold(l.Protocol, "HTTP") {
				scheme := "http"
				if isHTTPS {
					scheme = "https"
				}
				hctx, hcancel := context.WithTimeout(ctx, httpTimeout)
				url := fmt.Sprintf("%s://%s/", scheme, target)
				out = append(out, probe.HTTP(hctx, url, l.Hostname, vantage))
				hcancel()
			}
		}
	}
	return out
}

func uniqueHosts(declared []string, rules []RouteRule) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	for _, h := range declared {
		add(h)
	}
	for _, r := range rules {
		for _, h := range r.Hosts {
			add(h)
		}
	}
	return out
}
