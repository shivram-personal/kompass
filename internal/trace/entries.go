package trace

import (
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

type networkingIngress = networkingv1.Ingress

// traceServiceEntry handles the most common entry kind: a Service. The
// downstream chain is the Service itself + the selected-pod hop; upstreams
// are every Ingress and Gateway-API Route referencing this Service, judged
// independently because parallel ingress paths fail independently.
func traceServiceEntry(deps Deps, t *Trace) {
	svc, err := deps.Cache.Services().Services(t.Subject.Namespace).Get(t.Subject.Name)
	if err != nil {
		t.Verdict = VerdictUnknown
		t.Reason = serviceLookupReason(err, t.Subject)
		return
	}
	svcRef := refForService(svc)
	t.Subject = svcRef

	switch {
	case svc.Spec.Type == corev1.ServiceTypeExternalName:
		t.Downstream = []Hop{{
			Resource: svcRef,
			Edge:     "entry:Service",
			Findings: hopFindings(deps.Issues, svcRef),
		}, {
			Resource: ResourceRef{Kind: "ExternalName", Name: svc.Spec.ExternalName},
			Edge:     "Service->ExternalName",
			Findings: []Finding{{
				Code:     "svc:external-name",
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("Service resolves to %q outside the cluster; downstream is not traced", svc.Spec.ExternalName),
			}},
		}}
		t.Upstreams = serviceUpstreams(deps, svc)
		return

	case len(svc.Spec.Selector) == 0:
		t.Downstream = []Hop{{
			Resource: svcRef,
			Edge:     "entry:Service",
			Meta:     map[string]any{"selectorless": true},
			Findings: append(hopFindings(deps.Issues, svcRef), Finding{
				Code:     "svc:selectorless",
				Severity: SeverityInfo,
				Message:  "Service has no selector; endpoints are managed manually and pod health is not derivable",
				Command:  fmt.Sprintf("kubectl get endpoints %s -n %s", svc.Name, svc.Namespace),
			}),
		}}
		t.Upstreams = serviceUpstreams(deps, svc)
		return
	}

	svcHop := Hop{
		Resource: svcRef,
		Edge:     "entry:Service",
		Findings: hopFindings(deps.Issues, svcRef),
		Meta:     map[string]any{},
		Config:   serviceConfig(svc),
	}
	if svc.Spec.ClusterIP == "None" {
		svcHop.Meta["headless"] = true
	}

	pods := selectedPods(deps, svc)
	podsHop := buildPodsHop(deps, svc, pods)

	t.Downstream = []Hop{svcHop, podsHop}
	t.Upstreams = serviceUpstreams(deps, svc)
}

// serviceConfig captures the wire-shape of a Service: which port mapping
// the Service declares, the type (ClusterIP / NodePort / LoadBalancer /
// ExternalName), and the selector. This is the data an operator needs to
// reason about "what port does traffic actually target" without leaving
// the trace.
func serviceConfig(svc *corev1.Service) *HopConfig {
	if svc == nil {
		return nil
	}
	c := &HopConfig{
		ServiceType: string(svc.Spec.Type),
		ClusterIP:   svc.Spec.ClusterIP,
	}
	for _, p := range svc.Spec.Ports {
		tp := ""
		switch p.TargetPort.Type {
		case intstr.Int:
			if p.TargetPort.IntVal > 0 {
				tp = fmt.Sprintf("%d", p.TargetPort.IntVal)
			}
		case intstr.String:
			tp = p.TargetPort.StrVal
		}
		if tp == "" {
			tp = fmt.Sprintf("%d", p.Port)
		}
		appProto := ""
		if p.AppProtocol != nil {
			appProto = *p.AppProtocol
		}
		c.Ports = append(c.Ports, PortMap{
			Name:        p.Name,
			Port:        p.Port,
			TargetPort:  tp,
			Protocol:    string(p.Protocol),
			AppProtocol: appProto,
		})
	}
	if len(svc.Spec.Selector) > 0 {
		c.Selector = map[string]string{}
		for k, v := range svc.Spec.Selector {
			c.Selector[k] = v
		}
	}
	return c
}

func buildPodsHop(deps Deps, svc *corev1.Service, pods []*corev1.Pod) Hop {
	podsRef := ResourceRef{Kind: "Pods", Namespace: svc.Namespace}
	meta := map[string]any{
		"endpointSource": "pod-readiness",
		"selected":       len(pods),
		"ready":          readyCount(pods),
	}
	hop := Hop{
		Resource: podsRef,
		Edge:     "Service->Pods",
		Meta:     meta,
		Config:   podsConfig(pods, svc),
	}
	hop.Findings = append(hop.Findings, podFanoutFindings(deps, pods)...)
	if cmd := selectorReproducer(podsRef, svc.Spec.Selector); cmd != "" {
		for i := range hop.Findings {
			if hop.Findings[i].Command == "" {
				hop.Findings[i].Command = cmd
			}
		}
	}
	if advisory, ok := networkPolicyAdvisory(deps, svc, pods); ok {
		hop.Findings = append(hop.Findings, advisory)
	}
	return hop
}

const maxPodIPsInConfig = 10

// podsConfig folds the selected pods into a deduplicated container-port +
// probe summary. We dedupe on (container, name, port) so a 50-replica
// Deployment doesn't produce 50 identical rows — every replica declares the
// same ports. Probes from sidecars matter (multi-container readiness is
// AND'ed) so they're listed per container. Pod IPs are captured up to
// maxPodIPsInConfig (10) so the in-cluster probe has real targets.
//
// Container ports are filtered to those the upstream Service actually
// targets. Sidecar / admin / metrics ports (Envoy 15000, Prometheus 9090,
// kubelet probe ports) aren't reachable via kube-proxy, so probing them
// would emit false "probe failed" rows that mislead the operator about
// the actual data-path. When svc has no ports declared, every container
// port stays so we don't blank-screen the hop.
func podsConfig(pods []*corev1.Pod, svc *corev1.Service) *HopConfig {
	if len(pods) == 0 {
		return nil
	}
	targeted := serviceTargetedPorts(svc)
	cp := map[string]ContainerPortRef{}
	pr := map[string]ProbeRef{}
	var ips []string
	var names []string
	for _, pod := range pods {
		if pod == nil {
			continue
		}
		if isPodReadyForTrace(pod) && len(names) < maxPodIPsInConfig {
			if pod.Status.PodIP != "" {
				ips = append(ips, pod.Status.PodIP)
			}
			names = append(names, pod.Name)
		}
		for _, c := range pod.Spec.Containers {
			for _, port := range c.Ports {
				if !portIsServiceTargeted(targeted, port.Name, port.ContainerPort) {
					continue
				}
				key := c.Name + "/" + port.Name + "/" + fmt.Sprintf("%d", port.ContainerPort)
				if _, ok := cp[key]; ok {
					continue
				}
				cp[key] = ContainerPortRef{
					Container: c.Name,
					Name:      port.Name,
					Port:      port.ContainerPort,
					Protocol:  string(port.Protocol),
				}
			}
			if ref, ok := describeProbe(c.Name, "readiness", c.ReadinessProbe, c.Ports); ok {
				pr[c.Name+"/readiness"] = ref
			}
			if ref, ok := describeProbe(c.Name, "liveness", c.LivenessProbe, c.Ports); ok {
				pr[c.Name+"/liveness"] = ref
			}
		}
	}
	if len(cp) == 0 && len(pr) == 0 && len(ips) == 0 && len(names) == 0 {
		return nil
	}
	c := &HopConfig{}
	c.PodIPs = ips
	c.PodNames = names
	for _, v := range cp {
		c.ContainerPorts = append(c.ContainerPorts, v)
	}
	sort.Slice(c.ContainerPorts, func(i, j int) bool {
		if c.ContainerPorts[i].Container != c.ContainerPorts[j].Container {
			return c.ContainerPorts[i].Container < c.ContainerPorts[j].Container
		}
		return c.ContainerPorts[i].Port < c.ContainerPorts[j].Port
	})
	for _, v := range pr {
		c.Probes = append(c.Probes, v)
	}
	sort.Slice(c.Probes, func(i, j int) bool {
		if c.Probes[i].Container != c.Probes[j].Container {
			return c.Probes[i].Container < c.Probes[j].Container
		}
		return c.Probes[i].Type < c.Probes[j].Type
	})
	return c
}

// servicePortTargets captures the union of ports a Service routes to. The
// apiserver treats targetPort as int OR named-string; pods carry both
// (containerPort and Name). We surface both so portIsServiceTargeted can
// match against either. Empty == no Service known, in which case we
// keep all ports (Pods hops attached to non-Service entries).
type servicePortTargets struct {
	known   bool
	intSet  map[int32]struct{}
	nameSet map[string]struct{}
}

func serviceTargetedPorts(svc *corev1.Service) servicePortTargets {
	if svc == nil || len(svc.Spec.Ports) == 0 {
		return servicePortTargets{}
	}
	t := servicePortTargets{
		known:   true,
		intSet:  map[int32]struct{}{},
		nameSet: map[string]struct{}{},
	}
	for _, sp := range svc.Spec.Ports {
		switch sp.TargetPort.Type {
		case intstr.Int:
			if sp.TargetPort.IntVal > 0 {
				t.intSet[sp.TargetPort.IntVal] = struct{}{}
			} else {
				// Unset TargetPort defaults to Port (apiserver behavior).
				t.intSet[sp.Port] = struct{}{}
			}
		case intstr.String:
			if sp.TargetPort.StrVal != "" {
				t.nameSet[sp.TargetPort.StrVal] = struct{}{}
			} else {
				t.intSet[sp.Port] = struct{}{}
			}
		}
	}
	return t
}

func portIsServiceTargeted(t servicePortTargets, name string, port int32) bool {
	if !t.known {
		return true
	}
	if _, ok := t.intSet[port]; ok {
		return true
	}
	if name != "" {
		if _, ok := t.nameSet[name]; ok {
			return true
		}
	}
	return false
}

// isPodReadyForTrace returns true if the pod's Ready condition is True.
// Only ready pods are surfaced as probe targets — probing a NotReady pod
// reports "TCP connect failed", but kube-proxy was already routing around
// it, so the trace would be reporting a non-issue.
func isPodReadyForTrace(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func describeProbe(container, kind string, probe *corev1.Probe, ports []corev1.ContainerPort) (ProbeRef, bool) {
	if probe == nil {
		return ProbeRef{}, false
	}
	ref := ProbeRef{Container: container, Type: kind}
	resolvePort := func(p intstr.IntOrString) string {
		switch p.Type {
		case intstr.Int:
			if p.IntVal > 0 {
				return fmt.Sprintf("%d", p.IntVal)
			}
		case intstr.String:
			for _, cp := range ports {
				if cp.Name == p.StrVal && cp.ContainerPort > 0 {
					return fmt.Sprintf("%s→%d", p.StrVal, cp.ContainerPort)
				}
			}
			if p.StrVal != "" {
				return p.StrVal + "(unresolved)"
			}
		}
		return ""
	}
	switch {
	case probe.HTTPGet != nil:
		ref.Port = resolvePort(probe.HTTPGet.Port)
		ref.Path = probe.HTTPGet.Path
		ref.Scheme = string(probe.HTTPGet.Scheme)
	case probe.TCPSocket != nil:
		ref.Port = resolvePort(probe.TCPSocket.Port)
		ref.Scheme = "TCP"
	case probe.GRPC != nil:
		if probe.GRPC.Port > 0 {
			ref.Port = fmt.Sprintf("%d", probe.GRPC.Port)
		}
		ref.Scheme = "gRPC"
	case probe.Exec != nil:
		ref.Scheme = "exec"
	default:
		return ProbeRef{}, false
	}
	return ref, true
}

// podFanoutFindings collapses the per-pod issue fan-out into hop-level
// findings. We deliberately surface the worst-severity representative per
// distinct code instead of every pod's copy — the diagnosis is "this hop is
// broken", not "here are 47 identical alerts".
func podFanoutFindings(deps Deps, pods []*corev1.Pod) []Finding {
	if deps.Issues == nil {
		return nil
	}
	seen := map[string]Finding{}
	for _, pod := range pods {
		if pod == nil {
			continue
		}
		ref := ResourceRef{Kind: "Pod", Namespace: pod.Namespace, Name: pod.Name}
		for _, f := range hopFindings(deps.Issues, ref) {
			prev, ok := seen[f.Code]
			if !ok || severityRank(f.Severity) > severityRank(prev.Severity) {
				seen[f.Code] = f
			}
		}
	}
	out := make([]Finding, 0, len(seen))
	for _, f := range seen {
		out = append(out, f)
	}
	return out
}

func severityRank(s string) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	}
	return 0
}

