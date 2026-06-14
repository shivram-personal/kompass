// Package trace composes a path-shaped diagnostic over a network entry kind
// (Service, Ingress, HTTPRoute, GRPCRoute, Gateway) — answering "if traffic
// is sent toward this resource, does it reach a healthy process, and if not
// which hop breaks first?"
//
// The output is a hop-ordered diagnosis, not a pile of alerts: findings are
// attached to the hop where the failure is observable, the first critical
// Downstream hop is named BrokenAt, and parallel Upstreams are judged
// independently so one broken Ingress does not condemn a Service the other
// Ingresses still deliver to.
//
// This package is deliberately the declared-axis only: pure functions over the
// informer cache, zero per-check API calls, zero RBAC asks, zero user
// configuration. Active probes, EndpointSlice on-demand reads, and
// NetworkPolicy rule evaluation are out of scope — they would force this
// feature out of the local-first zero-config envelope. NetworkPolicies that
// select the subject's pods are noted as an advisory only.
//
// Existing detection lives in internal/issues and internal/k8s/detect*.go;
// this package COMPOSES those findings into a path shape rather than running a
// parallel check engine.
package trace

import (
	"context"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/skyhook-io/radar/internal/issues"
	"github.com/skyhook-io/radar/internal/k8s"
	"github.com/skyhook-io/radar/pkg/probe"
)

const (
	VerdictHealthy  = "healthy"
	VerdictDegraded = "degraded"
	VerdictBroken   = "broken"
	VerdictUnknown  = "unknown"

	SeverityCritical = "critical"
	SeverityWarning  = "warning"
	SeverityInfo     = "info"

	gatewayRouteCap = 20
)

// Deps lets the trace builder share Radar's caches with the call site. The
// typed/unstructured split mirrors internal/k8s/detect_missing_refs.go:
// Services/Pods/Ingresses/NetworkPolicies come from typed listers; Gateway API
// kinds come from the dynamic cache as *unstructured.Unstructured.
type Deps struct {
	Cache     *k8s.ResourceCache
	Dynamic   *k8s.DynamicResourceCache
	Discovery *k8s.ResourceDiscovery
	Issues    *issues.CacheProvider
	// Client is the live K8s clientset used by reachability probes that go
	// through the API server proxy (Service/Pod /proxy). Optional — when
	// nil, probes fall back to direct TCP only.
	Client kubernetes.Interface
}

// ResourceRef points at a single Kubernetes object in the trace.
type ResourceRef struct {
	Group     string `json:"group,omitempty"`
	Kind      string `json:"kind"`
	Namespace string `json:"namespace,omitempty"`
	Name      string `json:"name"`
}

// Finding is one observation attached to a hop. Code is stable across runs so
// agents/UIs can compare; Severity matches the issues vocabulary
// (critical|warning|info). Command is a kubectl reproducer where derivable.
//
// Cause + Action carry parsed domain diagnosis when the issues pipeline
// classified the failure (CrashLoopBackOff exit codes, ImagePullBackOff
// registry/auth/not-found, PVC provisioning failures). When present, Cause
// is the plain-English root cause and Action is the next step the operator
// should take. Both empty for detectors without a parser — the UI falls back
// to Message + Remediation in that case.
type Finding struct {
	Code        string `json:"code"`
	Severity    string `json:"severity"`
	Message     string `json:"message"`
	Cause       string `json:"cause,omitempty"`
	Action      string `json:"action,omitempty"`
	Remediation string `json:"remediation,omitempty"`
	Command     string `json:"command,omitempty"`
}

// Hop is a single resource along the traffic path. Edge labels the relation
// from the previous resource ("HTTPRoute->Service"). Meta carries hop-shaped
// extras the UI may surface (pod counts, endpointSource, headless flag).
// Config carries the declared-config shape this hop contributes to the path
// (Service ports, container ports, route rules, gateway listeners) so the
// UI can display the wiring without re-fetching each resource.
type Hop struct {
	Resource ResourceRef    `json:"resource"`
	Edge     string         `json:"edge"`
	Findings []Finding      `json:"findings"`
	Meta     map[string]any `json:"meta,omitempty"`
	Config   *HopConfig     `json:"config,omitempty"`
	Probes   []probe.Result `json:"probes,omitempty"`
}

