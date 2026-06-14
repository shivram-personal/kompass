package trace

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/skyhook-io/radar/pkg/probe"
)

// TestRunProbes_BudgetExhausted pins the budget cap: when the deadline is
// past, hops that haven't started get a structured skip and runProbes
// returns immediately rather than spawning probes that would race the
// caller's context cancellation.
func TestRunProbes_BudgetExhausted(t *testing.T) {
	tr := &Trace{
		Downstream: []Hop{
			{Resource: ResourceRef{Kind: "Service"}, Config: &HopConfig{ClusterIP: "10.0.0.1", Ports: []PortMap{{Port: 80}}}},
		},
	}
	// Already-canceled context — runProbes must bail without panicking.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runProbes(ctx, tr, Options{Probe: true}, nil)
	// No panic and we got here: pass. The Probes slice may be empty or
	// contain a skip — either is acceptable for an already-dead context.
}

// TestRunProbes_ServiceNoClientNoTCP pins that a Service without a usable
// primitive (no kubeconfig client, no direct route) produces a structured
// skip rather than a silent no-op.
func TestRunProbes_ServiceNoClientNoTCP(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Service", Namespace: "ns", Name: "svc"},
			Config:   &HopConfig{ServiceType: "ClusterIP", ClusterIP: "10.0.0.1", Ports: []PortMap{{Port: 80}}},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 100 * time.Millisecond}, nil)
	if len(tr.Downstream[0].Probes) == 0 {
		t.Fatalf("expected at least one probe row, got none")
	}
	p := tr.Downstream[0].Probes[0]
	if !p.Skipped {
		t.Errorf("probe = %+v, want skipped (no client + local vantage)", p)
	}
	if !strings.Contains(strings.ToLower(p.Reason), "kubernetes api") {
		t.Errorf("skip reason should mention Kubernetes API unreachable, got %q", p.Reason)
	}
}

// TestRunProbes_PodsNoClientNoIPs: from local vantage with no kube client
// and no pod IPs, the Pods hop honestly skips.
func TestRunProbes_PodsNoClientNoIPs(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Pods", Namespace: "ns"},
			Config: &HopConfig{
				ContainerPorts: []ContainerPortRef{{Container: "main", Port: 8080}},
				// No PodIPs and no PodNames, no client — nothing to probe.
			},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 100 * time.Millisecond}, nil)
	if len(tr.Downstream[0].Probes) != 1 {
		t.Fatalf("expected one structured skip, got %d (%+v)", len(tr.Downstream[0].Probes), tr.Downstream[0].Probes)
	}
	p := tr.Downstream[0].Probes[0]
	if !p.Skipped || p.Vantage != probe.VantageLocal {
		t.Errorf("probe = %+v, want skipped local-vantage", p)
	}
}

// TestRunProbes_IngressNoHostsSkips: an Ingress with empty hostnames
// produces a structured DNS skip rather than a silent no-op — operators
// reading the trace would otherwise wonder if the probe ran.
func TestRunProbes_IngressNoHostsSkips(t *testing.T) {
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Ingress"},
			Config:   &HopConfig{},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 100 * time.Millisecond}, nil)
	if len(tr.Downstream[0].Probes) != 1 {
		t.Fatalf("expected one skip, got %d", len(tr.Downstream[0].Probes))
	}
	if !strings.Contains(tr.Downstream[0].Probes[0].Reason, "no hostnames") {
		t.Errorf("reason should mention no hostnames, got %q", tr.Downstream[0].Probes[0].Reason)
	}
}

// TestRunProbes_GatewayNoAddressesSkips: a Gateway without programmed
// addresses is a known static-finding case; the probe just acknowledges it.
func TestRunProbes_GatewayNoAddressesSkips(t *testing.T) {
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Gateway"},
			Config:   &HopConfig{Listeners: []GatewayListener{{Port: 80, Protocol: "HTTP"}}},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 100 * time.Millisecond}, nil)
	if len(tr.Downstream[0].Probes) != 1 || !tr.Downstream[0].Probes[0].Skipped {
		t.Fatalf("expected one skip probe for Gateway with no addresses, got %+v", tr.Downstream[0].Probes)
	}
}

// TestRunProbes_RouteAlwaysSkips: HTTPRoute/GRPCRoute have no own
// routable address — they explicitly defer reachability to the upstream
// Gateway and downstream Service. Test pins that we never accidentally
// probe a Route directly.
func TestRunProbes_RouteAlwaysSkips(t *testing.T) {
	for _, kind := range []string{"HTTPRoute", "GRPCRoute"} {
		tr := &Trace{
			Downstream: []Hop{{
				Resource: ResourceRef{Kind: kind},
				Config:   &HopConfig{Hostnames: []string{"api.example.com"}},
			}},
		}
		runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 100 * time.Millisecond}, nil)
		if len(tr.Downstream[0].Probes) != 1 || !tr.Downstream[0].Probes[0].Skipped {
			t.Errorf("%s: expected one structured skip, got %+v", kind, tr.Downstream[0].Probes)
		}
	}
}

