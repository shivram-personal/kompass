package resourcecontext

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestRefReasonMarshalsSnakeCase asserts every RefReason constant
// marshals to its locked snake_case literal — the wire contract.
func TestRefReasonMarshalsSnakeCase(t *testing.T) {
	cases := map[RefReason]string{
		ReasonOwnerReference:   "owner_reference",
		ReasonLabelSelector:    "label_selector",
		ReasonPodSelector:      "pod_selector_match",
		ReasonPolicyReportSubj: "policy_report_subject",
		ReasonVolumeMount:      "volume_mount_ref",
		ReasonEnvVarRef:        "env_var_ref",
		ReasonScaleTargetRef:   "scale_target_ref",
		ReasonClaimRef:         "claim_ref",
		ReasonNodeName:         "node_name",
		ReasonSAName:           "service_account_name",
	}
	for c, want := range cases {
		got, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal RefReason %q: %v", c, err)
		}
		if string(got) != `"`+want+`"` {
			t.Errorf("RefReason marshal: got %s, want %q", got, want)
		}
	}
}

// TestRefReasonUnmarshalsSnakeCase asserts each locked snake_case literal
// round-trips into the matching constant.
func TestRefReasonUnmarshalsSnakeCase(t *testing.T) {
	cases := map[string]RefReason{
		"owner_reference":       ReasonOwnerReference,
		"label_selector":        ReasonLabelSelector,
		"pod_selector_match":    ReasonPodSelector,
		"policy_report_subject": ReasonPolicyReportSubj,
		"volume_mount_ref":      ReasonVolumeMount,
		"env_var_ref":           ReasonEnvVarRef,
		"scale_target_ref":      ReasonScaleTargetRef,
		"claim_ref":             ReasonClaimRef,
		"node_name":             ReasonNodeName,
		"service_account_name":  ReasonSAName,
	}
	for in, want := range cases {
		var got RefReason
		if err := json.Unmarshal([]byte(`"`+in+`"`), &got); err != nil {
			t.Fatalf("unmarshal RefReason %q: %v", in, err)
		}
		if got != want {
			t.Errorf("RefReason unmarshal %q: got %q, want %q", in, got, want)
		}
	}
}

func TestRefSourceMarshalsSnakeCase(t *testing.T) {
	cases := map[RefSource]string{
		SourceTopology:     "topology",
		SourceOwnerChain:   "owner_chain",
		SourcePolicyReport: "policy_report",
		SourceAuditEngine:  "audit_engine",
		SourceK8sSpec:      "k8s_spec",
	}
	for c, want := range cases {
		got, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal RefSource %q: %v", c, err)
		}
		if string(got) != `"`+want+`"` {
			t.Errorf("RefSource marshal: got %s, want %q", got, want)
		}
	}
}

func TestRefSourceUnmarshalsSnakeCase(t *testing.T) {
	cases := map[string]RefSource{
		"topology":      SourceTopology,
		"owner_chain":   SourceOwnerChain,
		"policy_report": SourcePolicyReport,
		"audit_engine":  SourceAuditEngine,
		"k8s_spec":      SourceK8sSpec,
	}
	for in, want := range cases {
		var got RefSource
		if err := json.Unmarshal([]byte(`"`+in+`"`), &got); err != nil {
			t.Fatalf("unmarshal RefSource %q: %v", in, err)
		}
		if got != want {
			t.Errorf("RefSource unmarshal %q: got %q, want %q", in, got, want)
		}
	}
}

func TestContextTierMarshalsSnakeCase(t *testing.T) {
	cases := map[ContextTier]string{
		TierBasic:      "basic",
		TierDiagnostic: "diagnostic",
	}
	for c, want := range cases {
		got, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal ContextTier %q: %v", c, err)
		}
		if string(got) != `"`+want+`"` {
			t.Errorf("ContextTier marshal: got %s, want %q", got, want)
		}
	}
}

func TestContextTierUnmarshalsSnakeCase(t *testing.T) {
	cases := map[string]ContextTier{
		"basic":      TierBasic,
		"diagnostic": TierDiagnostic,
	}
	for in, want := range cases {
		var got ContextTier
		if err := json.Unmarshal([]byte(`"`+in+`"`), &got); err != nil {
			t.Fatalf("unmarshal ContextTier %q: %v", in, err)
		}
		if got != want {
			t.Errorf("ContextTier unmarshal %q: got %q, want %q", in, got, want)
		}
	}
}

func TestOmittedReasonMarshalsSnakeCase(t *testing.T) {
	cases := map[OmittedReason]string{
		OmittedRBACDenied:       "rbac_denied",
		OmittedBudgetExceeded:   "budget_exceeded",
		OmittedCacheCold:        "cache_cold",
		OmittedNotInstalled:     "not_installed",
		OmittedKindUnsupported:  "kind_unsupported",
		OmittedProviderDisabled: "provider_disabled",
	}
	for c, want := range cases {
		got, err := json.Marshal(c)
		if err != nil {
			t.Fatalf("marshal OmittedReason %q: %v", c, err)
		}
		if string(got) != `"`+want+`"` {
			t.Errorf("OmittedReason marshal: got %s, want %q", got, want)
		}
	}
}

