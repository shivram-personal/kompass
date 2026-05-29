package issues

import "strings"

// Category is the user-facing symptom taxonomy for an issue — "what kind of
// problem is this" from an operator's mental model. It is DERIVED from the
// detection signal (Source + Kind + Reason + Message + crash context), not a
// new detector: the classifier below is a pure mapping over what radar already
// emits.
//
// Distinct from Source (the detection channel: problem|missing_ref|scheduling|
// condition) and from the resource's API Group. Category is the axis users
// filter and group by; Source stays an output label / CEL binding.
//
// `unknown` is first-class — an unmapped signal is honestly labeled, never
// dropped or force-fit into a neat bucket.
type Category string

const (
	CategoryUnknown Category = "unknown"

	// scheduling — can't get onto a node / rejected at admission
	CategoryUnschedulable            Category = "unschedulable"
	CategoryQuotaExceeded            Category = "quota_exceeded"
	CategoryAdmissionWebhookBlocking Category = "admission_webhook_blocking"

	// startup — scheduled, can't start the container
	CategoryImagePullFailed     Category = "image_pull_failed"
	CategoryContainerWaiting    Category = "container_waiting"
	CategoryInitContainerFailed Category = "init_container_failed"

	// runtime — started, won't stay healthy
	CategoryCrashLoop         Category = "crashloop"
	CategoryOOMKilled         Category = "oom_killed"
	CategoryLivenessProbeFail Category = "liveness_probe_failed"
	CategoryReadinessFailed   Category = "readiness_failed"
	CategoryWorkloadDegraded  Category = "workload_degraded"
	// batch workload failures (Job/CronJob) — runtime-stage failures of
	// one-shot / scheduled workloads.
	CategoryJobFailed     Category = "job_failed"
	CategoryCronJobFailed Category = "cronjob_failed"

	// configuration
	CategoryMissingConfigRef Category = "missing_config_ref"

	// networking
	CategoryServiceNoEndpoints    Category = "service_no_endpoints"
	CategoryIngressBackendMissing Category = "ingress_backend_missing"
	CategoryDNSFailure            Category = "dns_failure"
	CategoryNetworkPolicyBlock    Category = "network_policy_block"

	// storage
	CategoryPVCPending        Category = "pvc_pending"
	CategoryPVCLost           Category = "pvc_lost"
	CategoryVolumeMountFailed Category = "volume_mount_failed"

	// scaling / rollout
	CategoryRolloutStalled     Category = "rollout_stalled"
	CategoryHPALimitedOrFailed Category = "hpa_limited_or_failed"

	// security
	CategoryRBACForbidden       Category = "rbac_forbidden"
	CategoryCertificateNotReady Category = "certificate_not_ready"

	// control plane / operators / cluster infra
	CategoryNodeNotReady          Category = "node_not_ready"
	CategoryOperatorConditionFail Category = "operator_condition_failed"
	CategoryGitOpsSyncFailed      Category = "gitops_sync_failed"
	CategoryWebhookBackendDown    Category = "webhook_backend_down"
)

// Group is the coarse rollup over categories — the ~10 buckets used for the UI
// facet + summary strip. Derived from Category via categoryGroup; never set
// independently.
type Group string

const (
	GroupUnknown       Group = "unknown"
	GroupScheduling    Group = "scheduling"
	GroupStartup       Group = "startup"
	GroupRuntime       Group = "runtime"
	GroupConfiguration Group = "configuration"
	GroupNetworking    Group = "networking"
	GroupStorage       Group = "storage"
	GroupScaling       Group = "scaling"
	GroupSecurity      Group = "security"
	GroupControlPlane  Group = "control_plane"
	GroupApplication   Group = "application"
)