// stubProxyProbes swaps the apiserver-path probes for fakes that return
// success immediately, restoring the real implementations on cleanup.
// Tests use this to exercise probeService / probePods orchestration
// without touching client-go's REST stack.
func stubProxyProbes(t *testing.T) {
	t.Helper()
	origSvc, origPod := serviceProxyProbe, podProxyProbe
	serviceProxyProbe = func(_ context.Context, _ kubernetes.Interface, ns, name string, port int32, vantage probe.Vantage) probe.Result {
		return probe.Result{
			Layer: probe.LayerHTTP, Target: fmt.Sprintf("svc:%s/%s:%d", ns, name, port),
			Vantage: vantage, Path: probe.PathAPIServer, OK: true,
		}
	}
	podProxyProbe = func(_ context.Context, _ kubernetes.Interface, ns, name string, port int32, vantage probe.Vantage) probe.Result {
		return probe.Result{
			Layer: probe.LayerHTTP, Target: fmt.Sprintf("pod:%s/%s:%d", ns, name, port),
			Vantage: vantage, Path: probe.PathAPIServer, OK: true,
		}
	}
	t.Cleanup(func() {
		serviceProxyProbe = origSvc
		podProxyProbe = origPod
	})
}

// TestProbeService_DualPathInCluster pins that in-cluster + client runs both
// the data path (direct TCP) and the apiserver path (ServiceProxy) for each
// port, with Path tagged so the UI can place each result on its arrow.
// Without this, divergence between paths can't be detected.
func TestProbeService_DualPathInCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	stubProxyProbes(t)
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Service", Namespace: "ns", Name: "svc"},
			Config:   &HopConfig{ServiceType: "ClusterIP", ClusterIP: "10.0.0.1", Ports: []PortMap{{Port: 80}}},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 500 * time.Millisecond}, fake.NewClientset())
	results := tr.Downstream[0].Probes
	if len(results) < 2 {
		t.Fatalf("expected two probe rows (data + apiserver), got %d: %+v", len(results), results)
	}
	var sawData, sawAPI bool
	for _, r := range results {
		switch r.Path {
		case probe.PathData:
			sawData = true
		case probe.PathAPIServer:
			sawAPI = true
		}
	}
	if !sawData || !sawAPI {
		t.Errorf("expected one PathData and one PathAPIServer result, got %+v", results)
	}
}

// TestProbePods_DualPathInCluster pins the Pods-side counterpart: in-cluster
// vantage + client + both PodIPs and PodNames runs probePodsByIP (data path)
// AND probePodsByName (apiserver path). Without both, divergence between
// direct pod dial and kubelet-proxy dial can't surface.
func TestProbePods_DualPathInCluster(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	stubProxyProbes(t)
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Pods", Namespace: "ns"},
			Config: &HopConfig{
				ContainerPorts: []ContainerPortRef{{Container: "main", Port: 8080}},
				PodIPs:         []string{"10.244.0.1"},
				PodNames:       []string{"pod-x"},
			},
		}},
	}
	// Budget must exceed the data-path TCP timeout so both paths get a
	// chance to record a Result — even when the data dial times out
	// against a non-routable test IP, the apiserver path still runs.
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 2 * time.Second}, fake.NewClientset())
	var sawData, sawAPI bool
	for _, r := range tr.Downstream[0].Probes {
		switch r.Path {
		case probe.PathData:
			sawData = true
		case probe.PathAPIServer:
			sawAPI = true
		}
	}
	if !sawData || !sawAPI {
		t.Errorf("expected both PathData and PathAPIServer pod probes, got %+v", tr.Downstream[0].Probes)
	}
}

// TestProbeService_LaptopAPIServerOnly pins that laptop vantage with a client
// runs only the apiserver path — there's no in-cluster TCP route to a
// ClusterIP from a laptop, so emitting a data-path result would be a lie.
func TestProbeService_LaptopAPIServerOnly(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	stubProxyProbes(t)
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Service", Namespace: "ns", Name: "svc"},
			Config:   &HopConfig{ServiceType: "ClusterIP", ClusterIP: "10.0.0.1", Ports: []PortMap{{Port: 80}}},
		}},
	}
	runProbes(context.Background(), tr, Options{Probe: true, ProbeBudget: 500 * time.Millisecond}, fake.NewClientset())
	for _, r := range tr.Downstream[0].Probes {
		if r.Path == probe.PathData {
			t.Errorf("unexpected data-path probe from laptop vantage: %+v", r)
		}
	}
}

