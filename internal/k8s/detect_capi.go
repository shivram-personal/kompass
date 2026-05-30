package k8s

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/skyhook-io/radar/pkg/conditions"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func DetectCAPIProblems(dynamicCache *DynamicResourceCache, discovery *ResourceDiscovery, namespace string) []Detection {
	if dynamicCache == nil || discovery == nil {
		return nil
	}

	var problems []Detection
	now := time.Now()

	// Helper: list CAPI resources by kind
	listCAPI := func(kind, group string) []*unstructured.Unstructured {
		if group != "" {
			gvr, ok := discovery.GetGVRWithGroup(kind, group)
			if !ok {
				return nil // CRD not installed — expected
			}
			resources, err := dynamicCache.List(gvr, namespace)
			if err != nil {
				log.Printf("[capi-problems] Failed to list %s (%s): %v", kind, group, err)
				return nil
			}
			return resources
		}
		gvr, ok := discovery.GetGVR(kind)
		if !ok {
			return nil // CRD not installed — expected
		}
		resources, err := dynamicCache.List(gvr, namespace)
		if err != nil {
			log.Printf("[capi-problems] Failed to list %s: %v", kind, err)
			return nil
		}
		return resources
	}

	// Shared condition reader: conditions.FindFalseCondition (one source of truth
	// across the CAPI/GitOps detectors + the issues generic fallback).

	const capiGroup = "cluster.x-k8s.io"
	const capiCPGroup = "controlplane.cluster.x-k8s.io"

	// -----------------------------------------------------------------------
	// CAPI Cluster problems
	// -----------------------------------------------------------------------
	for _, cl := range listCAPI("Cluster", capiGroup) {
		ageDur := now.Sub(cl.GetCreationTimestamp().Time)

		// Phase-based: Failed
		phase, _, _ := unstructured.NestedString(cl.Object, "status", "phase")
		if strings.EqualFold(phase, "failed") {
			problems = append(problems, Detection{
				Kind: "Cluster", Namespace: cl.GetNamespace(), Name: cl.GetName(), Group: capiGroup,
				Severity: "critical", Reason: "Cluster in Failed phase",
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(ageDur), DurationSeconds: int64(ageDur.Seconds()),
			})
			continue // don't double-report conditions
		}

		// Condition-based: InfrastructureReady, ControlPlaneReady, Ready, TopologyReconciled
		if ct, reason, msg, dur, ok := conditions.FindFalseCondition(cl,
			"Ready", "InfrastructureReady", "ControlPlaneReady", "TopologyReconciled",
		); ok {
			severity := "high"
			if ct == "InfrastructureReady" || ct == "ControlPlaneReady" {
				severity = "critical"
			}
			displayReason := reason
			if displayReason == "" {
				displayReason = ct + "=False"
			}
			d := dur
			if d == 0 {
				d = ageDur
			}
			problems = append(problems, Detection{
				Kind: "Cluster", Namespace: cl.GetNamespace(), Name: cl.GetName(), Group: capiGroup,
				Severity: severity, Reason: displayReason, Message: msg,
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(d), DurationSeconds: int64(d.Seconds()),
			})
		}
	}

	// -----------------------------------------------------------------------
	// CAPI Machine problems
	// -----------------------------------------------------------------------
	for _, m := range listCAPI("Machine", "cluster.x-k8s.io") {
		ageDur := now.Sub(m.GetCreationTimestamp().Time)
		phase, _, _ := unstructured.NestedString(m.Object, "status", "phase")

		// Phase-based: Failed
		if strings.EqualFold(phase, "failed") {
			// Include the condition message for richer context
			_, _, msg, _, _ := conditions.FindFalseCondition(m, "Ready", "InfrastructureReady", "BootstrapReady")
			problems = append(problems, Detection{
				Kind: "Machine", Namespace: m.GetNamespace(), Name: m.GetName(), Group: capiGroup,
				Severity: "critical", Reason: "Machine in Failed phase", Message: msg,
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(ageDur), DurationSeconds: int64(ageDur.Seconds()),
			})
			continue
		}

		// Phase-based: stuck Provisioning > 10m
		if strings.EqualFold(phase, "provisioning") && ageDur > 10*time.Minute {
			_, reason, msg, _, _ := conditions.FindFalseCondition(m, "InfrastructureReady", "BootstrapReady")
			displayReason := fmt.Sprintf("Stuck provisioning for %s", FormatAge(ageDur))
			if reason != "" {
				displayReason += " (" + reason + ")"
			}
			problems = append(problems, Detection{
				Kind: "Machine", Namespace: m.GetNamespace(), Name: m.GetName(), Group: capiGroup,
				Severity: "high", Reason: displayReason, Message: msg,
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(ageDur), DurationSeconds: int64(ageDur.Seconds()),
			})
			continue
		}

		// Condition-based: BootstrapReady=False, NodeHealthy=False, InfrastructureReady=False
		// (catches problems that phase alone misses, e.g. Running phase but NodeHealthy=False)
		if ct, reason, msg, dur, ok := conditions.FindFalseCondition(m,
			"BootstrapReady", "NodeHealthy", "InfrastructureReady",
		); ok {
			severity := "high"
			if ct == "BootstrapReady" {
				severity = "critical"
			}
			displayReason := reason
			if displayReason == "" {
				displayReason = ct + "=False"
			}
			d := dur
			if d == 0 {
				d = ageDur
			}
			problems = append(problems, Detection{
				Kind: "Machine", Namespace: m.GetNamespace(), Name: m.GetName(), Group: capiGroup,
				Severity: severity, Reason: displayReason, Message: msg,
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(d), DurationSeconds: int64(d.Seconds()),
			})
		}
	}

	// -----------------------------------------------------------------------
	// CAPI MachineDeployment problems: ready < desired for > 5m
	// -----------------------------------------------------------------------
	for _, md := range listCAPI("MachineDeployment", "") {
		desired, _, _ := unstructured.NestedInt64(md.Object, "spec", "replicas")
		ready, _, _ := unstructured.NestedInt64(md.Object, "status", "readyReplicas")
		if desired > 0 && ready < desired {
			ageDur := now.Sub(md.GetCreationTimestamp().Time)
			if ageDur > 5*time.Minute {
				_, reason, msg, _, _ := conditions.FindFalseCondition(md, "Ready", "Available")
				displayReason := fmt.Sprintf("%d/%d machines ready", ready, desired)
				if reason != "" {
					displayReason += " (" + reason + ")"
				}
				problems = append(problems, Detection{
					Kind: "MachineDeployment", Namespace: md.GetNamespace(), Name: md.GetName(), Group: capiGroup,
					Severity: "high", Reason: displayReason, Message: msg,
					Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
					Duration: FormatAge(ageDur), DurationSeconds: int64(ageDur.Seconds()),
				})
			}
		}
	}

	// -----------------------------------------------------------------------
	// CAPI KubeadmControlPlane problems: Ready=False or replicas mismatch
	// -----------------------------------------------------------------------
	for _, kcp := range listCAPI("KubeadmControlPlane", "") {
		ageDur := now.Sub(kcp.GetCreationTimestamp().Time)
		desired, _, _ := unstructured.NestedInt64(kcp.Object, "spec", "replicas")
		ready, _, _ := unstructured.NestedInt64(kcp.Object, "status", "readyReplicas")

		if ct, reason, msg, dur, ok := conditions.FindFalseCondition(kcp,
			"Ready", "Available", "CertificatesAvailable", "MachinesReady",
		); ok {
			severity := "critical"
			displayReason := reason
			if displayReason == "" {
				displayReason = ct + "=False"
			}
			if desired > 0 && ready < desired {
				displayReason = fmt.Sprintf("%d/%d CP replicas ready, %s", ready, desired, displayReason)
			}
			d := dur
			if d == 0 {
				d = ageDur
			}
			problems = append(problems, Detection{
				Kind: "KubeadmControlPlane", Namespace: kcp.GetNamespace(), Name: kcp.GetName(), Group: capiCPGroup,
				Severity: severity, Reason: displayReason, Message: msg,
				Age: FormatAge(ageDur), AgeSeconds: int64(ageDur.Seconds()),
				Duration: FormatAge(d), DurationSeconds: int64(d.Seconds()),
			})
		}
	}

	// -----------------------------------------------------------------------
	// CAPI MachineHealthCheck: actively remediating
	// -----------------------------------------------------------------------
	for _, mhc := range listCAPI("MachineHealthCheck", "") {
		expected, _, _ := unstructured.NestedInt64(mhc.Object, "status", "expectedMachines")
		healthy, _, _ := unstructured.NestedInt64(mhc.Object, "status", "currentHealthy")
		if expected > 0 && healthy < expected {
			ageDur := now.Sub(mhc.GetCreationTimestamp().Time)
			problems = append(problems, Detection{
				Kind: "MachineHealthCheck", Namespace: mhc.GetNamespace(), Name: mhc.GetName(), Group: capiGroup,
				Severity:        "high",
				Reason:          fmt.Sprintf("Remediating: %d/%d healthy", healthy, expected),
				Age:             FormatAge(ageDur),
				AgeSeconds:      int64(ageDur.Seconds()),
				Duration:        FormatAge(ageDur),
				DurationSeconds: int64(ageDur.Seconds()),
			})
		}
	}

	return problems
}