func TestOmittedReasonUnmarshalsSnakeCase(t *testing.T) {
	cases := map[string]OmittedReason{
		"rbac_denied":       OmittedRBACDenied,
		"budget_exceeded":   OmittedBudgetExceeded,
		"cache_cold":        OmittedCacheCold,
		"not_installed":     OmittedNotInstalled,
		"kind_unsupported":  OmittedKindUnsupported,
		"provider_disabled": OmittedProviderDisabled,
	}
	for in, want := range cases {
		var got OmittedReason
		if err := json.Unmarshal([]byte(`"`+in+`"`), &got); err != nil {
			t.Fatalf("unmarshal OmittedReason %q: %v", in, err)
		}
		if got != want {
			t.Errorf("OmittedReason unmarshal %q: got %q, want %q", in, got, want)
		}
	}
}

// TestEmptyResourceContextMarshalsStable pins the wire shape of a zero-value
// ResourceContext. omitempty handling means every nilable field disappears
// and only "tier" remains, with the empty string.
func TestEmptyResourceContextMarshalsStable(t *testing.T) {
	got, err := json.Marshal(ResourceContext{})
	if err != nil {
		t.Fatalf("marshal empty ResourceContext: %v", err)
	}
	want := `{"tier":""}`
	if string(got) != want {
		t.Errorf("empty ResourceContext marshal: got %s, want %s", got, want)
	}
}

// TestResourceContextFieldOrdering pins the on-the-wire field order by
// inspecting the marshaled JSON for a fully populated value. Go's
// encoder emits struct fields in declaration order, so this guards
// against accidental field reshuffling.
func TestResourceContextFieldOrdering(t *testing.T) {
	ac := ResourceContext{
		Tier:          TierBasic,
		ManagedBy:     []ContextRef{{Kind: "Deployment", Name: "api"}},
		Exposes:       []ContextRef{{Kind: "Service", Name: "api"}},
		SelectedBy:    []ContextRef{{Kind: "NetworkPolicy", Name: "default-deny"}},
		Uses:          &UsesBlock{},
		RunsOn:        &ContextRef{Kind: "Node", Name: "node-1"},
		ScaledBy:      []ContextRef{{Kind: "HorizontalPodAutoscaler", Name: "api-hpa"}},
		IssueSummary:  &IssueSummary{Count: 1},
		AuditSummary:  &AuditSummary{Count: 2},
		PolicySummary: &PolicySummary{},
		Hints:         []string{"hint"},
		Omitted:       []OmittedField{{Field: "selectedBy", Reason: OmittedRBACDenied}},
		Truncated:     true,
	}
	b, err := json.Marshal(ac)
	if err != nil {
		t.Fatalf("marshal populated ResourceContext: %v", err)
	}
	s := string(b)
	wantOrder := []string{
		`"tier"`,
		`"managedBy"`,
		`"exposes"`,
		`"selectedBy"`,
		`"uses"`,
		`"runsOn"`,
		`"scaledBy"`,
		`"issueSummary"`,
		`"auditSummary"`,
		`"policySummary"`,
		`"hints"`,
		`"omitted"`,
		`"truncated"`,
	}
	prev := -1
	for _, key := range wantOrder {
		idx := strings.Index(s, key)
		if idx == -1 {
			t.Fatalf("missing key %s in %s", key, s)
		}
		if idx <= prev {
			t.Fatalf("field %s out of order in %s", key, s)
		}
		prev = idx
	}
}

