package issues

import (
	"strings"

	"github.com/skyhook-io/radar/pkg/issuesapi"
)

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
type Category = issuesapi.Category

const (
	CategoryUnknown = issuesapi.CategoryUnknown

	// scheduling — can't get onto a node / rejected at admission
	CategoryUnschedulable            = issuesapi.CategoryUnschedulable
	CategoryQuotaExceeded            = issuesapi.CategoryQuotaExceeded
	CategoryAdmissionWebhookBlocking = issuesapi.CategoryAdmissionWebhookBlocking

	// startup — scheduled, can't start the container
	CategoryImagePullFailed     = issuesapi.CategoryImagePullFailed
	CategoryContainerWaiting    = issuesapi.CategoryContainerWaiting
	CategoryInitContainerFailed = issuesapi.CategoryInitContainerFailed

	// runtime — started, won't stay healthy
	CategoryCrashLoop         = issuesapi.CategoryCrashLoop
	CategoryOOMKilled         = issuesapi.CategoryOOMKilled
	CategoryLivenessProbeFail = issuesapi.CategoryLivenessProbeFail
	CategoryReadinessFailed   = issuesapi.CategoryReadinessFailed
	CategoryWorkloadDegraded  = issuesapi.CategoryWorkloadDegraded
	// CategoryHighRestart is a container with a high cumulative restart count
	// that is still unhealthy and churning, but isn't a classic
	// CrashLoopBackOff (e.g. readiness-probe churn with clean exits). Sits
	// alongside CategoryCrashLoop rather than replacing it.
	CategoryHighRestart = issuesapi.CategoryHighRestart
	// batch workload failures (Job/CronJob) — runtime-stage failures of
	// one-shot / scheduled workloads.
	CategoryJobFailed     = issuesapi.CategoryJobFailed
	CategoryCronJobFailed = issuesapi.CategoryCronJobFailed

	// configuration
	CategoryMissingConfigRef   = issuesapi.CategoryMissingConfigRef
	CategoryPDBBlocksEvictions = issuesapi.CategoryPDBBlocksEvictions

	// networking
	CategoryServiceNoEndpoints    = issuesapi.CategoryServiceNoEndpoints
	CategoryIngressBackendMissing = issuesapi.CategoryIngressBackendMissing
	CategoryDNSFailure            = issuesapi.CategoryDNSFailure
	CategoryNetworkPolicyBlock    = issuesapi.CategoryNetworkPolicyBlock

	// storage
	CategoryPVCPending        = issuesapi.CategoryPVCPending
	CategoryPVCLost           = issuesapi.CategoryPVCLost
	CategoryVolumeMountFailed = issuesapi.CategoryVolumeMountFailed
	// CategoryVolumeAccessModeConflict is a config-level storage fault: a
	// multi-replica Deployment mounts a ReadWriteOnce volume, which only one
	// node can attach — surplus replicas can never start. Distinct from
	// volume_mount_failed (the observed attach error) because this is the
	// proactive root cause, detected from spec, before/independent of the symptom.
	CategoryVolumeAccessModeConflict = issuesapi.CategoryVolumeAccessModeConflict

	// scaling / rollout
	CategoryRolloutStalled     = issuesapi.CategoryRolloutStalled
	CategoryHPALimitedOrFailed = issuesapi.CategoryHPALimitedOrFailed

	// security
	CategoryRBACForbidden        = issuesapi.CategoryRBACForbidden
	CategoryCertificateNotReady  = issuesapi.CategoryCertificateNotReady
	CategoryPodSecurityViolation = issuesapi.CategoryPodSecurityViolation

	// control plane / operators / cluster infra
	CategoryNodeNotReady          = issuesapi.CategoryNodeNotReady
	CategoryOperatorConditionFail = issuesapi.CategoryOperatorConditionFail
	CategoryGitOpsSyncFailed      = issuesapi.CategoryGitOpsSyncFailed
	CategoryWebhookBackendDown    = issuesapi.CategoryWebhookBackendDown
	CategoryControlPlaneNotReady  = issuesapi.CategoryControlPlaneNotReady
	CategoryMachineNotReady       = issuesapi.CategoryMachineNotReady
)

// CategoryGroup is the coarse rollup over categories — the ~10 buckets used for
// the UI facet + summary strip. Derived from Category via categoryGroup; never
// set independently. Named distinctly from Issue.Group (the resource's API
// group) to avoid the two colliding.
type CategoryGroup = issuesapi.CategoryGroup

const (
	GroupUnknown       = issuesapi.GroupUnknown
	GroupScheduling    = issuesapi.GroupScheduling
	GroupStartup       = issuesapi.GroupStartup
	GroupRuntime       = issuesapi.GroupRuntime
	GroupConfiguration = issuesapi.GroupConfiguration
	GroupNetworking    = issuesapi.GroupNetworking
	GroupStorage       = issuesapi.GroupStorage
	GroupScaling       = issuesapi.GroupScaling
	GroupSecurity      = issuesapi.GroupSecurity
	GroupControlPlane  = issuesapi.GroupControlPlane
)