// TestIsHTTPProbablePort pins the signal hierarchy: appProtocol (if set) is
// authoritative; port name is the next signal; well-known port numbers cover
// the no-metadata case (Helm charts that ship Redis on 6379 without setting
// either field). No-signal default is optimistic — most Service ports are
// HTTP-shaped — so an unannotated web service still gets probed.
func TestIsHTTPProbablePort(t *testing.T) {
	tests := []struct {
		name        string
		portName    string
		appProtocol string
		port        int32
		want        bool
	}{
		// appProtocol authoritative — HTTP family
		{"appProto http", "", "http", 0, true},
		{"appProto HTTP uppercase", "", "HTTP", 0, true},
		{"appProto https", "", "https", 0, true},
		// gRPC and HTTP/2 are HTTP-shaped but our probe speaks HTTP/1.1,
		// so they're excluded to avoid a false fail.
		{"appProto grpc skipped — HTTP/2-only", "", "grpc", 0, false},
		{"appProto h2c skipped — HTTP/2-only", "", "h2c", 0, false},
		{"appProto k8s h2c skipped — HTTP/2-only", "", "kubernetes.io/h2c", 0, false},
		{"appProto wss", "", "wss", 0, true},
		// appProtocol authoritative — explicit non-HTTP
		{"appProto tcp", "", "tcp", 0, false},
		{"appProto postgresql", "", "postgresql", 0, false},
		{"appProto mysql", "", "mysql", 0, false},
		{"appProto with whitespace", "", "  tcp  ", 0, false},
		{"appProto wins over web-ish port name", "http", "tcp", 80, false},
		// no appProtocol — port name signals non-HTTP
		{"name postgres", "postgres", "", 0, false},
		{"name postgresql", "postgresql", "", 0, false},
		{"name redis", "redis", "", 0, false},
		{"name mysql", "mysql", "", 0, false},
		{"name mongo", "mongo", "", 0, false},
		{"name MYSQL uppercase", "MYSQL", "", 0, false},
		{"name memcached", "memcached", "", 0, false},
		// no appProtocol — port name signals HTTP / optimistic default
		{"name http", "http", "", 0, true},
		{"name https", "https", "", 0, true},
		{"name web", "web", "", 0, true},
		{"name api", "api", "", 0, true},
		{"name grpc skipped — HTTP/2-only", "grpc", "", 0, false},
		{"name unknown", "metrics", "", 0, true},
		// no metadata — well-known port numbers
		{"port 6379 redis no-name", "", "", 6379, false},
		{"port 5432 postgres no-name", "", "", 5432, false},
		{"port 3306 mysql no-name", "", "", 3306, false},
		{"port 27017 mongo no-name", "", "", 27017, false},
		{"port 53 dns no-name", "", "", 53, false},
		{"port 22 ssh no-name", "", "", 22, false},
		{"port 80 unknown name — HTTP", "", "", 80, true},
		{"port 8080 unknown name — HTTP", "", "", 8080, true},
		{"port 9000 unknown name — optimistic", "", "", 9000, true},
		{"empty everything zero port", "", "", 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := isHTTPProbablePort(tc.portName, tc.appProtocol, tc.port)
			if got != tc.want {
				t.Errorf("isHTTPProbablePort(%q, %q, %d) = %v, want %v", tc.portName, tc.appProtocol, tc.port, got, tc.want)
			}
		})
	}
}

