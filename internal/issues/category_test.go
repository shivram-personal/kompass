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
		{"podsecurity", classifyInput{Source: SourceScheduling, Kind: "Deployment", Reason: "PodSecurityViolation"}, CategoryPodSecurityViolation},
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
		{"high restart thrash", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "HighRestartCount"}, CategoryHighRestart},

		// problem / GitOps reconcilers (DetectGitOpsProblems → SourceProblem)
		{"argo app degraded", classifyInput{Source: SourceProblem, Kind: "Application", APIGroup: "argoproj.io", Reason: "HealthDegraded"}, CategoryGitOpsSyncFailed},
		{"argo app outofsync", classifyInput{Source: SourceProblem, Kind: "Application", APIGroup: "argoproj.io", Reason: "OutOfSync"}, CategoryGitOpsSyncFailed},
		{"flux kustomization problem", classifyInput{Source: SourceProblem, Kind: "Kustomization", APIGroup: "kustomize.toolkit.fluxcd.io", Reason: "ReconciliationFailed"}, CategoryGitOpsSyncFailed},
		{"flux helmrelease problem", classifyInput{Source: SourceProblem, Kind: "HelmRelease", APIGroup: "helm.toolkit.fluxcd.io", Reason: "InstallFailed"}, CategoryGitOpsSyncFailed},
		{"flux-looking kustomization group is not gitops", classifyInput{Source: SourceProblem, Kind: "Kustomization", APIGroup: "custom-fluxcd.io", Reason: "ReconciliationFailed"}, CategoryUnknown},
		{"non-argo Application kind is not gitops", classifyInput{Source: SourceProblem, Kind: "Application", APIGroup: "other.example.com", Reason: "whatever"}, CategoryUnknown},

		// argo Rollout is progressive delivery, NOT a sync failure
		{"argo rollout condition is rollout_stalled", classifyInput{Source: SourceCondition, Kind: "Rollout", APIGroup: "argoproj.io", Reason: "Ready: ProgressDeadlineExceeded"}, CategoryRolloutStalled},

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
		{"pvc lost is storage", classifyInput{Source: SourceProblem, Kind: "PersistentVolumeClaim", Reason: "Lost"}, CategoryPVCLost},
		{"pdb blocks evictions", classifyInput{Source: SourceProblem, Kind: "PodDisruptionBudget", Reason: "Voluntary evictions blocked"}, CategoryPDBBlocksEvictions},

		// problem / batch (Job/CronJob)
		{"job failed condition", classifyInput{Source: SourceProblem, Kind: "Job", Reason: "BackoffLimitExceeded"}, CategoryJobFailed},
		{"job failed fallback", classifyInput{Source: SourceProblem, Kind: "Job", Reason: "Failed"}, CategoryJobFailed},
		{"job stuck active", classifyInput{Source: SourceProblem, Kind: "Job", Reason: "Running for 3h with no completions"}, CategoryJobFailed},
		{"cronjob stale", classifyInput{Source: SourceProblem, Kind: "CronJob", Reason: "stale"}, CategoryCronJobFailed},
		{"cronjob never scheduled", classifyInput{Source: SourceProblem, Kind: "CronJob", Reason: "never-scheduled"}, CategoryCronJobFailed},

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
		{"flux helmrelease condition fallback is not sync", classifyInput{Source: SourceCondition, Kind: "HelmRelease", APIGroup: "helm.toolkit.fluxcd.io", Reason: "Ready=False"}, CategoryOperatorConditionFail},
		{"flux-looking helmrelease group is not sync", classifyInput{Source: SourceCondition, Kind: "HelmRelease", APIGroup: "custom-fluxcd.io", Reason: "Ready=False"}, CategoryOperatorConditionFail},
		{"cert-manager not ready", classifyInput{Source: SourceCondition, Kind: "Certificate", APIGroup: "cert-manager.io", Reason: "Ready: DoesNotExist"}, CategoryCertificateNotReady},
		{"cert-manager Issuer is NOT certificate_not_ready", classifyInput{Source: SourceCondition, Kind: "ClusterIssuer", APIGroup: "cert-manager.io", Reason: "Ready=False"}, CategoryOperatorConditionFail},
		{"generic operator condition", classifyInput{Source: SourceCondition, Kind: "Foo", APIGroup: "example.com", Reason: "Ready=False"}, CategoryOperatorConditionFail},
		// Flux source CRDs are NOT sync failures — they fall to operator condition.
		{"flux source repo is not sync", classifyInput{Source: SourceCondition, Kind: "GitRepository", APIGroup: "source.toolkit.fluxcd.io", Reason: "Ready: GitOperationFailed"}, CategoryOperatorConditionFail},
		{"argo non-app CRD is not sync", classifyInput{Source: SourceCondition, Kind: "AppProject", APIGroup: "argoproj.io", Reason: "Ready=False"}, CategoryOperatorConditionFail},

		// CAPI: control-plane vs machine layer, gated on the CAPI group.
		{"capi cluster failed", classifyInput{Source: SourceProblem, Kind: "Cluster", APIGroup: "cluster.x-k8s.io", Reason: "Cluster in Failed phase"}, CategoryControlPlaneNotReady},
		{"capi control plane not ready", classifyInput{Source: SourceProblem, Kind: "KubeadmControlPlane", APIGroup: "controlplane.cluster.x-k8s.io", Reason: "Ready=False"}, CategoryControlPlaneNotReady},
		{"capi machine failed", classifyInput{Source: SourceProblem, Kind: "Machine", APIGroup: "cluster.x-k8s.io", Reason: "Machine in Failed phase"}, CategoryMachineNotReady},
		{"capi machinedeployment", classifyInput{Source: SourceProblem, Kind: "MachineDeployment", APIGroup: "cluster.x-k8s.io", Reason: "Ready=False"}, CategoryMachineNotReady},
		{"non-capi Cluster kind is not control plane", classifyInput{Source: SourceProblem, Kind: "Cluster", APIGroup: "postgresql.cnpg.io", Reason: "whatever"}, CategoryUnknown},
		// new pod waiting reasons (bad image tag / container create)
		{"invalid image name", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "InvalidImageName"}, CategoryImagePullFailed},
		{"run container error", classifyInput{Source: SourceProblem, Kind: "Pod", Reason: "RunContainerError"}, CategoryContainerWaiting},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.in); got != tc.want {
				t.Errorf("Classify(%+v) = %q, want %q", tc.in, got, tc.want)
			}
			// Every category Classify emits must have a group rollup — a category
			// wired into Classify but missing from categoryGroup rolls up to
			// GroupUnknown silently. Asserted here, at the (mandatory) Classify
			// case, because TestGroupOf's map-iteration can't see a category
			// that's absent from the map.
			if tc.want != CategoryUnknown && GroupOf(tc.want) == GroupUnknown {
				t.Errorf("category %q has no categoryGroup rollup (→ GroupUnknown)", tc.want)
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