// categoryGroup is the fixed category→group rollup. Server-side source of
// truth: the UI renders whatever group the server reports, so adding a
// category needs no frontend change.
var categoryGroup = map[Category]CategoryGroup{
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
	CategoryHighRestart:              GroupRuntime,
	CategoryJobFailed:                GroupRuntime,
	CategoryCronJobFailed:            GroupRuntime,
	CategoryMissingConfigRef:         GroupConfiguration,
	CategoryPDBBlocksEvictions:       GroupConfiguration,
	CategoryServiceNoEndpoints:       GroupNetworking,
	CategoryIngressBackendMissing:    GroupNetworking,
	CategoryDNSFailure:               GroupNetworking,
	CategoryNetworkPolicyBlock:       GroupNetworking,
	CategoryPVCPending:               GroupStorage,
	CategoryPVCLost:                  GroupStorage,
	CategoryVolumeMountFailed:        GroupStorage,
	CategoryVolumeAccessModeConflict: GroupStorage,
	CategoryRolloutStalled:           GroupScaling,
	CategoryHPALimitedOrFailed:       GroupScaling,
	CategoryRBACForbidden:            GroupSecurity,
	CategoryCertificateNotReady:      GroupSecurity,
	CategoryPodSecurityViolation:     GroupSecurity,
	CategoryNodeNotReady:             GroupControlPlane,
	CategoryOperatorConditionFail:    GroupControlPlane,
	CategoryGitOpsSyncFailed:         GroupControlPlane,
	CategoryWebhookBackendDown:       GroupControlPlane,
	CategoryControlPlaneNotReady:     GroupControlPlane,
	CategoryMachineNotReady:          GroupControlPlane,
}

// GroupOf returns the rollup group for a category. Unknown/unmapped → unknown.
func GroupOf(c Category) CategoryGroup {
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
// exact reason vocabulary emitted by the detector layer in internal/k8s and the
// CRD-condition source in internal/issues.
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
		case "PodSecurityViolation":
			// Pod Security admission (built-in PSA) is NOT a webhook — don't
			// mislabel it as such.
			return CategoryPodSecurityViolation
		case "WebhookDenied":
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
			// Only a Certificate is "certificate not ready". Issuer/ClusterIssuer/
			// Order/Challenge are different objects — a not-ready Issuer is a
			// control-plane condition, not a certificate problem.
			if in.Kind == "Certificate" {
				return CategoryCertificateNotReady
			}
			return CategoryOperatorConditionFail
		case strings.Contains(g, "argoproj.io"):
			switch in.Kind {
			case "Application":
				return CategoryGitOpsSyncFailed
			case "Rollout":
				// Progressive-delivery workload, not a sync operation.
				return CategoryRolloutStalled
			}
			// AppProject/ApplicationSet/etc. are control-plane CRDs, not a sync.
			return CategoryOperatorConditionFail
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
		case "ImagePullBackOff", "ErrImagePull", "InvalidImageName", "ImageInspectError":
			return CategoryImagePullFailed
		case "CrashLoopBackOff":
			return CategoryCrashLoop
		case "HighRestartCount":
			return CategoryHighRestart
		case "CreateContainerConfigError", "CreateContainerError", "RunContainerError", "Pending", "ContainerCreating":
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
		// Stable reason literal emitted by sharedRWOVolumeConflicts —
		// a multi-replica Deployment mounting a ReadWriteOnce volume.
		if in.Reason == "ReadWriteOnce volume shared across replicas" {
			return CategoryVolumeAccessModeConflict
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

	case "PodDisruptionBudget":
		if in.Reason == "Voluntary evictions blocked" {
			return CategoryPDBBlocksEvictions
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

	case "Application":
		// ArgoCD Application health/sync failure from DetectGitOpsProblems.
		// Gate on group so a same-named CRD from another controller can't be
		// force-fit into the GitOps bucket.
		if strings.Contains(strings.ToLower(in.APIGroup), "argoproj.io") {
			return CategoryGitOpsSyncFailed
		}
		return CategoryUnknown

	case "Kustomization", "HelmRelease":
		// Flux reconciler failure from DetectGitOpsProblems.
		g := strings.ToLower(in.APIGroup)
		if g == "kustomize.toolkit.fluxcd.io" || g == "helm.toolkit.fluxcd.io" {
			return CategoryGitOpsSyncFailed
		}
		return CategoryUnknown

	case "Cluster", "KubeadmControlPlane":
		// Cluster API control plane (cluster.x-k8s.io / controlplane.
		// cluster.x-k8s.io). Gate on the group so a same-named CRD from
		// another controller can't be force-fit.
		if strings.Contains(strings.ToLower(in.APIGroup), "cluster.x-k8s.io") {
			return CategoryControlPlaneNotReady
		}
		return CategoryUnknown

	case "Machine", "MachineDeployment", "MachineHealthCheck":
		// Cluster API machine layer — node-backing infra, distinct from the
		// control plane it forms.
		if strings.Contains(strings.ToLower(in.APIGroup), "cluster.x-k8s.io") {
			return CategoryMachineNotReady
		}
		return CategoryUnknown
	}

	return CategoryUnknown
}
