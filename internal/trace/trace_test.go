package trace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/skyhook-io/radar/internal/issues"
	"github.com/skyhook-io/radar/internal/k8s"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/fake"
)

func waitFor(t *testing.T, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("condition not met within deadline")
}

func TestBuildTrace_ServiceHealthy(t *testing.T) {
	defer k8s.ResetTestState()

	labelsMap := map[string]string{"app": "api"}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec: corev1.ServiceSpec{
			Selector: labelsMap,
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80, TargetPort: intstr.FromString("http")}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "prod", Labels: labelsMap},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{
			Name:  "main",
			Ports: []corev1.ContainerPort{{Name: "http", ContainerPort: 8080}},
			ReadinessProbe: &corev1.Probe{ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{Port: intstr.FromString("http")},
			}},
		}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())}},
		},
	}
	client := fake.NewClientset(svc, pod)
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}

	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}

	var trace *Trace
	waitFor(t, func() bool {
		tr, err := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "api", Options{})
		if err != nil {
			return false
		}
		trace = tr
		return trace.Verdict == VerdictHealthy
	})

	if trace.Subject.Kind != "Service" || trace.Subject.Name != "api" {
		t.Errorf("subject = %+v, want Service prod/api", trace.Subject)
	}
	if len(trace.Downstream) != 2 {
		t.Fatalf("downstream len = %d, want 2 (Service, Pods); got %+v", len(trace.Downstream), trace.Downstream)
	}
	if trace.Downstream[0].Resource.Kind != "Service" || trace.Downstream[1].Resource.Kind != "Pods" {
		t.Errorf("downstream order wrong: %+v", trace.Downstream)
	}
	if trace.BrokenAt != -1 {
		t.Errorf("brokenAt = %d, want -1 on healthy trace", trace.BrokenAt)
	}
	if trace.Downstream[1].Meta["selected"] != 1 {
		t.Errorf("pods hop selected=%v, want 1", trace.Downstream[1].Meta["selected"])
	}
	if trace.Downstream[1].Meta["endpointSource"] != "pod-readiness" {
		t.Errorf("endpointSource=%v, want pod-readiness", trace.Downstream[1].Meta["endpointSource"])
	}
}

func TestBuildTrace_ServiceNoSelectorIsSelectorless(t *testing.T) {
	defer k8s.ResetTestState()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "manual", Namespace: "prod"},
		Spec: corev1.ServiceSpec{
			Ports: []corev1.ServicePort{{Port: 80}},
		},
	}
	client := fake.NewClientset(svc)
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}
	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}

	var trace *Trace
	waitFor(t, func() bool {
		tr, _ := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "manual", Options{})
		if tr == nil {
			return false
		}
		trace = tr
		return len(trace.Downstream) == 1
	})

	if trace.Downstream[0].Meta["selectorless"] != true {
		t.Errorf("expected selectorless=true on Service hop meta, got %+v", trace.Downstream[0].Meta)
	}
	hasInfo := false
	for _, f := range trace.Downstream[0].Findings {
		if f.Code == "svc:selectorless" {
			hasInfo = true
		}
	}
	if !hasInfo {
		t.Errorf("expected svc:selectorless finding, got %+v", trace.Downstream[0].Findings)
	}
	if trace.Verdict != VerdictUnknown {
		t.Errorf("selectorless trace must not claim healthy; got verdict=%q", trace.Verdict)
	}
	if trace.Reason == "" {
		t.Errorf("unknown verdict must carry a reason for the banner")
	}
}

func TestBuildTrace_ServiceExternalNameIsTerminal(t *testing.T) {
	defer k8s.ResetTestState()

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "ext", Namespace: "prod"},
		Spec: corev1.ServiceSpec{
			Type:         corev1.ServiceTypeExternalName,
			ExternalName: "stripe.example.com",
		},
	}
	client := fake.NewClientset(svc)
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}
	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}

	var trace *Trace
	waitFor(t, func() bool {
		tr, _ := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "ext", Options{})
		if tr == nil {
			return false
		}
		trace = tr
		return len(trace.Downstream) == 2
	})
	if trace.Downstream[1].Resource.Kind != "ExternalName" {
		t.Errorf("expected ExternalName hop, got %+v", trace.Downstream[1].Resource)
	}
	if trace.Verdict == VerdictBroken {
		t.Errorf("ExternalName service should not be broken: %+v", trace)
	}
}

