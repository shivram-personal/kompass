package trace

import (
	"fmt"
	"sort"
	"strings"

	"github.com/skyhook-io/radar/internal/issues"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
)

// hopFindings collects findings for a hop from the live issues stream. We
// match the same way internal/mcp/tools_diagnose.go's RelatedIssues selection
// does — by (group, kind, namespace, name) — so a Phase 0 detection that
// fires for a Service shows up on that Service's hop with no special-casing.
func hopFindings(p *issues.CacheProvider, ref ResourceRef) []Finding {
	if p == nil {
		return nil
	}
	related := issues.RelatedIssues(p, nil, ref.Group, ref.Kind, ref.Namespace, ref.Name)
	if len(related) == 0 {
		return nil
	}
	out := make([]Finding, 0, len(related))
	for _, iss := range related {
		out = append(out, issueToFinding(iss, ref))
	}
	sortFindingsBySeverity(out)
	return out
}

// sortFindingsBySeverity orders worst-first so a hop with mixed severities
// leads with its most actionable row. Code is the tiebreaker for stable
// output across polls.
func sortFindingsBySeverity(out []Finding) {
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := severityRank(out[i].Severity), severityRank(out[j].Severity)
		if ri != rj {
			return ri > rj
		}
		return out[i].Code < out[j].Code
	})
}

func issueToFinding(iss issues.Issue, ref ResourceRef) Finding {
	severity := SeverityWarning
	if iss.Severity == issues.SeverityCritical {
		severity = SeverityCritical
	}
	code := issueCode(iss)
	msg := iss.Reason
	if iss.Message != "" {
		if msg != "" {
			msg = msg + " - " + iss.Message
		} else {
			msg = iss.Message
		}
	}
	return Finding{
		Code:     code,
		Severity: severity,
		Message:  msg,
		// Cause + Action are populated by the detectors that classify a
		// failure into plain-English diagnosis (CrashLoopBackOff exit codes,
		// ImagePullBackOff registry/auth/not-found, PVC provisioning). The
		// UI surfaces them as the primary readout when present so the
		// operator doesn't have to navigate to Issues to see "why".
		Cause:   iss.Cause,
		Action:  iss.Action,
		Command: reproducerForRef(ref),
	}
}

// issueCode derives a stable, agent-friendly code from an issue. Fingerprint
// is preferred when set — it's the same identity the issues pipeline uses for
// dedupe. Falling back to "SOURCE:reason" matches what an agent reading the
// trace would write themselves: which detector + what it found.
func issueCode(iss issues.Issue) string {
	if iss.Fingerprint != "" {
		return iss.Fingerprint
	}
	return string(iss.Source) + ":" + iss.Reason
}

// networkPolicyAdvisory flags that traffic to these pods MAY be restricted
// by NetworkPolicy without evaluating the rules. Rule evaluation would mean
// modelling CNI semantics zero-config, which produces false confidence —
// the controller is the only authority on whether a packet actually flows.
func networkPolicyAdvisory(deps Deps, svc *corev1.Service, pods []*corev1.Pod) (Finding, bool) {
	if deps.Cache == nil {
		return Finding{}, false
	}
	npLister := deps.Cache.NetworkPolicies()
	if npLister == nil || svc == nil || len(pods) == 0 {
		return Finding{}, false
	}
	policies, err := npLister.NetworkPolicies(svc.Namespace).List(labels.Everything())
	if err != nil || len(policies) == 0 {
		return Finding{}, false
	}
	matching := 0
	for _, np := range policies {
		sel, err := metav1.LabelSelectorAsSelector(&np.Spec.PodSelector)
		if err != nil {
			continue
		}
		for _, pod := range pods {
			if pod == nil {
				continue
			}
			if sel.Matches(labels.Set(pod.Labels)) {
				matching++
				break
			}
		}
	}
	if matching == 0 {
		return Finding{}, false
	}
	return Finding{
		Code:     "netpol:advisory",
		Severity: SeverityInfo,
		Message:  fmt.Sprintf("%d NetworkPolic%s select these pods; traffic may be restricted.", matching, pluralY(matching)),
		Command:  fmt.Sprintf("kubectl get networkpolicy -n %s", svc.Namespace),
	}, true
}

func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}

// reproducerForRef returns the kubectl-shaped one-liner an operator can paste
// to see the raw state behind a finding. The command is intentionally
// read-only — the trace explains, the operator decides.
func reproducerForRef(ref ResourceRef) string {
	ns := ""
	if ref.Namespace != "" {
		ns = " -n " + ref.Namespace
	}
	switch ref.Kind {
	case "Service":
		return "kubectl describe service " + ref.Name + ns
	case "Pod":
		return "kubectl describe pod " + ref.Name + ns
	case "Pods":
		return "kubectl get pods" + ns
	case "Ingress":
		return "kubectl describe ingress " + ref.Name + ns
	case "HTTPRoute":
		return fmt.Sprintf("kubectl get httproute %s%s -o jsonpath='{.status.parents}'", ref.Name, ns)
	case "GRPCRoute":
		return fmt.Sprintf("kubectl get grpcroute %s%s -o jsonpath='{.status.parents}'", ref.Name, ns)
	case "Gateway":
		return fmt.Sprintf("kubectl get gateway.gateway.networking.k8s.io %s%s -o yaml", ref.Name, ns)
	}
	return ""
}

func selectorReproducer(ref ResourceRef, selector map[string]string) string {
	if len(selector) == 0 || ref.Namespace == "" {
		return ""
	}
	keys := make([]string, 0, len(selector))
	for k := range selector {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+selector[k])
	}
	return "kubectl get pods -n " + ref.Namespace + " -l " + strings.Join(parts, ",")
}