// TestResourceContextRoundTrip marshals a populated ResourceContext, unmarshals
// it back, and asserts deep equality. Covers every type defined in this
// package.
func TestResourceContextRoundTrip(t *testing.T) {
	orig := ResourceContext{
		Tier: TierDiagnostic,
		ManagedBy: []ContextRef{{
			Kind:      "Deployment",
			Group:     "apps",
			Namespace: "prod",
			Name:      "api",
			Reason:    ReasonOwnerReference,
			Source:    SourceOwnerChain,
		}},
		Exposes: []ContextRef{{
			Kind:      "Service",
			Namespace: "prod",
			Name:      "api",
			Reason:    ReasonLabelSelector,
			Source:    SourceTopology,
		}},
		SelectedBy: []ContextRef{{
			Kind:      "NetworkPolicy",
			Group:     "networking.k8s.io",
			Namespace: "prod",
			Name:      "default-deny",
			Reason:    ReasonPodSelector,
			Source:    SourceTopology,
		}},
		Uses: &UsesBlock{
			ConfigMaps: []ContextRef{{Kind: "ConfigMap", Name: "api-config", Reason: ReasonEnvVarRef, Source: SourceK8sSpec}},
			Secrets:    []ContextRef{{Kind: "Secret", Name: "api-creds", Reason: ReasonVolumeMount, Source: SourceK8sSpec}},
			ServiceAccount: &ContextRef{
				Kind: "ServiceAccount", Name: "api-sa",
				Reason: ReasonSAName, Source: SourceK8sSpec,
			},
			PVCs: []ContextRef{{Kind: "PersistentVolumeClaim", Name: "data", Reason: ReasonClaimRef, Source: SourceK8sSpec}},
		},
		RunsOn: &ContextRef{Kind: "Node", Name: "node-1", Reason: ReasonNodeName, Source: SourceK8sSpec},
		ScaledBy: []ContextRef{{
			Kind:   "HorizontalPodAutoscaler",
			Group:  "autoscaling",
			Name:   "api-hpa",
			Reason: ReasonScaleTargetRef,
			Source: SourceTopology,
		}},
		IssueSummary: &IssueSummary{
			Count:           3,
			HighestSeverity: "critical",
			TopReason:       "ImagePullBackOff",
			BySource:        map[string]int{"problem": 2, "condition": 1},
		},
		AuditSummary: &AuditSummary{
			Count:           4,
			HighestSeverity: "warning",
			TopFinding:      "CKV_K8S_8",
		},
		PolicySummary: &PolicySummary{
			Kyverno: &KyvernoSummary{
				Fail: 1, Warn: 2, Pass: 3,
				Top: []KyvernoFinding{{
					Policy:  "require-labels",
					Rule:    "check-app",
					Result:  "fail",
					Message: "missing label",
				}},
			},
		},
		Hints: []string{"Managed by Deployment api"},
		Omitted: []OmittedField{
			{Field: "selectedBy.networkPolicies", Reason: OmittedRBACDenied},
			{Field: "policySummary.kyverno", Reason: OmittedNotInstalled},
		},
		Truncated: true,
	}

	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got ResourceContext
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch:\nwant %#v\ngot  %#v", orig, got)
	}
}

// TestSummaryContextRoundTrip covers SummaryContext + ManagedByRef
// which are not embedded in ResourceContext.
func TestSummaryContextRoundTrip(t *testing.T) {
	orig := SummaryContext{
		ManagedBy:  &ManagedByRef{Kind: "Application", Source: "argocd", Name: "storefront", Namespace: "argocd"},
		Health:     "degraded",
		IssueCount: 2,
	}
	b, err := json.Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got SummaryContext
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch:\nwant %#v\ngot  %#v", orig, got)
	}

	// Compact wire shape: kind + source + name (+ optional namespace) only.
	// No group, reason, or confidence — those would inflate per-row bytes
	// in list/search responses with no consumer benefit at this tier.
	wantSubstr := []string{`"kind":"Application"`, `"source":"argocd"`, `"name":"storefront"`, `"namespace":"argocd"`, `"health":"degraded"`, `"issueCount":2`}
	s := string(b)
	for _, sub := range wantSubstr {
		if !strings.Contains(s, sub) {
			t.Errorf("SummaryContext JSON missing %s: %s", sub, s)
		}
	}
	for _, forbidden := range []string{`"group"`, `"reason"`, `"confidence"`} {
		if strings.Contains(s, forbidden) {
			t.Errorf("SummaryContext JSON leaks %s: %s", forbidden, s)
		}
	}
}

// TestManagedByRefDistinguishesFluxKinds pins the reason Kind was added:
// without it, Flux Kustomization vs HelmRelease serialize to identical
// JSON, forcing consumers to parse the Source string.
func TestManagedByRefDistinguishesFluxKinds(t *testing.T) {
	kustomization := SummaryContext{ManagedBy: &ManagedByRef{Kind: "Kustomization", Source: "flux", Name: "prod-apps", Namespace: "flux-system"}}
	helmRelease := SummaryContext{ManagedBy: &ManagedByRef{Kind: "HelmRelease", Source: "flux", Name: "prod-apps", Namespace: "flux-system"}}

	kJSON, _ := json.Marshal(kustomization)
	hJSON, _ := json.Marshal(helmRelease)
	if string(kJSON) == string(hJSON) {
		t.Fatalf("Flux Kustomization and HelmRelease must serialize to different JSON when Kind is set\nboth: %s", kJSON)
	}
	if !strings.Contains(string(kJSON), `"kind":"Kustomization"`) {
		t.Errorf("Kustomization JSON missing kind: %s", kJSON)
	}
	if !strings.Contains(string(hJSON), `"kind":"HelmRelease"`) {
		t.Errorf("HelmRelease JSON missing kind: %s", hJSON)
	}
}

// TestConfidenceOmittedWhenEmpty pins the contract that the reserved
// Confidence field is dropped from the wire when not populated.
func TestConfidenceOmittedWhenEmpty(t *testing.T) {
	ref := ContextRef{Kind: "Pod", Name: "p"}
	b, err := json.Marshal(ref)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "confidence") {
		t.Errorf("expected confidence omitted when empty, got %s", b)
	}
}
