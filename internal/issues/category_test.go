package issues

import "testing"

func TestClassify(t *testing.T) {
	cases := []struct {
		name string
		in   classifyInput
		want Category
	}{
		// scheduling
		{"unschedulable", classifyInput{Source: SourceScheduling, Kind: "Pod", Reason: "Unschedulable"}, CategoryUnschedulable},
		{"quota", classifyInput{Source: SourceScheduling, Kind: "Deployment", Reason: "QuotaExceeded"}, CategoryQuotaExceeded},
		{"limitrange", classifyInput{Source: SourceScheduling, Kind: "Deployment", Reason: "LimitRangeViolation"}, CategoryQuotaExceeded},
		{"podsecurity", classifyInput{Source: SourceScheduling, Kind: "Deployment", Reason: "PodSecurityViolation"}, CategoryAdmissionWebhookBlocking},
		{"webhook denied", classifyInput{Source: SourceScheduling, Kind: "StatefulSet", Reason: "WebhookDenied"}, CategoryAdmissionWebhookBlocking},
		{"ip exhaustion is startup stall", classifyInput{Source: SourceScheduling, Kind: "Pod", Reason: "IPExhaustion"}, CategoryContainerWaiting},
		{"sandbox failed is startup stall", classifyInput{Source: SourceScheduling, Kind: "Pod", Reason: "SandboxCreationFailed"}, CategoryContainerWaiting},
		{"volume multiattach", classifyInput{Source: SourceScheduling, Kind: "Pod", Reason: "VolumeMultiAttach"}, CategoryVolumeMountFailed},
		{"volume mount", classifyInput{Source: SourceScheduling, Kind: "Pod", Reason: "VolumeMount"}, CategoryVolumeMountFailed},

		// problem / Pod
		{"image pull backoff", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "ImagePullBackOff"}, CategoryImagePullFailed},
		{"err image pull", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "ErrImagePull"}, CategoryImagePullFailed},
		{"crashloop", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "CrashLoopBackOff"}, CategoryCrashLoop},
		{"oom by reason", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "OOMKilled"}, CategoryOOMKilled},
		{"oom by last-terminated, crashloop reason", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "CrashLoopBackOff", LastTerminatedReason: "OOMKilled"}, CategoryOOMKilled},
		{"config error waiting", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "CreateContainerConfigError"}, CategoryContainerWaiting},
		{"pending pod (non-scheduling)", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "Pending"}, CategoryContainerWaiting},
		{"errored pod", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "Error"}, CategoryCrashLoop},

		// problem / workloads
		{"deploy degraded", classifyInput{Source: SourceProblem, Kind: "Deployment", Reason: "3/5 available"}, CategoryWorkloadDegraded},
		{"deploy rollout stuck", classifyInput{Source: SourceProblem, Kind: "Deployment", Reason: "Rollout stuck"}, CategoryRolloutStalled},
		{"statefulset degraded", classifyInput{Source: SourceProblem, Kind: "StatefulSet", Reason: "2/3 ready"}, CategoryWorkloadDegraded},
		{"daemonset degraded", classifyInput{Source: SourceProblem, Kind: "DaemonSet", Reason: "1 unavailable"}, CategoryWorkloadDegraded},

		// problem / service
		{"service no pods", classifyInput{Source: SourceProblem, Kind: "Service", Reason: "Selector matches no pods"}, CategoryServiceNoEndpoints},
		{"service 0 ready", classifyInput{Source: SourceProblem, Kind: "Service", Reason: "0/3 selected pods ready"}, CategoryServiceNoEndpoints},

		// problem / hpa, node, pvc
		{"hpa maxed", classifyInput{Source: SourceProblem, Kind: "HorizontalPodAutoscaler", Reason: "maxed"}, CategoryHPALimitedOrFailed},
		{"node notready", classifyInput{Source: SourceProblem, Kind: "Node", Reason: "NotReady"}, CategoryNodeNotReady},
		{"node mempressure", classifyInput{Source: SourceProblem, Kind: "Node", Reason: "MemoryPressure"}, CategoryNodeNotReady},
		{"node cordoned is intentional", classifyInput{Source: SourceProblem, Kind: "Node", Reason: "Cordoned"}, CategoryUnknown},
		{"pvc pending", classifyInput{Source: SourceProblem, Kind: "PersistentVolumeClaim", Reason: "Pending"}, CategoryPVCPending},
		{"pvc lost is a gap", classifyInput{Source: SourceProblem, Kind: "PersistentVolumeClaim", Reason: "Lost"}, CategoryUnknown},

		// missing_ref
		{"missing configmap", classifyInput{Source: SourceMissingRef, Kind: "Pod", Reason: "Missing ConfigMap"}, CategoryMissingConfigRef},
		{"missing secret", classifyInput{Source: SourceMissingRef, Kind: "Pod", Reason: "Missing Secret"}, CategoryMissingConfigRef},
		{"missing pvc", classifyInput{Source: SourceMissingRef, Kind: "Pod", Reason: "Missing PVC"}, CategoryMissingConfigRef},
		{"missing sa", classifyInput{Source: SourceMissingRef, Kind: "Pod", Reason: "Missing ServiceAccount"}, CategoryMissingConfigRef},
		{"ingress backend missing", classifyInput{Source: SourceMissingRef, Kind: "Ingress", APIGroup: "networking.k8s.io", Reason: "Missing backend Service"}, CategoryIngressBackendMissing},
		{"ingress tls secret is config", classifyInput{Source: SourceMissingRef, Kind: "Ingress", APIGroup: "networking.k8s.io", Reason: "Missing TLS Secret"}, CategoryMissingConfigRef},
		{"webhook backend down", classifyInput{Source: SourceMissingRef, Kind: "ValidatingWebhookConfiguration", APIGroup: "admissionregistration.k8s.io", Reason: "Missing webhook backend Service"}, CategoryWebhookBackendDown},
		{"missing storageclass is pvc pending", classifyInput{Source: SourceMissingRef, Kind: "PersistentVolumeClaim", Reason: "Missing StorageClass"}, CategoryPVCPending},
		{"missing roleref is config", classifyInput{Source: SourceMissingRef, Kind: "RoleBinding", APIGroup: "rbac.authorization.k8s.io", Reason: "Missing roleRef target"}, CategoryMissingConfigRef},

		// condition (CRD fallback) — discriminated by API group
		{"argo sync failed", classifyInput{Source: SourceCondition, Kind: "Application", APIGroup: "argoproj.io", Reason: "Synced: ComparisonError"}, CategoryGitOpsSyncFailed},
		{"flux helmrelease", classifyInput{Source: SourceCondition, Kind: "HelmRelease", APIGroup: "helm.toolkit.fluxcd.io", Reason: "Ready=False"}, CategoryGitOpsSyncFailed},
		{"cert-manager not ready", classifyInput{Source: SourceCondition, Kind: "Certificate", APIGroup: "cert-manager.io", Reason: "Ready: DoesNotExist"}, CategoryCertificateNotReady},
		{"generic operator condition", classifyInput{Source: SourceCondition, Kind: "Foo", APIGroup: "example.com", Reason: "Ready=False"}, CategoryOperatorConditionFail},

		// gaps → unknown (CronJob/Job/CAPI)
		{"cronjob is a gap", classifyInput{Source: SourceProblem, Kind: "CronJob", Reason: "stale"}, CategoryUnknown},
		{"capi machine is a gap", classifyInput{Source: SourceProblem, Kind: "Machine", APIGroup: "cluster.x-k8s.io", Reason: "Machine in Failed phase"}, CategoryUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in); got != tc.want {
				t.Errorf("Classify(%+v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestGroupOf pins the category→group rollup and that every mapped category has
// a group (no silent GroupUnknown for a real category).
func TestGroupOf(t *testing.T) {
	if got := GroupOf(CategoryImagePullFailed); got != GroupStartup {
		t.Errorf("image_pull_failed group = %q, want startup", got)
	}
	if got := GroupOf(CategoryUnschedulable); got != GroupScheduling {
		t.Errorf("unschedulable group = %q, want scheduling", got)
	}
	if got := GroupOf(CategoryGitOpsSyncFailed); got != GroupControlPlane {
		t.Errorf("gitops_sync_failed group = %q, want control_plane", got)
	}
	if got := GroupOf(CategoryUnknown); got != GroupUnknown {
		t.Errorf("unknown group = %q, want unknown", got)
	}
	// Every category constant that Classify can return must map to a non-unknown group.
	for c := range categoryGroup {
		if categoryGroup[c] == GroupUnknown {
			t.Errorf("category %q maps to GroupUnknown", c)
		}
	}
}