// HopConfig is the structured per-hop config snapshot. Every field is
// optional; only the fields relevant to the hop's resource kind are filled.
// The UI is expected to gracefully ignore fields it doesn't recognize.
type HopConfig struct {
	// Service hops
	Ports       []PortMap         `json:"ports,omitempty"`
	ServiceType string            `json:"serviceType,omitempty"`
	ClusterIP   string            `json:"clusterIP,omitempty"`
	Selector    map[string]string `json:"selector,omitempty"`

	// Pod-collection hops
	ContainerPorts []ContainerPortRef `json:"containerPorts,omitempty"`
	Probes         []ProbeRef         `json:"probes,omitempty"`
	// PodIPs lists the IPs of selected pods at trace time (capped to keep
	// the JSON bounded on large fleets). Drives the in-cluster TCP probe
	// against pod-level reachability; from a local vantage this is
	// informational only — operators reading the trace can see "these are
	// the pods that would receive traffic if the vantage were inside".
	PodIPs []string `json:"podIPs,omitempty"`
	// PodNames is the same set as PodIPs but by name — used by the API
	// server proxy probe (which addresses pods by name, not IP). Kept
	// separate from PodIPs because the in-cluster TCP path needs IPs and
	// the local kube-proxy path needs names.
	PodNames []string `json:"podNames,omitempty"`

	// Ingress / HTTPRoute / GRPCRoute hops
	Hostnames []string    `json:"hostnames,omitempty"`
	Rules     []RouteRule `json:"rules,omitempty"`

	// Gateway hops
	Listeners []GatewayListener `json:"listeners,omitempty"`
	Addresses []string          `json:"addresses,omitempty"`
}

// PortMap describes a Service port mapping: the Service-side port + the
// targetPort on the pod side (numeric or named). Protocol defaults to TCP
// when omitted by the Service spec. AppProtocol (when set) is the
// authoritative L7 hint — drives whether the laptop-vantage probe can
// reach it via the HTTP-only API server proxy.
type PortMap struct {
	Name        string `json:"name,omitempty"`
	Port        int32  `json:"port"`
	TargetPort  string `json:"targetPort,omitempty"`
	Protocol    string `json:"protocol,omitempty"`
	AppProtocol string `json:"appProtocol,omitempty"`
}

// ContainerPortRef carries a named or unnamed container port observed on at
// least one of the Service's selected pods. The Container field names the
// container so multi-container pods stay readable.
type ContainerPortRef struct {
	Container string `json:"container"`
	Name      string `json:"name,omitempty"`
	Port      int32  `json:"port"`
	Protocol  string `json:"protocol,omitempty"`
}

// ProbeRef describes a single readiness/liveness/startup probe declaration
// on a Service-selected pod's container. Port is the resolved numeric port
// (string form to keep named/numeric symmetry with k8s intstr).
type ProbeRef struct {
	Container string `json:"container"`
	Type      string `json:"type"`
	Port      string `json:"port,omitempty"`
	Path      string `json:"path,omitempty"`
	Scheme    string `json:"scheme,omitempty"`
}

// RouteRule summarizes one rule on an Ingress or Gateway-API route: which
// hostnames/paths it matches and which backends it points at. The shape is
// deliberately union-compatible across Ingress and Gateway API so the UI can
// render one row per backend.
type RouteRule struct {
	Hosts    []string     `json:"hosts,omitempty"`
	Paths    []string     `json:"paths,omitempty"`
	Backends []BackendRef `json:"backends,omitempty"`
}

// BackendRef points at a route's destination — almost always a Service in
// scope, but Kind is included so the UI can show non-Service backends as
// "out of scope" without misrendering them as Services.
type BackendRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
	Port      string `json:"port,omitempty"`
}

// GatewayListener describes one listener on a Gateway: the port and
// protocol traffic enters on, plus the listener's name for cross-reference
// with route parentRefs.sectionName.
type GatewayListener struct {
	Name     string `json:"name,omitempty"`
	Port     int32  `json:"port"`
	Protocol string `json:"protocol,omitempty"`
	Hostname string `json:"hostname,omitempty"`
}