func selectedPods(deps Deps, svc *corev1.Service) []*corev1.Pod {
	if deps.Cache == nil || deps.Cache.Pods() == nil || svc == nil || len(svc.Spec.Selector) == 0 {
		return nil
	}
	pods, err := deps.Cache.Pods().Pods(svc.Namespace).List(labels.SelectorFromSet(labels.Set(svc.Spec.Selector)))
	if err != nil {
		return nil
	}
	return pods
}

func readyCount(pods []*corev1.Pod) int {
	n := 0
	for _, pod := range pods {
		if pod == nil {
			continue
		}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
				n++
				break
			}
		}
	}
	return n
}

// serviceUpstreams reverse-walks Ingresses and Gateway-API Routes pointing at
// the Service. Each upstream is one Hop with its own findings; we don't
// chain through to the route's own pods (that's the route's own trace).
func serviceUpstreams(deps Deps, svc *corev1.Service) []Hop {
	var out []Hop
	out = append(out, ingressUpstreamsForService(deps, svc)...)
	out = append(out, routeUpstreamsForService(deps, svc, "HTTPRoute", "httproutes")...)
	out = append(out, routeUpstreamsForService(deps, svc, "GRPCRoute", "grpcroutes")...)
	return out
}

func ingressUpstreamsForService(deps Deps, svc *corev1.Service) []Hop {
	if deps.Cache == nil || deps.Cache.Ingresses() == nil {
		return nil
	}
	ingresses, err := deps.Cache.Ingresses().Ingresses(svc.Namespace).List(labels.Everything())
	if err != nil {
		return nil
	}
	var out []Hop
	for _, ing := range ingresses {
		if !ingressReferencesService(ing, svc.Name) {
			continue
		}
		ref := ResourceRef{Group: "networking.k8s.io", Kind: "Ingress", Namespace: ing.Namespace, Name: ing.Name}
		out = append(out, Hop{
			Resource: ref,
			Edge:     "Ingress->Service",
			Findings: hopFindings(deps.Issues, ref),
		})
	}
	return out
}