// categoryGroup is the fixed category→group rollup. Server-side source of
// truth: the UI renders whatever group the server reports, so adding a
// category needs no frontend change.
var categoryGroup = map[Category]Group{
	CategoryUnschedulable:            GroupScheduling,
	CategoryQuotaExceeded:            GroupScheduling,
	CategoryAdmissionWebhookBlocking: GroupScheduling,
	CategoryImagePullFailed:          GroupStartup,
	CategoryContainerWaiting:         GroupStartup,
	CategoryInitContainerFailed:      GroupStartup,
	CategoryCrashLoop:                GroupRuntime,
	CategoryOOMKilled:                GroupRuntime,
	CategoryLivenessProbeFail:        GroupRuntime,
	CategoryReadinessFailed:          GroupRuntime,
	CategoryWorkloadDegraded:         GroupRuntime,
	CategoryJobFailed:                GroupRuntime,
	CategoryCronJobFailed:            GroupRuntime,
	CategoryMissingConfigRef:         GroupConfiguration,
	CategoryServiceNoEndpoints:       GroupNetworking,
	CategoryIngressBackendMissing:    GroupNetworking,
	CategoryDNSFailure:               GroupNetworking,
	CategoryNetworkPolicyBlock:       GroupNetworking,
	CategoryPVCPending:               GroupStorage,
	CategoryPVCLost:                  GroupStorage,
	CategoryVolumeMountFailed:        GroupStorage,
	CategoryRolloutStalled:           GroupScaling,
	CategoryHPALimitedOrFailed:       GroupScaling,
	CategoryRBACForbidden:            GroupSecurity,
	CategoryCertificateNotReady:      GroupSecurity,
	CategoryNodeNotReady:             GroupControlPlane,
	CategoryOperatorConditionFail:    GroupControlPlane,
	CategoryGitOpsSyncFailed:         GroupControlPlane,
	CategoryWebhookBackendDown:       GroupControlPlane,
}

// GroupOf returns the rollup group for a category. Unknown/unmapped → unknown.
func GroupOf(c Category) Group {
	if g, ok := categoryGroup[c]; ok {
		return g
	}
	return GroupUnknown
}

// classifyInput is the minimal signal the classifier reads. It mirrors the
// fields an Issue already carries, so wiring is a field read (no new data).
type classifyInput struct {
	Source               Source
	APIGroup             string // the resource's API group (Issue.Group)
	Kind                 string
	Reason               string
	LastTerminatedReason string
}

// Classify maps a detection signal to a user-facing Category. Pure and
// deterministic — same inputs always yield the same category, so the
// category-in-issue-id contract stays stable (no oscillation). Grounded in the
// exact reason vocabulary emitted by internal/k8s/{health,problems,scheduling,
// missing_refs}.go and internal/issues/conditions.go.
//
// Coverage is intentionally partial: signals without a clean mapping (and
// categories whose detectors don't exist yet — probes, DNS, network policy,
// real RBAC-forbidden) fall to CategoryUnknown rather than being force-fit.
func Classify(in classifyInput) Category {
	switch in.Source {
	case SourceScheduling:
		switch in.Reason {
		case "Unschedulable":
			return CategoryUnschedulable
		case "QuotaExceeded", "LimitRangeViolation":
			return CategoryQuotaExceeded
		case "PodSecurityViolation", "WebhookDenied":
			return CategoryAdmissionWebhookBlocking
		case "IPExhaustion", "SandboxCreationFailed":
			// scheduled but stuck creating the sandbox — a startup-stage stall
			return CategoryContainerWaiting
		case "VolumeMultiAttach", "VolumeAttach", "VolumeMount":
			return CategoryVolumeMountFailed
		}
		return CategoryUnknown

	case SourceMissingRef:
		// Ingress backend refs are their own category; webhook backends map to
		// the control-plane "backend down"; everything else is a dangling
		// config/resource reference.
		switch in.Reason {
		case "Missing backend Service", "Missing backend Service port":
			return CategoryIngressBackendMissing
		case "Missing webhook backend Service":
			return CategoryWebhookBackendDown
		case "Missing StorageClass":
			// the dangling ref is a StorageClass, but the user-facing effect is
			// a PVC that can't provision — surface it under storage.
			return CategoryPVCPending
		}
		// Missing PVC/ConfigMap/Secret/ServiceAccount/imagePullSecret (Pod),
		// Missing scaleTargetRef (HPA), Missing headless Service (StatefulSet),
		// Missing TLS Secret (Ingress), Missing roleRef target (RoleBinding).
		return CategoryMissingConfigRef

	case SourceCondition:
		// Generic CRD .status.conditions[]=False fallback. Discriminate the
		// well-known controller families by API group.
		g := strings.ToLower(in.APIGroup)
		switch {
		case strings.Contains(g, "cert-manager.io"):
			return CategoryCertificateNotReady
		case strings.Contains(g, "argoproj.io") || strings.Contains(g, "fluxcd"):
			return CategoryGitOpsSyncFailed
		default:
			return CategoryOperatorConditionFail
		}

	case SourceProblem:
		return classifyProblem(in)
	}
	return CategoryUnknown
}