func TestBuildTrace_ServiceWithUpstreamIngressIsParallel(t *testing.T) {
	defer k8s.ResetTestState()

	labelsMap := map[string]string{"app": "api"}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec:       corev1.ServiceSpec{Selector: labelsMap, Ports: []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(8080)}}},
	}
	ingA := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing-a", Namespace: "prod"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
				Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "api", Port: networkingv1.ServiceBackendPort{Number: 80}}},
			}}}},
		}}},
	}
	ingB := &networkingv1.Ingress{
		ObjectMeta: metav1.ObjectMeta{Name: "ing-b", Namespace: "prod"},
		Spec: networkingv1.IngressSpec{Rules: []networkingv1.IngressRule{{
			IngressRuleValue: networkingv1.IngressRuleValue{HTTP: &networkingv1.HTTPIngressRuleValue{Paths: []networkingv1.HTTPIngressPath{{
				Backend: networkingv1.IngressBackend{Service: &networkingv1.IngressServiceBackend{Name: "api", Port: networkingv1.ServiceBackendPort{Number: 80}}},
			}}}},
		}}},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "prod", Labels: labelsMap},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue, LastTransitionTime: metav1.NewTime(time.Now())}},
		},
	}
	client := fake.NewClientset(svc, ingA, ingB, pod)
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}
	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}

	var trace *Trace
	waitFor(t, func() bool {
		tr, _ := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "api", Options{})
		if tr == nil {
			return false
		}
		trace = tr
		return len(trace.Upstreams) == 2
	})

	if trace.Verdict != VerdictHealthy && trace.Verdict != VerdictDegraded {
		t.Errorf("verdict = %q, expected healthy/degraded with two healthy ingresses; trace=%+v", trace.Verdict, trace)
	}
}

func TestBuildTrace_BrokenServiceFlagsBrokenAt(t *testing.T) {
	defer k8s.ResetTestState()

	labelsMap := map[string]string{"app": "api"}
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "prod"},
		Spec: corev1.ServiceSpec{
			Selector: labelsMap,
			Ports:    []corev1.ServicePort{{Port: 80, TargetPort: intstr.FromInt(8080)}},
		},
	}
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "prod", Labels: labelsMap, CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute))},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "main"}}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionFalse, LastTransitionTime: metav1.NewTime(time.Now().Add(-10 * time.Minute))}},
		},
	}
	client := fake.NewClientset(svc, pod)
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}
	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}

	var trace *Trace
	waitFor(t, func() bool {
		tr, _ := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "api", Options{})
		if tr == nil {
			return false
		}
		trace = tr
		return trace.Verdict == VerdictBroken || trace.Verdict == VerdictDegraded
	})

	if trace.Verdict != VerdictBroken {
		t.Fatalf("expected broken verdict with 0 ready pods; got %q (trace=%+v)", trace.Verdict, trace)
	}
	// The existing detection attaches "0/N selected pods ready" to the
	// Service (not each Pod) — that's where the operator can act. So
	// BrokenAt points at the Service hop (index 0), and the diagnosis
	// localizes correctly: "this Service has no ready endpoints".
	if trace.BrokenAt != 0 {
		t.Errorf("brokenAt=%d, want 0 (Service hop carries the no-ready-endpoints finding)", trace.BrokenAt)
	}
	var noReadyCode string
	for _, f := range trace.Downstream[0].Findings {
		if strings.Contains(f.Code, "no-ready-endpoints") || strings.Contains(f.Message, "0/") {
			noReadyCode = f.Code
		}
	}
	if noReadyCode == "" {
		t.Errorf("expected svc:no-ready-endpoints style finding on Service hop, got %+v", trace.Downstream[0].Findings)
	}
}

func TestBuildTrace_CacheNotReadyReturnsUnknown(t *testing.T) {
	defer k8s.ResetTestState()

	deps := Deps{
		Cache:     nil,
		Dynamic:   nil,
		Discovery: nil,
		Issues:    nil,
	}
	trace, err := BuildTraceWithOptions(context.Background(), deps, "Service", "prod", "api", Options{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if trace.Verdict != VerdictUnknown {
		t.Errorf("verdict=%q, want unknown when cache not ready", trace.Verdict)
	}
	if !strings.Contains(trace.Reason, "cache syncing") {
		t.Errorf("reason=%q, want cache-syncing message", trace.Reason)
	}
}

func TestBuildTrace_UnknownKindReturnsUnknown(t *testing.T) {
	defer k8s.ResetTestState()
	client := fake.NewClientset()
	if err := k8s.InitTestResourceCache(client); err != nil {
		t.Fatalf("init: %v", err)
	}
	deps := Deps{
		Cache:     k8s.GetResourceCache(),
		Dynamic:   k8s.GetDynamicResourceCache(),
		Discovery: k8s.GetResourceDiscovery(),
		Issues:    issues.NewCacheProvider(),
	}
	trace, _ := BuildTraceWithOptions(context.Background(), deps, "Pod", "prod", "x", Options{})
	if trace.Verdict != VerdictUnknown {
		t.Errorf("verdict=%q, want unknown for unsupported kind", trace.Verdict)
	}
}