func ingressReferencesService(ing *networkingIngress, svcName string) bool {
	if ing == nil {
		return false
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil && ing.Spec.DefaultBackend.Service.Name == svcName {
		return true
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			if p.Backend.Service != nil && p.Backend.Service.Name == svcName {
				return true
			}
		}
	}
	return false
}

func routeUpstreamsForService(deps Deps, svc *corev1.Service, kind, resourceName string) []Hop {
	gvr, ok := deps.Discovery.GetGVRWithGroup(kind, "gateway.networking.k8s.io")
	if !ok {
		return nil
	}
	routes, err := deps.Dynamic.ListWatched(gvr)
	if err != nil || len(routes) == 0 {
		// Some clusters only have the GVR namespaced; fall back to the
		// service's namespace before giving up so common configurations
		// still produce upstream hops.
		routes2, _ := deps.Dynamic.List(gvr, svc.Namespace)
		routes = routes2
	}
	var out []Hop
	for _, route := range routes {
		if !routeReferencesService(route, svc.Namespace, svc.Name) {
			continue
		}
		ref := ResourceRef{Group: "gateway.networking.k8s.io", Kind: kind, Namespace: route.GetNamespace(), Name: route.GetName()}
		out = append(out, Hop{
			Resource: ref,
			Edge:     kind + "->Service",
			Findings: hopFindings(deps.Issues, ref),
		})
	}
	return out
}