// Trace is the diagnosis. Downstream is the chain FROM the subject TOWARD
// pods; this is where BrokenAt applies. Upstreams are the parallel hops INTO
// the subject (e.g. multiple Ingresses pointing at one Service) — they are
// judged independently because a single broken Ingress does not condemn the
// other delivery paths.
type Trace struct {
	Subject    ResourceRef `json:"subject"`
	Upstreams  []Hop       `json:"upstreams"`
	Downstream []Hop       `json:"downstream"`
	Verdict    string      `json:"verdict"`
	BrokenAt   int         `json:"brokenAt"`
	// Reason explains an unknown/degraded verdict in a single sentence; the
	// UI shows it under the verdict banner without expansion.
	Reason string `json:"reason,omitempty"`
	// Truncated is set when fan-out (e.g. Gateway attached routes) exceeded
	// the cap, so the UI can surface "showing N of M".
	Truncated bool `json:"truncated,omitempty"`
}

// Options shape what BuildTrace does beyond the static walk. Probe is opt-in
// because active checks cost wall time and generate traffic external systems
// can see — repeating them every poll cycle would create observability noise.
// Defaults are the cheapest sensible answer: static only.
type Options struct {
	// Probe controls active reachability checks. When true, BuildTrace
	// attaches probe.Result entries to each hop, labeled with the vantage
	// the binary is running from.
	Probe bool
	// ProbeBudget caps total wall time across all probes in this trace.
	// Zero means "use the package default" (3 seconds).
	ProbeBudget time.Duration
}

// BuildTraceWithOptions dispatches by entry kind. Kinds outside the accepted
// set return an empty trace with VerdictUnknown — the caller should validate
// input before reaching here; the guard exists only to avoid panics on
// frontend/MCP misuse. Callers that want a static-only trace pass Options{}.
func BuildTraceWithOptions(ctx context.Context, deps Deps, kind, namespace, name string, opts Options) (*Trace, error) {
	subject := ResourceRef{Kind: kind, Namespace: namespace, Name: name}

	if !cacheReady(deps) {
		return &Trace{
			Subject:    subject,
			Downstream: []Hop{},
			Upstreams:  []Hop{},
			Verdict:    VerdictUnknown,
			BrokenAt:   -1,
			Reason:     "cache syncing — initial informer sync has not completed yet",
		}, nil
	}

	t := &Trace{Subject: subject, BrokenAt: -1}

	switch normalizeKind(kind) {
	case "Service":
		traceServiceEntry(deps, t)
	case "Ingress":
		traceIngressEntry(deps, t)
	case "HTTPRoute":
		traceRouteEntry(deps, t, "HTTPRoute")
	case "GRPCRoute":
		traceRouteEntry(deps, t, "GRPCRoute")
	case "Gateway":
		traceGatewayEntry(deps, t)
	default:
		t.Downstream = []Hop{}
		t.Upstreams = []Hop{}
		t.Verdict = VerdictUnknown
		t.Reason = "trace not supported for kind " + kind
		return t, nil
	}

	if t.Downstream == nil {
		t.Downstream = []Hop{}
	}
	if t.Upstreams == nil {
		t.Upstreams = []Hop{}
	}
	normalizeHopFindings(t)
	// Entry handlers may set Verdict directly (NotFound, RBAC denied,
	// missing API). Treat that as authoritative — computing "healthy" over
	// an empty Downstream after a failed resource lookup would be a lie.
	if t.Verdict == "" {
		t.Verdict, t.BrokenAt = computeVerdict(t)
		if t.Verdict == VerdictUnknown && t.Reason == "" {
			t.Reason = unknownReason(t)
		}
	}

	// Probes augment the static verdict, never override it: a successful
	// probe against a static-broken trace could be an external bystander;
	// a failed probe against a static-healthy trace could be the vantage
	// rather than the path. Both belong as informational rows under each
	// hop. UI decides whether to surface the action button by inspecting
	// each HopConfig directly.
	if opts.Probe {
		runProbes(ctx, t, opts, deps.Client)
	}
	return t, nil
}

// normalizeHopFindings enforces two wire-format invariants before the trace
// leaves the package: Hop.Findings is always a non-nil slice (Go would
// marshal nil as JSON null, which the TS panel iterates as a crash), and
// findings on every hop are sorted worst-severity-first so consumers
// don't have to re-order.
func normalizeHopFindings(t *Trace) {
	for i := range t.Downstream {
		if t.Downstream[i].Findings == nil {
			t.Downstream[i].Findings = []Finding{}
		}
		sortFindingsBySeverity(t.Downstream[i].Findings)
	}
	for i := range t.Upstreams {
		if t.Upstreams[i].Findings == nil {
			t.Upstreams[i].Findings = []Finding{}
		}
		sortFindingsBySeverity(t.Upstreams[i].Findings)
	}
}