// classifyProblem handles the broad source=problem channel (radar's per-kind
// detection). Split out to keep Classify readable.
func classifyProblem(in classifyInput) Category {
	switch in.Kind {
	case "Pod":
		// OOM can show as the current Reason or only in the last-terminated
		// reason of a now-restarting container; check both.
		if in.Reason == "OOMKilled" || in.LastTerminatedReason == "OOMKilled" {
			return CategoryOOMKilled
		}
		switch in.Reason {
		case "ImagePullBackOff", "ErrImagePull":
			return CategoryImagePullFailed
		case "CrashLoopBackOff":
			return CategoryCrashLoop
		case "CreateContainerConfigError", "Pending", "ContainerCreating":
			return CategoryContainerWaiting
		case "Error", "Failed":
			// a terminated/failed pod that isn't image-pull/OOM/scheduling —
			// closest runtime bucket is a crash.
			return CategoryCrashLoop
		}
		return CategoryUnknown

	case "Service":
		// "Selector matches no pods" / "0/N selected pods ready" /
		// "Unresolved named targetPort" all mean: no healthy endpoints.
		return CategoryServiceNoEndpoints

	case "Deployment", "StatefulSet", "DaemonSet":
		if in.Reason == "Rollout stuck" {
			return CategoryRolloutStalled
		}
		// "{avail}/{desired} available" / "{ready}/{desired} ready" /
		// "{n} unavailable" — workload under its desired healthy count. The
		// pod-level root (crashloop/image/etc.) groups under this once owner
		// grouping lands.
		return CategoryWorkloadDegraded

	case "HorizontalPodAutoscaler":
		return CategoryHPALimitedOrFailed

	case "Node":
		switch in.Reason {
		case "NotReady", "MemoryPressure", "DiskPressure", "PIDPressure":
			return CategoryNodeNotReady
		}
		// "Cordoned" is an intentional admin action, not a failure → unknown.
		return CategoryUnknown

	case "PersistentVolumeClaim":
		switch in.Reason {
		case "Pending":
			return CategoryPVCPending
		case "Lost":
			// bound volume gone — a storage failure, not unknown.
			return CategoryPVCLost
		}
		return CategoryUnknown

	case "Job":
		// DetectProblems only emits Job problems for genuine failures: a
		// JobFailed condition (reason e.g. BackoffLimitExceeded /
		// DeadlineExceeded, or the "Failed" fallback) or a stuck-active job
		// ("Running for … with no completions"). All map to the batch
		// workload-failure category rather than being discarded.
		return CategoryJobFailed

	case "CronJob":
		// "stale" (no recent run) / "never-scheduled" — the CronJob is not
		// producing the Jobs it's meant to.
		switch in.Reason {
		case "stale", "never-scheduled":
			return CategoryCronJobFailed
		}
		return CategoryUnknown
	}

	// The CAPI kinds (Cluster/Machine/MachineDeployment/…) have no category
	// yet — see the taxonomy gaps noted in ISSUES_PLAN.
	return CategoryUnknown
}