func routeReferencesService(route *unstructured.Unstructured, svcNS, svcName string) bool {
	rules, found, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	if !found {
		return false
	}
	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		refs, _ := rm["backendRefs"].([]any)
		for _, ref := range refs {
			refm, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			group, _ := refm["group"].(string)
			kindf, _ := refm["kind"].(string)
			if group != "" || (kindf != "" && kindf != "Service") {
				continue
			}
			name, _ := refm["name"].(string)
			ns, _ := refm["namespace"].(string)
			if ns == "" {
				ns = route.GetNamespace()
			}
			if name == svcName && ns == svcNS {
				return true
			}
		}
	}
	return false
}

// traceIngressEntry: Ingress is the external entry point; no upstreams. We
// walk every unique backend Service the rules name and emit a Service +
// Pods hop pair for each one. Without that, a multi-backend Ingress where
// backend B is broken but backend A is healthy would look healthy — the
// trace would only show A's pods. Capped at maxBackends to bound fan-out;
// Truncated is set when the cap kicks in.
//
// Verdict semantics for multi-backend paths: any critical hop trips the
// verdict to broken. That slightly overstates severity for the
// "some backends broken, others healthy" case (degraded would be more
// precise), but the operator still sees every backend named in the trace
// with its own findings.
func traceIngressEntry(deps Deps, t *Trace) {
	ingLister := deps.Cache.Ingresses()
	if ingLister == nil {
		t.Verdict = VerdictUnknown
		t.Reason = "Ingress lister not ready"
		return
	}
	ing, err := ingLister.Ingresses(t.Subject.Namespace).Get(t.Subject.Name)
	if err != nil {
		t.Verdict = VerdictUnknown
		t.Reason = serviceLookupReason(err, t.Subject)
		return
	}
	ingRef := ResourceRef{Group: "networking.k8s.io", Kind: "Ingress", Namespace: ing.Namespace, Name: ing.Name}
	t.Subject = ingRef

	hops := []Hop{{
		Resource: ingRef,
		Edge:     "entry:Ingress",
		Findings: hopFindings(deps.Issues, ingRef),
		Config:   ingressConfig(ing),
	}}

	backends := ingressBackendNames(ing)
	if len(backends) > maxBackendsTraced {
		backends = backends[:maxBackendsTraced]
		t.Truncated = true
	}
	for _, name := range backends {
		svcRef := ResourceRef{Kind: "Service", Namespace: ing.Namespace, Name: name}
		svcHop := Hop{
			Resource: svcRef,
			Edge:     "Ingress->Service",
			Findings: hopFindings(deps.Issues, svcRef),
		}
		svc, svcErr := deps.Cache.Services().Services(ing.Namespace).Get(name)
		if svcErr == nil {
			svcHop.Config = serviceConfig(svc)
		}
		hops = append(hops, svcHop)
		if svcErr == nil && len(svc.Spec.Selector) > 0 {
			hops = append(hops, buildPodsHop(deps, svc, selectedPods(deps, svc)))
		}
	}
	t.Downstream = hops
}

