package k8s

import (
	"fmt"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// Detection is a transport-neutral raw operational finding emitted by the
// detector layer — a failing Deployment, a crashlooping pod, a dangling
// reference, a degraded Argo app. It carries NO classification, grouping, or
// ranking; those are the issues layer's job.
//
// detection.go and its sibling detectors (health.go, missing_refs.go,
// scheduling.go, capi.go, gitops.go) ARE that detector layer: each reads the
// live cache and returns []Detection. internal/issues is the layer ABOVE them —
// it classifies each Detection into a symptom Category, resolves its Subject,
// and folds replica fan-out into the public grouped Issue model; the home
// dashboard and MCP also consume Detections directly. So this is the bottom
// tier of the pipeline (detectors → classify/group → render) — the generalized
// successor to the v0 standalone "problems" feature, NOT a parallel surface to
// issues.
type Detection struct {
	Kind            string
	Namespace       string
	Name            string
	Group           string // API group for CRD disambiguation (e.g., "cluster.x-k8s.io")
	Severity        string // "critical", "high", or "medium"
	Reason          string
	Message         string
	Age             string // human-readable
	AgeSeconds      int64  // for sorting
	Duration        string // how long the problem has persisted
	DurationSeconds int64
	// RestartCount + LastTerminatedReason are populated for Pod problems where
	// the kubelet has recorded crash data. Together they answer the two
	// questions an agent needs about a CrashLoopBackOff in one read:
	// chronic-vs-acute (RestartCount: 2 vs 2000) and what kind of failure
	// (Reason: OOMKilled / Error / Completed — disambiguates memory pressure
	// from app bug from misconfigured-as-long-running). Zero / empty values
	// mean either non-Pod problem or no crash data on this Pod yet.
	RestartCount         int32
	LastTerminatedReason string
	// OwnerKind + OwnerName name the topmost stable controller of a Pod
	// problem (Pod→Deployment, not the intermediate ReplicaSet), resolved
	// via topOwnerForPod when the Pod is detected. Empty for non-Pod and
	// standalone-pod problems — those are their own subject. Lets the
	// issues layer group member pods under one workload without re-walking
	// ownerReferences.
	OwnerGroup string
	OwnerKind  string
	OwnerName  string
}

// podOwnerKindName resolves a Pod's topmost stable controller for issue
// grouping (Pod→Deployment, not ReplicaSet), returning empty strings for
// standalone pods. Thin wrapper over topOwnerForPod so the pod
// problem-emission sites stay terse.
func podOwnerKindName(cache *ResourceCache, pod *corev1.Pod) (group, kind, name string) {
	if to := topOwnerForPodResolved(cache, pod); to != nil {
		return to.Group, to.Kind, to.Name
	}
	return "", "", ""
}

// DetectProblems scans workloads in cache and returns detected problems.
// Covers: Deployments, StatefulSets, DaemonSets, HPAs, CronJobs, Nodes.
// Does NOT include pods (consumers handle pod problems differently).
// namespace="" scans all namespaces.
func DetectProblems(cache *ResourceCache, namespace string) []Detection {
	var problems []Detection
	now := time.Now()

	// Deployment problems: unavailableReplicas > 0
	if depLister := cache.Deployments(); depLister != nil {
		var deps []*appsv1.Deployment
		if namespace != "" {
			deps, _ = depLister.Deployments(namespace).List(labels.Everything())
		} else {
			deps, _ = depLister.List(labels.Everything())
		}
		for _, d := range deps {
			if d.Status.UnavailableReplicas > 0 {
				ageDur := now.Sub(d.CreationTimestamp.Time)
				durDur := ageDur // fallback to creation time
				for _, cond := range d.Status.Conditions {
					if cond.Type == appsv1.DeploymentAvailable && cond.Status == "False" && !cond.LastTransitionTime.IsZero() {
						durDur = now.Sub(cond.LastTransitionTime.Time)
						break
					}
				}
				problems = append(problems, Detection{
					Kind:            "Deployment",
					Namespace:       d.Namespace,
					Name:            d.Name,
					Group:           "apps",
					Severity:        "critical",
					Reason:          fmt.Sprintf("%d/%d available", d.Status.AvailableReplicas, d.Status.Replicas),
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(durDur),
					DurationSeconds: int64(durDur.Seconds()),
				})
			}
			// Stuck rollout: ProgressDeadlineExceeded
			for _, cond := range d.Status.Conditions {
				if cond.Type == appsv1.DeploymentProgressing && cond.Status == "False" && cond.Reason == "ProgressDeadlineExceeded" {
					durDur := now.Sub(d.CreationTimestamp.Time)
					if !cond.LastTransitionTime.IsZero() {
						durDur = now.Sub(cond.LastTransitionTime.Time)
					}
					problems = append(problems, Detection{
						Kind:            "Deployment",
						Namespace:       d.Namespace,
						Name:            d.Name,
						Group:           "apps",
						Severity:        "critical",
						Reason:          "Rollout stuck",
						Message:         cond.Message,
						Age:             FormatAge(now.Sub(d.CreationTimestamp.Time)),
						AgeSeconds:      int64(now.Sub(d.CreationTimestamp.Time).Seconds()),
						Duration:        FormatAge(durDur),
						DurationSeconds: int64(durDur.Seconds()),
					})
					break
				}
			}
		}
	}

	// StatefulSet problems: readyReplicas < replicas
	if ssLister := cache.StatefulSets(); ssLister != nil {
		var ssets []*appsv1.StatefulSet
		if namespace != "" {
			ssets, _ = ssLister.StatefulSets(namespace).List(labels.Everything())
		} else {
			ssets, _ = ssLister.List(labels.Everything())
		}
		for _, ss := range ssets {
			if ss.Status.ReadyReplicas < ss.Status.Replicas {
				ageDur := now.Sub(ss.CreationTimestamp.Time)
				problems = append(problems, Detection{
					Kind:            "StatefulSet",
					Namespace:       ss.Namespace,
					Name:            ss.Name,
					Group:           "apps",
					Severity:        "critical",
					Reason:          fmt.Sprintf("%d/%d ready", ss.Status.ReadyReplicas, ss.Status.Replicas),
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
			}
		}
	}

	// DaemonSet problems: numberUnavailable > 0
	if dsLister := cache.DaemonSets(); dsLister != nil {
		var dsets []*appsv1.DaemonSet
		if namespace != "" {
			dsets, _ = dsLister.DaemonSets(namespace).List(labels.Everything())
		} else {
			dsets, _ = dsLister.List(labels.Everything())
		}
		for _, ds := range dsets {
			if ds.Status.NumberUnavailable > 0 {
				ageDur := now.Sub(ds.CreationTimestamp.Time)
				problems = append(problems, Detection{
					Kind:            "DaemonSet",
					Namespace:       ds.Namespace,
					Name:            ds.Name,
					Group:           "apps",
					Severity:        "critical",
					Reason:          fmt.Sprintf("%d unavailable", ds.Status.NumberUnavailable),
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
			}
		}
	}

	podsByNamespace := listPodsByNamespace(cache, namespace)

	// Pod problems: high-signal container waiting/terminated states, old
	// Pending pods, and restart-heavy pods. These are useful direct pointers
	// even when a controller-level problem also exists.
	for _, pods := range podsByNamespace {
		for _, pod := range pods {
			health := ClassifyPodHealth(pod, now)
			if health == "healthy" {
				continue
			}
			// Unschedulable pods are owned by the scheduling source, which
			// names the offending constraint instead of a bare "Pending".
			if IsPodUnschedulable(pod) {
				continue
			}
			ageDur := now.Sub(pod.CreationTimestamp.Time)
			severity := "high"
			if health == "error" {
				severity = "critical"
			}
			restartCount, lastTermReason := PodRestartContext(pod)
			ownerGroup, ownerKind, ownerName := podOwnerKindName(cache, pod)
			problems = append(problems, Detection{
				Kind:                 "Pod",
				Namespace:            pod.Namespace,
				Name:                 pod.Name,
				Severity:             severity,
				Reason:               PodProblemReason(pod),
				Age:                  FormatAge(ageDur),
				AgeSeconds:           int64(ageDur.Seconds()),
				Duration:             FormatAge(ageDur),
				DurationSeconds:      int64(ageDur.Seconds()),
				RestartCount:         restartCount,
				LastTerminatedReason: lastTermReason,
				OwnerGroup:           ownerGroup,
				OwnerKind:            ownerKind,
				OwnerName:            ownerName,
			})
		}
	}

	// Service problems: routing health that workload .status often misses.
	// EndpointSlice would be the strongest source for realized backend state,
	// but the typed cache intentionally does not watch noisy endpoint resources
	// today. Use selector -> Pod readiness here, and keep the targetPort check
	// conservative: only named targetPorts are flagged as unresolved.
	if svcLister := cache.Services(); svcLister != nil {
		var services []*corev1.Service
		if namespace != "" {
			services, _ = svcLister.Services(namespace).List(labels.Everything())
		} else {
			services, _ = svcLister.List(labels.Everything())
		}
		for _, svc := range services {
			if svc.Spec.Type == corev1.ServiceTypeExternalName || len(svc.Spec.Selector) == 0 {
				continue
			}
			selected := podsMatchingService(svc, podsByNamespace[svc.Namespace])
			ageDur := now.Sub(svc.CreationTimestamp.Time)
			if len(selected) == 0 {
				// Warning, not critical: a Service with zero matching pods
				// is often intentional (scaled-to-zero workload, dormant
				// staging environment, just-deployed Service waiting for
				// its workload to apply). The "0/N selected but 0 ready"
				// case below stays critical — that's a real routing break
				// because the workload is up but unhealthy.
				reason := "Selector matches no pods"
				message := selectorMessage(svc.Spec.Selector)
				// Distinguish the deliberate scale-to-0 case (managed-prometheus
				// components disabled, antrea on Autopilot, dormant staging) from
				// a genuinely orphaned selector. Both stay warning, but an honest
				// reason keeps the row from reading as a routing fault.
				if scaledToZeroBackingWorkload(cache, svc) {
					reason = "Backing workload scaled to 0"
					message = "selector matches a Deployment/StatefulSet that is intentionally scaled to 0 replicas"
				}
				problems = append(problems, Detection{
					Kind:            "Service",
					Namespace:       svc.Namespace,
					Name:            svc.Name,
					Severity:        "warning",
					Reason:          reason,
					Message:         message,
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
				continue
			}
			ready := 0
			for _, pod := range selected {
				if isPodReadyForProblem(pod) {
					ready++
				}
			}
			if ready == 0 {
				problems = append(problems, Detection{
					Kind:            "Service",
					Namespace:       svc.Namespace,
					Name:            svc.Name,
					Severity:        "critical",
					Reason:          fmt.Sprintf("0/%d selected pods ready", len(selected)),
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
			}
			if missing := unresolvedNamedTargetPorts(svc, selected); len(missing) > 0 {
				problems = append(problems, Detection{
					Kind:            "Service",
					Namespace:       svc.Namespace,
					Name:            svc.Name,
					Severity:        "high",
					Reason:          fmt.Sprintf("Unresolved named targetPort: %s", strings.Join(missing, ", ")),
					Message:         "No selected pod declares a container port with this name",
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
			}
		}
	}

	// HPA problems
	if hpaLister := cache.HorizontalPodAutoscalers(); hpaLister != nil {
		var hpas []*autoscalingv2.HorizontalPodAutoscaler
		if namespace != "" {
			hpas, _ = hpaLister.HorizontalPodAutoscalers(namespace).List(labels.Everything())
		} else {
			hpas, _ = hpaLister.List(labels.Everything())
		}
		for _, hp := range DetectHPAProblems(hpas) {
			// "cannot-scale" is critical (HPA inert; workload's scaling
			// guarantees silently broken). "maxed" stays medium (HPA is
			// working; signal is that the ceiling was hit, which may or
			// may not be a problem depending on intent).
			severity := "medium"
			if hp.Problem == "cannot-scale" {
				severity = "critical"
			}
			problems = append(problems, Detection{
				Kind:      "HorizontalPodAutoscaler",
				Namespace: hp.Namespace,
				Name:      hp.Name,
				Group:     "autoscaling",
				Severity:  severity,
				Reason:    hp.Problem,
				Message:   hp.Reason,
			})
		}
	}

	// CronJob problems
	if cjLister := cache.CronJobs(); cjLister != nil {
		var cronjobs []*batchv1.CronJob
		if namespace != "" {
			cronjobs, _ = cjLister.CronJobs(namespace).List(labels.Everything())
		} else {
			cronjobs, _ = cjLister.List(labels.Everything())
		}
		for _, cp := range DetectCronJobProblems(cronjobs) {
			problems = append(problems, Detection{
				Kind:      "CronJob",
				Namespace: cp.Namespace,
				Name:      cp.Name,
				Group:     "batch",
				Severity:  "medium",
				Reason:    cp.Problem,
				Message:   cp.Reason,
			})
		}
	}

	// Node problems (cluster-scoped, not filtered by namespace)
	if nodeLister := cache.Nodes(); nodeLister != nil {
		nodes, _ := nodeLister.List(labels.Everything())
		for _, np := range DetectNodeProblems(nodes) {
			ageDur := time.Duration(0)
			for _, n := range nodes {
				if n.Name == np.NodeName {
					ageDur = now.Sub(n.CreationTimestamp.Time)
					break
				}
			}
			problems = append(problems, Detection{
				Kind:       "Node",
				Name:       np.NodeName,
				Severity:   np.Severity,
				Reason:     np.Problem,
				Message:    np.Reason,
				Age:        FormatAge(ageDur),
				AgeSeconds: int64(ageDur.Seconds()),
			})
		}
	}

	// PVC problems: stuck in Pending phase or Lost bound volume.
	if pvcLister := cache.PersistentVolumeClaims(); pvcLister != nil {
		var pvcs []*corev1.PersistentVolumeClaim
		if namespace != "" {
			pvcs, _ = pvcLister.PersistentVolumeClaims(namespace).List(labels.Everything())
		} else {
			pvcs, _ = pvcLister.List(labels.Everything())
		}
		for _, pvc := range pvcs {
			ageDur := now.Sub(pvc.CreationTimestamp.Time)
			if pvc.Status.Phase == corev1.ClaimLost {
				problems = append(problems, Detection{
					Kind:            "PersistentVolumeClaim",
					Namespace:       pvc.Namespace,
					Name:            pvc.Name,
					Severity:        "critical",
					Reason:          "Lost",
					Message:         "PVC has lost its bound volume",
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(ageDur),
					DurationSeconds: int64(ageDur.Seconds()),
				})
				continue
			}
			if pvc.Status.Phase == corev1.ClaimPending {
				if ageDur > 5*time.Minute {
					problems = append(problems, Detection{
						Kind:            "PersistentVolumeClaim",
						Namespace:       pvc.Namespace,
						Name:            pvc.Name,
						Severity:        "high",
						Reason:          "Pending",
						Age:             FormatAge(ageDur),
						AgeSeconds:      int64(ageDur.Seconds()),
						Duration:        FormatAge(ageDur),
						DurationSeconds: int64(ageDur.Seconds()),
					})
				}
			}
		}
	}

	// Job problems: stuck active (running > 1h with no completions)
	if jobLister := cache.Jobs(); jobLister != nil {
		var jobs []*batchv1.Job
		if namespace != "" {
			jobs, _ = jobLister.Jobs(namespace).List(labels.Everything())
		} else {
			jobs, _ = jobLister.List(labels.Everything())
		}
		for _, job := range jobs {
			ageDur := now.Sub(job.CreationTimestamp.Time)
			if cond := failedJobCondition(job); cond != nil {
				durDur := ageDur
				if !cond.LastTransitionTime.IsZero() {
					durDur = now.Sub(cond.LastTransitionTime.Time)
				}
				reason := cond.Reason
				if reason == "" {
					reason = "Failed"
				}
				problems = append(problems, Detection{
					Kind:            "Job",
					Namespace:       job.Namespace,
					Name:            job.Name,
					Group:           "batch",
					Severity:        "critical",
					Reason:          reason,
					Message:         cond.Message,
					Age:             FormatAge(ageDur),
					AgeSeconds:      int64(ageDur.Seconds()),
					Duration:        FormatAge(durDur),
					DurationSeconds: int64(durDur.Seconds()),
				})
				continue
			}
			if job.Status.Active > 0 && job.Status.Succeeded == 0 && job.Status.Failed == 0 {
				if ageDur > time.Hour {
					problems = append(problems, Detection{
						Kind:            "Job",
						Namespace:       job.Namespace,
						Name:            job.Name,
						Group:           "batch",
						Severity:        "high",
						Reason:          fmt.Sprintf("Running for %s with no completions", FormatAge(ageDur)),
						Age:             FormatAge(ageDur),
						AgeSeconds:      int64(ageDur.Seconds()),
						Duration:        FormatAge(ageDur),
						DurationSeconds: int64(ageDur.Seconds()),
					})
				}
			}
		}
	}

	return problems
}

func listPodsByNamespace(cache *ResourceCache, namespace string) map[string][]*corev1.Pod {
	out := make(map[string][]*corev1.Pod)
	if cache == nil || cache.Pods() == nil {
		return out
	}
	var pods []*corev1.Pod
	if namespace != "" {
		pods, _ = cache.Pods().Pods(namespace).List(labels.Everything())
	} else {
		pods, _ = cache.Pods().List(labels.Everything())
	}
	for _, pod := range pods {
		out[pod.Namespace] = append(out[pod.Namespace], pod)
	}
	return out
}

func podsMatchingService(svc *corev1.Service, pods []*corev1.Pod) []*corev1.Pod {
	if svc == nil || len(svc.Spec.Selector) == 0 {
		return nil
	}
	selector := labels.SelectorFromSet(labels.Set(svc.Spec.Selector))
	out := make([]*corev1.Pod, 0, len(pods))
	for _, pod := range pods {
		if selector.Matches(labels.Set(pod.Labels)) {
			out = append(out, pod)
		}
	}
	return out
}

// scaledToZeroBackingWorkload reports whether the Service's selector matches a
// Deployment or StatefulSet that is intentionally scaled to 0 replicas. Such a
// Service has no endpoints by design (a disabled managed component, a dormant
// environment), which is a different — benign — state than a selector that
// matches nothing in the cluster. Only called on the rare zero-endpoint branch,
// so the per-Service workload scan is not a hot path.
func scaledToZeroBackingWorkload(cache *ResourceCache, svc *corev1.Service) bool {
	if cache == nil || len(svc.Spec.Selector) == 0 {
		return false
	}
	sel := labels.SelectorFromSet(labels.Set(svc.Spec.Selector))
	if dl := cache.Deployments(); dl != nil {
		deps, _ := dl.Deployments(svc.Namespace).List(labels.Everything())
		for _, d := range deps {
			if d.Spec.Replicas != nil && *d.Spec.Replicas == 0 && sel.Matches(labels.Set(d.Spec.Template.Labels)) {
				return true
			}
		}
	}
	if sl := cache.StatefulSets(); sl != nil {
		stss, _ := sl.StatefulSets(svc.Namespace).List(labels.Everything())
		for _, s := range stss {
			if s.Spec.Replicas != nil && *s.Spec.Replicas == 0 && sel.Matches(labels.Set(s.Spec.Template.Labels)) {
				return true
			}
		}
	}
	return false
}

func isPodReadyForProblem(pod *corev1.Pod) bool {
	if pod == nil {
		return false
	}
	for _, cond := range pod.Status.Conditions {
		if cond.Type == corev1.PodReady {
			return cond.Status == corev1.ConditionTrue
		}
	}
	return false
}

func unresolvedNamedTargetPorts(svc *corev1.Service, pods []*corev1.Pod) []string {
	if svc == nil || len(pods) == 0 {
		return nil
	}
	declared := make(map[string]bool)
	for _, pod := range pods {
		for _, container := range pod.Spec.InitContainers {
			addNamedContainerPorts(declared, container.Ports)
		}
		for _, container := range pod.Spec.Containers {
			addNamedContainerPorts(declared, container.Ports)
		}
	}
	var missing []string
	seen := make(map[string]bool)
	for _, port := range svc.Spec.Ports {
		if port.TargetPort.Type != intstr.String || port.TargetPort.StrVal == "" {
			continue
		}
		name := port.TargetPort.StrVal
		if declared[name] || seen[name] {
			continue
		}
		seen[name] = true
		missing = append(missing, name)
	}
	return missing
}

func addNamedContainerPorts(dst map[string]bool, ports []corev1.ContainerPort) {
	for _, port := range ports {
		if port.Name != "" {
			dst[port.Name] = true
		}
	}
}

func selectorMessage(selector map[string]string) string {
	if len(selector) == 0 {
		return ""
	}
	parts := make([]string, 0, len(selector))
	for k, v := range selector {
		parts = append(parts, k+"="+v)
	}
	sort.Strings(parts)
	return "selector: " + strings.Join(parts, ", ")
}

func failedJobCondition(job *batchv1.Job) *batchv1.JobCondition {
	if job == nil {
		return nil
	}
	for i := range job.Status.Conditions {
		cond := &job.Status.Conditions[i]
		if cond.Type == batchv1.JobFailed && cond.Status == corev1.ConditionTrue {
			return cond
		}
	}
	return nil
}

// DetectCAPIProblems scans Cluster API resources for problems.
// Checks both status.phase and the rich condition system (Ready, InfrastructureReady,
// ControlPlaneReady, BootstrapReady, NodeHealthy, TopologyReconciled).
// Returns nil if CAPI is not installed in the cluster.