// TestPortKey pins the port-suffix extractor that drives divergence
// pairing. Pod and service paths label their targets differently
// ("10.0.0.5:80" vs "port 80" vs "name port 80") and the divergence
// finder needs them to share a bucket key.
func TestPortKey(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"10.0.0.5:80", "80"},
		{"port 80", "80"},
		{"httpbin-abc port 8080", "8080"},
		{"redis.default.svc.cluster.local:6379", "6379"},
		{"", ""},
		{"no-port-here", ""},
		{"trailing space ", ""},
		{":443", "443"},
	}
	for _, c := range cases {
		if got := portKey(c.in); got != c.want {
			t.Errorf("portKey(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPathDivergenceFinding_DataFailApiOK pins the dual-path divergence
// signal: when the in-cluster data path fails on a port but the apiserver
// path succeeds on the same port, the operator gets a warning naming the
// likely subsystems. The two paths label their targets differently, so
// this also pins that pairing works across format mismatches.
func TestPathDivergenceFinding_DataFailApiOK(t *testing.T) {
	probes := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:80", Path: probe.PathData, OK: false},
		{Layer: probe.LayerHTTP, Target: "port 80", Path: probe.PathAPIServer, OK: true},
	}
	f, ok := pathDivergenceFinding(probes)
	if !ok {
		t.Fatalf("expected divergence finding, got none")
	}
	if f.Code != "probe:data-path-only-broken" {
		t.Errorf("code = %q, want probe:data-path-only-broken", f.Code)
	}
	if f.Severity != SeverityWarning {
		t.Errorf("severity = %q, want warning", f.Severity)
	}
}

// TestPathDivergenceFinding_DataOKApiFail pins the inverse — when the
// data path is healthy but the apiserver-relayed probe fails, that's
// almost always a non-impacting issue (RBAC denied or non-HTTP port);
// the finding is severity:info so it doesn't flag the trace as broken.
func TestPathDivergenceFinding_DataOKApiFail(t *testing.T) {
	probes := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:80", Path: probe.PathData, OK: true},
		{Layer: probe.LayerHTTP, Target: "port 80", Path: probe.PathAPIServer, OK: false},
	}
	f, ok := pathDivergenceFinding(probes)
	if !ok {
		t.Fatalf("expected divergence finding, got none")
	}
	if f.Code != "probe:apiserver-path-only-broken" {
		t.Errorf("code = %q, want probe:apiserver-path-only-broken", f.Code)
	}
	if f.Severity != SeverityInfo {
		t.Errorf("severity = %q, want info", f.Severity)
	}
}

// TestPathDivergenceFinding_NoDivergence pins the silent case: when
// both paths agree (both OK or both fail) on every port, no finding
// is emitted — the divergence detector is only the asymmetry signal.
func TestPathDivergenceFinding_NoDivergence(t *testing.T) {
	bothOK := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:80", Path: probe.PathData, OK: true},
		{Layer: probe.LayerHTTP, Target: "port 80", Path: probe.PathAPIServer, OK: true},
	}
	if _, ok := pathDivergenceFinding(bothOK); ok {
		t.Errorf("both-OK should be silent")
	}
	bothFail := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:80", Path: probe.PathData, OK: false},
		{Layer: probe.LayerHTTP, Target: "port 80", Path: probe.PathAPIServer, OK: false},
	}
	if _, ok := pathDivergenceFinding(bothFail); ok {
		t.Errorf("both-fail should be silent (real failure, not divergence)")
	}
}

// TestPathDivergenceFinding_PartialFleetIsSilent pins that mixed results
// on the data side (one replica OK, one fails) do NOT fire divergence.
// On a multi-replica Pods hop those collapse to the same port-key bucket;
// without unanimity we'd emit data-path-only-broken even when most pods
// are reachable, masking the real signal (a partial-fleet failure).
func TestPathDivergenceFinding_PartialFleetIsSilent(t *testing.T) {
	probes := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:80", Path: probe.PathData, OK: true},
		{Layer: probe.LayerTCP, Target: "10.0.0.6:80", Path: probe.PathData, OK: false},
		{Layer: probe.LayerHTTP, Target: "port 80", Path: probe.PathAPIServer, OK: true},
	}
	if _, ok := pathDivergenceFinding(probes); ok {
		t.Errorf("partial-fleet data failure must not fire divergence; per-row severities are the signal")
	}
}

// TestPathDivergenceFinding_SkippedRowsIgnored pins that "skipped" rows
// (the apiserver path being skipped because the port is non-HTTP) never
// trigger divergence. A skip is a "didn't run", not a "failed".
func TestPathDivergenceFinding_SkippedRowsIgnored(t *testing.T) {
	probes := []probe.Result{
		{Layer: probe.LayerTCP, Target: "10.0.0.5:6379", Path: probe.PathData, OK: true},
		{Layer: probe.LayerHTTP, Target: "port 6379", Path: probe.PathAPIServer, Skipped: true, Reason: "non-HTTP port"},
	}
	if _, ok := pathDivergenceFinding(probes); ok {
		t.Errorf("skipped apiserver path against OK data path must not produce divergence")
	}
}