// maxBackendsTraced caps the per-backend Service+Pods fan-out for Ingress
// and Route subjects. Real-world Ingresses with more than a handful of
// backends are rare; the cap bounds response size + selector-match work.
// Truncated is set on the Trace when this kicks in so the UI surfaces
// "showing N of M".
const maxBackendsTraced = 5

// ingressConfig captures the spec.rules[] shape (hosts + paths + backend
// service+port) so the trace's Ingress hop names the URLs and ports it
// actually owns. Hostnames are flattened to a top-level field for fast
// scanning — operators usually ask "which hosts does this Ingress serve"
// before they ask "for each host, what paths".
func ingressConfig(ing *networkingv1.Ingress) *HopConfig {
	if ing == nil {
		return nil
	}
	c := &HopConfig{}
	hostsSeen := map[string]bool{}
	addHost := func(h string) {
		if h == "" || hostsSeen[h] {
			return
		}
		hostsSeen[h] = true
		c.Hostnames = append(c.Hostnames, h)
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		c.Rules = append(c.Rules, RouteRule{
			Paths: []string{"(default backend)"},
			Backends: []BackendRef{{
				Kind: "Service",
				Name: ing.Spec.DefaultBackend.Service.Name,
				Port: ingressBackendPortString(ing.Spec.DefaultBackend.Service.Port),
			}},
		})
	}
	for _, rule := range ing.Spec.Rules {
		addHost(rule.Host)
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			path := p.Path
			if path == "" {
				path = "/"
			}
			rr := RouteRule{Paths: []string{path}}
			if rule.Host != "" {
				rr.Hosts = []string{rule.Host}
			}
			if p.Backend.Service != nil {
				rr.Backends = append(rr.Backends, BackendRef{
					Kind: "Service",
					Name: p.Backend.Service.Name,
					Port: ingressBackendPortString(p.Backend.Service.Port),
				})
			}
			c.Rules = append(c.Rules, rr)
		}
	}
	if ing.Spec.IngressClassName != nil && *ing.Spec.IngressClassName != "" {
		// Surface IngressClass as a low-frequency "Selector"-style field;
		// the UI renders it as a chip next to the type/clusterIP set.
		c.Selector = map[string]string{"ingressClass": *ing.Spec.IngressClassName}
	}
	return c
}

func ingressBackendPortString(p networkingv1.ServiceBackendPort) string {
	if p.Name != "" {
		return p.Name
	}
	if p.Number > 0 {
		return fmt.Sprintf("%d", p.Number)
	}
	return ""
}

func ingressBackendNames(ing *networkingIngress) []string {
	if ing == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	add := func(n string) {
		if n == "" || seen[n] {
			return
		}
		seen[n] = true
		out = append(out, n)
	}
	if ing.Spec.DefaultBackend != nil && ing.Spec.DefaultBackend.Service != nil {
		add(ing.Spec.DefaultBackend.Service.Name)
	}
	for _, rule := range ing.Spec.Rules {
		if rule.HTTP == nil {
			continue
		}
		for _, p := range rule.HTTP.Paths {
			if p.Backend.Service != nil {
				add(p.Backend.Service.Name)
			}
		}
	}
	return out
}