// computeVerdict walks Downstream first (BrokenAt indexes Downstream only),
// then folds Upstreams: an all-broken upstream set is a system-level break;
// a mixed set is only degraded because other parallel hops can still deliver.
func computeVerdict(t *Trace) (string, int) {
	verdict := VerdictHealthy
	brokenAt := -1
	for i, hop := range t.Downstream {
		s := worstSeverity(hop.Findings)
		switch s {
		case SeverityCritical:
			if brokenAt < 0 {
				brokenAt = i
			}
			verdict = VerdictBroken
		case SeverityWarning:
			if verdict == VerdictHealthy {
				verdict = VerdictDegraded
			}
		}
	}

	if len(t.Upstreams) > 0 {
		brokenUp := 0
		warnUp := 0
		for _, hop := range t.Upstreams {
			switch worstSeverity(hop.Findings) {
			case SeverityCritical:
				brokenUp++
			case SeverityWarning:
				warnUp++
			}
		}
		if brokenUp == len(t.Upstreams) && verdict != VerdictBroken {
			verdict = VerdictBroken
			// All upstream entries are broken: traffic can't reach the
			// subject at all. Anchor brokenAt on the subject row (index 0)
			// so the UI's downstream highlight isn't blank while the banner
			// reads "broken". The subject row IS the unreachable target.
			if brokenAt < 0 && len(t.Downstream) > 0 {
				brokenAt = 0
			}
		} else if (brokenUp > 0 || warnUp > 0) && verdict == VerdictHealthy {
			verdict = VerdictDegraded
		}
	}

	// A trace whose downstream is a selectorless Service can't honestly be
	// called healthy: endpoints are managed manually, so we have no proof
	// the path resolves to a real target. Degrade to unknown so the banner
	// doesn't overclaim.
	if verdict == VerdictHealthy && hasSelectorlessHop(t) {
		verdict = VerdictUnknown
	}

	return verdict, brokenAt
}

func hasSelectorlessHop(t *Trace) bool {
	for _, hop := range t.Downstream {
		if hop.Meta == nil {
			continue
		}
		if v, _ := hop.Meta["selectorless"].(bool); v {
			return true
		}
	}
	return false
}

// unknownReason names the dominant cause for an unknown verdict so the
// banner can be specific instead of "we don't know".
func unknownReason(t *Trace) string {
	for _, hop := range t.Downstream {
		if hop.Meta == nil {
			continue
		}
		if v, _ := hop.Meta["selectorless"].(bool); v {
			return "Service has no selector. Endpoints are managed manually, so we can't confirm a target is reachable."
		}
	}
	return "Path can't be verified from declared config alone."
}

func worstSeverity(fs []Finding) string {
	worst := ""
	for _, f := range fs {
		switch f.Severity {
		case SeverityCritical:
			return SeverityCritical
		case SeverityWarning:
			worst = SeverityWarning
		case SeverityInfo:
			if worst == "" {
				worst = SeverityInfo
			}
		}
	}
	return worst
}

func cacheReady(deps Deps) bool {
	if deps.Cache == nil {
		return false
	}
	// Listers return nil when their informer hasn't synced yet; the Services
	// lister is the cheapest readiness probe for trace work since every entry
	// kind needs it.
	if deps.Cache.Services() == nil {
		return false
	}
	return true
}

// IsEntryKind returns true if the given kind (any plural/lowercase form) is
// one of the network entry kinds the trace surface supports. Used by the
// server's input gate and the trace dispatcher; keeping them aligned on one
// helper prevents the "added a kind in one place, forgot the other" bug.
func IsEntryKind(kind string) bool {
	switch normalizeKind(kind) {
	case "Service", "Ingress", "HTTPRoute", "GRPCRoute", "Gateway":
		return true
	}
	return false
}

func normalizeKind(k string) string {
	switch k {
	case "service", "Service", "services":
		return "Service"
	case "ingress", "Ingress", "ingresses":
		return "Ingress"
	case "httproute", "HTTPRoute", "httproutes":
		return "HTTPRoute"
	case "grpcroute", "GRPCRoute", "grpcroutes":
		return "GRPCRoute"
	case "gateway", "Gateway", "gateways":
		return "Gateway"
	}
	return k
}