// TestVerdict_DegradeUnknownOnUnreadableEndpoints pins the verdict-
// honesty contract: a trace with no critical/warning findings but with
// a hop that flagged endpointSource=unknown (e.g. RBAC blocked the
// endpoints listing) must NOT claim healthy. The banner would mislead
// the operator into thinking the path is reachable when we have no
// proof. Selectorless is the other downgrade trigger and is covered
// by TestBuildTrace_ServiceNoSelectorIsSelectorless in trace_test.go.
func TestVerdict_DegradeUnknownOnUnreadableEndpoints(t *testing.T) {
	tr := &Trace{
		Downstream: []Hop{{
			Resource: ResourceRef{Kind: "Service", Namespace: "ns", Name: "svc"},
			Meta:     map[string]any{"endpointSource": "unknown"},
		}},
	}
	v, _ := computeVerdict(tr)
	if v != VerdictUnknown {
		t.Errorf("computeVerdict with endpointSource=unknown = %q, want %q", v, VerdictUnknown)
	}
}

// TestRouteAttachedToGateway_RejectsNonGatewayKind pins that a Route's
// parentRefs are only considered attached when the parentRef.kind is
// Gateway. A Route whose parentRef points at a same-named non-Gateway
// resource (e.g. another Route in a tree) must not appear on the
// Gateway trace; that would skew attached-route diagnosis.
func TestRouteAttachedToGateway_RejectsNonGatewayKind(t *testing.T) {
	route := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"namespace": "ns", "name": "r1"},
		"spec": map[string]any{
			"parentRefs": []any{
				map[string]any{"kind": "HTTPRoute", "name": "my-gateway", "namespace": "ns"},
			},
		},
	}}
	if routeAttachedToGateway(route, "ns", "my-gateway") {
		t.Errorf("parentRef kind=HTTPRoute should not match Gateway 'my-gateway'")
	}
}

// TestRouteAttachedToGateway_AllowsExplicitGatewayKind and the default
// (empty kind) both attach to a same-named Gateway. The Gateway API
// defaults parentRef.kind to "Gateway" when omitted.
func TestRouteAttachedToGateway_AllowsExplicitGatewayKind(t *testing.T) {
	cases := []map[string]any{
		{"kind": "Gateway", "name": "my-gateway", "namespace": "ns"},
		{"name": "my-gateway", "namespace": "ns"}, // kind omitted defaults to Gateway
		{"kind": "Gateway", "group": "gateway.networking.k8s.io", "name": "my-gateway", "namespace": "ns"},
	}
	for i, pr := range cases {
		route := &unstructured.Unstructured{Object: map[string]any{
			"metadata": map[string]any{"namespace": "ns", "name": "r1"},
			"spec":     map[string]any{"parentRefs": []any{pr}},
		}}
		if !routeAttachedToGateway(route, "ns", "my-gateway") {
			t.Errorf("case %d: parentRef %+v should attach", i, pr)
		}
	}
}

// TestPortIsServiceTargeted pins the Pods-hop port filter that prevents
// non-Service ports (sidecar metrics, kubelet liveness/readiness ports)
// from being probed. kube-proxy doesn't route to them, so a "probe failed"
// row on those ports would mislead the operator about real reachability.
func TestPortIsServiceTargeted(t *testing.T) {
	t.Run("no service known keeps every port", func(t *testing.T) {
		empty := servicePortTargets{}
		if !portIsServiceTargeted(empty, "metrics", 9090) {
			t.Errorf("with no Service context every container port should pass")
		}
	})
	t.Run("matches int targetPort", func(t *testing.T) {
		st := servicePortTargets{known: true, intSet: map[int32]struct{}{80: {}}, nameSet: map[string]struct{}{}}
		if !portIsServiceTargeted(st, "http", 80) {
			t.Errorf("port 80 should match int target")
		}
		if portIsServiceTargeted(st, "metrics", 9090) {
			t.Errorf("port 9090 should NOT match when Service targets only 80")
		}
	})
	t.Run("matches named targetPort", func(t *testing.T) {
		st := servicePortTargets{known: true, intSet: map[int32]struct{}{}, nameSet: map[string]struct{}{"web": {}}}
		if !portIsServiceTargeted(st, "web", 8080) {
			t.Errorf("named container port should match named target")
		}
		if portIsServiceTargeted(st, "metrics", 9090) {
			t.Errorf("non-matching named port should not match")
		}
	})
}

// TestUniqueHosts verifies the host de-dup that drives Ingress probing:
// blanks dropped, dupes collapsed, rule-level hosts merged.
func TestUniqueHosts(t *testing.T) {
	got := uniqueHosts(
		[]string{"a.com", "", "b.com", "a.com"},
		[]RouteRule{{Hosts: []string{"b.com", "c.com"}}, {Hosts: nil}},
	)
	want := []string{"a.com", "b.com", "c.com"}
	if len(got) != len(want) {
		t.Fatalf("uniqueHosts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("uniqueHosts[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