// traceRouteEntry: HTTPRoute or GRPCRoute. Downstream is Route → (Service +
// Pods) per backend; upstreams are the route's parent Gateways via
// parentRefs. Every backend is walked, not just the first — a route fanning
// out to N services where one is broken must surface that backend's hop or
// the verdict lies. Cross-namespace backends respect the BackendRef's own
// namespace.
func traceRouteEntry(deps Deps, t *Trace, kind string) {
	gvr, ok := deps.Discovery.GetGVRWithGroup(kind, "gateway.networking.k8s.io")
	if !ok {
		t.Verdict = VerdictUnknown
		t.Reason = kind + " API not available in this cluster"
		return
	}
	route, err := deps.Dynamic.Get(gvr, t.Subject.Namespace, t.Subject.Name)
	if err != nil || route == nil {
		t.Verdict = VerdictUnknown
		t.Reason = serviceLookupReason(err, t.Subject)
		return
	}
	routeRef := ResourceRef{Group: "gateway.networking.k8s.io", Kind: kind, Namespace: route.GetNamespace(), Name: route.GetName()}
	t.Subject = routeRef

	hops := []Hop{{
		Resource: routeRef,
		Edge:     "entry:" + kind,
		Findings: hopFindings(deps.Issues, routeRef),
		Config:   routeConfig(route),
	}}

	backends := routeBackends(route)
	if len(backends) > maxBackendsTraced {
		backends = backends[:maxBackendsTraced]
		t.Truncated = true
	}
	for _, b := range backends {
		svcRef := ResourceRef{Kind: "Service", Namespace: b.Namespace, Name: b.Name}
		svcHop := Hop{
			Resource: svcRef,
			Edge:     kind + "->Service",
			Findings: hopFindings(deps.Issues, svcRef),
		}
		svc, svcErr := deps.Cache.Services().Services(b.Namespace).Get(b.Name)
		if svcErr == nil {
			svcHop.Config = serviceConfig(svc)
		}
		hops = append(hops, svcHop)
		if svcErr == nil && len(svc.Spec.Selector) > 0 {
			hops = append(hops, buildPodsHop(deps, svc, selectedPods(deps, svc)))
		}
	}
	t.Downstream = hops
	t.Upstreams = routeParentGateways(deps, route)
}

// routeConfig captures the route's hostnames + per-rule match/backend
// shape. Unlike Ingress, HTTPRoute paths can be regex/prefix/exact — we
// flatten to a single display string per match (the matcher type is shown
// as a prefix like "PathPrefix:") so the UI doesn't need a sub-tab.
func routeConfig(route *unstructured.Unstructured) *HopConfig {
	if route == nil {
		return nil
	}
	c := &HopConfig{}
	hostnames, _, _ := unstructured.NestedStringSlice(route.Object, "spec", "hostnames")
	c.Hostnames = hostnames

	rules, found, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	if !found {
		return c
	}
	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		var paths []string
		matches, _ := rm["matches"].([]any)
		for _, m := range matches {
			mm, ok := m.(map[string]any)
			if !ok {
				continue
			}
			pathSpec, _ := mm["path"].(map[string]any)
			if pathSpec != nil {
				pType, _ := pathSpec["type"].(string)
				pValue, _ := pathSpec["value"].(string)
				if pValue == "" {
					pValue = "/"
				}
				if pType != "" && pType != "PathPrefix" {
					paths = append(paths, pType+":"+pValue)
				} else {
					paths = append(paths, pValue)
				}
			}
		}
		var backends []BackendRef
		refs, _ := rm["backendRefs"].([]any)
		for _, ref := range refs {
			refm, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			group, _ := refm["group"].(string)
			kind, _ := refm["kind"].(string)
			if kind == "" {
				kind = "Service"
			}
			name, _ := refm["name"].(string)
			ns, _ := refm["namespace"].(string)
			b := BackendRef{Kind: kind, Name: name, Namespace: ns}
			if group != "" {
				b.Kind = group + "/" + kind
			}
			b.Port = backendPortString(refm["port"])
			backends = append(backends, b)
		}
		rr := RouteRule{Paths: paths, Backends: backends}
		c.Rules = append(c.Rules, rr)
	}
	return c
}

func backendPortString(v any) string {
	switch n := v.(type) {
	case int64:
		return fmt.Sprintf("%d", n)
	case int32:
		return fmt.Sprintf("%d", n)
	case int:
		return fmt.Sprintf("%d", n)
	case float64:
		if n == float64(int64(n)) {
			return fmt.Sprintf("%d", int64(n))
		}
	case string:
		return n
	}
	return ""
}

func routeBackends(route *unstructured.Unstructured) []ResourceRef {
	rules, found, _ := unstructured.NestedSlice(route.Object, "spec", "rules")
	if !found {
		return nil
	}
	seen := map[string]bool{}
	var out []ResourceRef
	for _, r := range rules {
		rm, ok := r.(map[string]any)
		if !ok {
			continue
		}
		refs, _ := rm["backendRefs"].([]any)
		for _, ref := range refs {
			refm, ok := ref.(map[string]any)
			if !ok {
				continue
			}
			group, _ := refm["group"].(string)
			kind, _ := refm["kind"].(string)
			if group != "" || (kind != "" && kind != "Service") {
				continue
			}
			name, _ := refm["name"].(string)
			ns, _ := refm["namespace"].(string)
			if ns == "" {
				ns = route.GetNamespace()
			}
			key := ns + "/" + name
			if name == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ResourceRef{Kind: "Service", Namespace: ns, Name: name})
		}
	}
	return out
}

func routeParentGateways(deps Deps, route *unstructured.Unstructured) []Hop {
	parents, found, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	if !found {
		return nil
	}
	gvr, ok := deps.Discovery.GetGVRWithGroup("Gateway", "gateway.networking.k8s.io")
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []Hop
	for _, p := range parents {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		group, _ := pm["group"].(string)
		kind, _ := pm["kind"].(string)
		if (group != "" && group != "gateway.networking.k8s.io") || (kind != "" && kind != "Gateway") {
			continue
		}
		name, _ := pm["name"].(string)
		ns, _ := pm["namespace"].(string)
		if ns == "" {
			ns = route.GetNamespace()
		}
		key := ns + "/" + name
		if name == "" || seen[key] {
			continue
		}
		seen[key] = true
		gw, _ := deps.Dynamic.Get(gvr, ns, name)
		ref := ResourceRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Namespace: ns, Name: name}
		findings := hopFindings(deps.Issues, ref)
		if gw == nil {
			findings = append(findings, Finding{
				Code:     "gateway:missing-parent",
				Severity: SeverityCritical,
				Message:  fmt.Sprintf("parentRef points at Gateway %q in namespace %q which does not exist", name, ns),
				Command:  fmt.Sprintf("kubectl get gateway.gateway.networking.k8s.io -n %s", ns),
			})
		}
		out = append(out, Hop{
			Resource: ref,
			Edge:     "Gateway->Route",
			Findings: findings,
		})
	}
	return out
}

// traceGatewayEntry: a Gateway's downstream is its attached routes as
// parallel hops (capped at gatewayRouteCap to keep the response bounded on
// shared gateways). We don't recurse into each route's own pods — the Diagnose
// tab on the route does that. The Gateway is the subject of its own posture;
// upstreams (which controller manages it) are not modeled here.
func traceGatewayEntry(deps Deps, t *Trace) {
	gvr, ok := deps.Discovery.GetGVRWithGroup("Gateway", "gateway.networking.k8s.io")
	if !ok {
		t.Verdict = VerdictUnknown
		t.Reason = "Gateway API not available in this cluster"
		return
	}
	gw, err := deps.Dynamic.Get(gvr, t.Subject.Namespace, t.Subject.Name)
	if err != nil || gw == nil {
		t.Verdict = VerdictUnknown
		t.Reason = serviceLookupReason(err, t.Subject)
		return
	}
	gwRef := ResourceRef{Group: "gateway.networking.k8s.io", Kind: "Gateway", Namespace: gw.GetNamespace(), Name: gw.GetName()}
	t.Subject = gwRef
	hops := []Hop{{
		Resource: gwRef,
		Edge:     "entry:Gateway",
		Findings: hopFindings(deps.Issues, gwRef),
		Config:   gatewayConfig(gw),
	}}

	attached, total := attachedRoutes(deps, gw)
	for _, ref := range attached {
		hops = append(hops, Hop{
			Resource: ref,
			Edge:     "Gateway->" + ref.Kind,
			Findings: hopFindings(deps.Issues, ref),
		})
	}
	if total > len(attached) {
		t.Truncated = true
		hops = append(hops, Hop{
			Resource: ResourceRef{Kind: "Routes"},
			Edge:     "Gateway->Routes",
			Findings: []Finding{{
				Code:     "gateway:routes-truncated",
				Severity: SeverityInfo,
				Message:  fmt.Sprintf("Showing %d of %d attached routes; cap protects response size on shared gateways", len(attached), total),
			}},
		})
	}
	t.Downstream = hops
}

// gatewayConfig captures listeners (port/protocol/hostname/name) and the
// status.addresses field — Gateway-API surfaces its routable identity via
// addresses once the controller assigns them.
func gatewayConfig(gw *unstructured.Unstructured) *HopConfig {
	if gw == nil {
		return nil
	}
	c := &HopConfig{}
	listeners, _, _ := unstructured.NestedSlice(gw.Object, "spec", "listeners")
	for _, l := range listeners {
		lm, ok := l.(map[string]any)
		if !ok {
			continue
		}
		gl := GatewayListener{}
		if v, ok := lm["name"].(string); ok {
			gl.Name = v
		}
		switch v := lm["port"].(type) {
		case int64:
			gl.Port = int32(v)
		case float64:
			gl.Port = int32(v)
		}
		if v, ok := lm["protocol"].(string); ok {
			gl.Protocol = v
		}
		if v, ok := lm["hostname"].(string); ok {
			gl.Hostname = v
		}
		c.Listeners = append(c.Listeners, gl)
	}
	addresses, _, _ := unstructured.NestedSlice(gw.Object, "status", "addresses")
	for _, a := range addresses {
		am, ok := a.(map[string]any)
		if !ok {
			continue
		}
		if v, ok := am["value"].(string); ok && v != "" {
			c.Addresses = append(c.Addresses, v)
		}
	}
	return c
}

func attachedRoutes(deps Deps, gw *unstructured.Unstructured) ([]ResourceRef, int) {
	var all []ResourceRef
	for _, kind := range []string{"HTTPRoute", "GRPCRoute"} {
		gvr, ok := deps.Discovery.GetGVRWithGroup(kind, "gateway.networking.k8s.io")
		if !ok {
			continue
		}
		routes, err := deps.Dynamic.ListWatched(gvr)
		if err != nil {
			continue
		}
		for _, r := range routes {
			if routeAttachedToGateway(r, gw.GetNamespace(), gw.GetName()) {
				all = append(all, ResourceRef{Group: "gateway.networking.k8s.io", Kind: kind, Namespace: r.GetNamespace(), Name: r.GetName()})
			}
		}
	}
	sortRefs(all)
	if len(all) > gatewayRouteCap {
		return all[:gatewayRouteCap], len(all)
	}
	return all, len(all)
}

func routeAttachedToGateway(route *unstructured.Unstructured, gwNS, gwName string) bool {
	parents, found, _ := unstructured.NestedSlice(route.Object, "spec", "parentRefs")
	if !found {
		return false
	}
	for _, p := range parents {
		pm, ok := p.(map[string]any)
		if !ok {
			continue
		}
		name, _ := pm["name"].(string)
		ns, _ := pm["namespace"].(string)
		if ns == "" {
			ns = route.GetNamespace()
		}
		if name == gwName && ns == gwNS {
			return true
		}
	}
	return false
}

func refForService(svc *corev1.Service) ResourceRef {
	return ResourceRef{Kind: "Service", Namespace: svc.Namespace, Name: svc.Name}
}

func serviceLookupReason(err error, subject ResourceRef) string {
	if apierrors.IsNotFound(err) {
		return fmt.Sprintf("%s %s/%s not found in cache", subject.Kind, subject.Namespace, subject.Name)
	}
	if apierrors.IsForbidden(err) {
		return "RBAC denies access to this resource for the current user"
	}
	return strings.TrimSpace(fmt.Sprintf("lookup failed: %v", err))
}


// sortRefs orders a slice of ResourceRef by (Kind, Namespace, Name) so the
// trace's attached-routes / upstream lists are stable across polls — without
// it, map iteration order leaks into the rendered output.
func sortRefs(refs []ResourceRef) {
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].Kind != refs[j].Kind {
			return refs[i].Kind < refs[j].Kind
		}
		if refs[i].Namespace != refs[j].Namespace {
			return refs[i].Namespace < refs[j].Namespace
		}
		return refs[i].Name < refs[j].Name
	})
}
