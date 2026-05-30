package k8s

import (
	"fmt"
	"strings"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// crashLoopReason is the canonical reason a stable-crashloop pod emits,
// independent of the kubelet's instantaneous container phase. Folding to one
// reason here keeps issues/category.Classify returning `crashloop` across the
// Waiting→Running→Waiting oscillation (and the issue_id stable, since category
// is hashed into it).
const crashLoopReason = "CrashLoopBackOff"

// highRestartReason is the canonical reason for a container that is actively
// thrashing — a high cumulative restart count while still unhealthy and
// churning — but is NOT a classic CrashLoopBackOff (its restarts come from
// e.g. failing readiness probes with clean exits, so isStableCrashLoop's
// crash-class guard doesn't fire). Naming it keeps the row out of the
// `unknown` catch-all (see PodProblemReason / category.Classify).
const highRestartReason = "HighRestartCount"

// highRestartThreshold is the cumulative per-container RestartCount above which
// a still-unhealthy container is treated as actively thrashing.
const highRestartThreshold = 3

// isStableCrashLoop reports whether a container is in an ACTIVE crashloop: it
// has restarted with a crash-class last termination (CrashLoopBackOff / generic
// Error / non-zero exit), AND it has not since recovered. It reads the stable
// history fields (RestartCount + LastTerminationState) rather than the
// instantaneous State the kubelet flips between polls — so a real loop's brief
// "Running" blip doesn't downgrade the verdict — but it must NOT fire on a
// container that crashed once and is now running healthily: RestartCount and
// LastTerminationState persist for the life of the container, so without the
// recovery guard below a pod that restarted once at startup would read as a
// crashloop forever. Two recovery signals clear it: a container Running
// continuously past the kubelet's max CrashLoopBackOff backoff (5m) has outlived
// the loop, and a container whose CURRENT state is a clean exit (Terminated,
// exit 0) has succeeded — the common init-container-retries-then-completes case,
// whose failed prior attempt lingers in LastTerminationState. OOMKilled is
// intentionally excluded — it has its own category/severity path upstream.
func isStableCrashLoop(cs *corev1.ContainerStatus, now time.Time) bool {
	if cs.RestartCount == 0 {
		return false
	}
	if r := cs.State.Running; r != nil && !r.StartedAt.IsZero() && now.Sub(r.StartedAt.Time) > 5*time.Minute {
		return false
	}
	if term := cs.State.Terminated; term != nil && term.ExitCode == 0 {
		return false
	}
	t := cs.LastTerminationState.Terminated
	if t == nil {
		return false
	}
	switch t.Reason {
	case "OOMKilled":
		// Memory pressure is classified separately (CategoryOOMKilled); don't
		// fold it into the generic crashloop bucket.
		return false
	case "CrashLoopBackOff", "Error":
		return true
	}
	// A non-zero exit code with no special reason is still a crash — the app
	// died and the kubelet is restarting it.
	return t.ExitCode != 0
}

// podHasStableCrashLoop reports whether any main or init container is in a
// stable crashloop (see isStableCrashLoop).
func podHasStableCrashLoop(pod *corev1.Pod, now time.Time) bool {
	for i := range pod.Status.ContainerStatuses {
		if isStableCrashLoop(&pod.Status.ContainerStatuses[i], now) {
			return true
		}
	}
	for i := range pod.Status.InitContainerStatuses {
		if isStableCrashLoop(&pod.Status.InitContainerStatuses[i], now) {
			return true
		}
	}
	return false
}

// restartedRecently reports whether a container's most recent termination
// finished within the given window — i.e. it is still actively churning, not a
// container that crashed long ago and has since gone quiet (the laptop-sleep /
// node-reboot artifact where RestartCount is high but every termination is days
// old).
func restartedRecently(cs *corev1.ContainerStatus, now time.Time, within time.Duration) bool {
	if t := cs.LastTerminationState.Terminated; t != nil && !t.FinishedAt.IsZero() {
		return now.Sub(t.FinishedAt.Time) <= within
	}
	return false
}

// isActivelyThrashing reports whether a container has a high cumulative restart
// count AND is currently unhealthy AND is still churning. The Ready gate is what
// clears the recovered-after-crash false positive — a pod that restarted many
// times at startup but is now Ready and stable (its RestartCount never resets)
// no longer trips this. The Waiting/recency gate clears the slept-then-woken
// node whose restarts are days old. The 5m window matches isStableCrashLoop's
// horizon so the two guards don't drift.
func isActivelyThrashing(cs *corev1.ContainerStatus, now time.Time) bool {
	if cs.RestartCount <= highRestartThreshold || cs.Ready {
		return false
	}
	if cs.State.Waiting != nil {
		return true
	}
	return restartedRecently(cs, now, 5*time.Minute)
}

// podActiveThrashContainer reports whether any main container is actively
// thrashing (see isActivelyThrashing). Init containers are excluded — a failing
// init container surfaces through podProblemReasonRaw's init walk with a
// specific reason already.
func podActiveThrashContainer(pod *corev1.Pod, now time.Time) bool {
	for i := range pod.Status.ContainerStatuses {
		if isActivelyThrashing(&pod.Status.ContainerStatuses[i], now) {
			return true
		}
	}
	return false
}

// ClassifyPodHealth determines if a pod is "healthy", "warning", or "error".
// This is the canonical implementation used by both MCP and REST dashboards.
func ClassifyPodHealth(pod *corev1.Pod, now time.Time) string {
	if pod.Status.Phase == corev1.PodSucceeded {
		return "healthy"
	}
	if pod.Status.Phase == corev1.PodFailed {
		return "error"
	}

	// Stable crashloop: a container that has restarted with a recorded crash
	// outcome is an error REGARDLESS of whether the kubelet currently reports
	// it Waiting (backing off) or Running (just restarted, about to die
	// again). Keying off the instantaneous phase here is what made severity
	// flap critical↔warning poll-to-poll; the stable history fields don't
	// oscillate, so neither does the verdict. Checked before the per-state
	// scan below so a momentary "Running" can't downgrade it.
	if podHasStableCrashLoop(pod, now) {
		return "error"
	}

	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" || reason == "CreateContainerConfigError" {
				return "error"
			}
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return "error"
		}
		if cs.LastTerminationState.Terminated != nil && cs.LastTerminationState.Terminated.Reason == "OOMKilled" {
			return "error"
		}
	}

	// Init container errors
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil {
			reason := cs.State.Waiting.Reason
			if reason == "CrashLoopBackOff" || reason == "ImagePullBackOff" || reason == "ErrImagePull" {
				return "error"
			}
		}
	}

	// Warning: pods pending for more than 5 minutes
	if pod.Status.Phase == corev1.PodPending {
		if now.Sub(pod.CreationTimestamp.Time) > 5*time.Minute {
			return "warning"
		}
		return "healthy"
	}

	// Warning: a container actively thrashing — high cumulative restarts AND
	// currently not ready AND still churning. A plain RestartCount>N check
	// also fires on a pod that crashed at startup and has since been Ready
	// for hours (RestartCount never resets), and on nodes whose restarts are
	// stale laptop-sleep / reboot artifacts — both are healthy now. The
	// thrash gate (not-ready + recent/Waiting) excludes those.
	if podActiveThrashContainer(pod, now) {
		return "warning"
	}

	return "healthy"
}

// PodRestartContext extracts crash-debugging context from a pod's container
// statuses: total restarts across main + init containers, and the kubelet-
// recorded reason for the most recent container termination (OOMKilled,
// Error, Completed, etc.). Used by Pod problem rows so agents can tell
// chronic-vs-acute (high RestartCount = old) and pick the right next call
// (OOMKilled → memory analysis; Error → fetch previous logs).
func PodRestartContext(pod *corev1.Pod) (restartCount int32, lastTerminatedReason string) {
	var newestFinish time.Time
	walk := func(statuses []corev1.ContainerStatus) {
		for _, cs := range statuses {
			restartCount += cs.RestartCount
			if t := cs.LastTerminationState.Terminated; t != nil && !t.FinishedAt.IsZero() {
				if newestFinish.IsZero() || t.FinishedAt.After(newestFinish) {
					newestFinish = t.FinishedAt.Time
					lastTerminatedReason = t.Reason
				}
			}
		}
	}
	walk(pod.Status.ContainerStatuses)
	walk(pod.Status.InitContainerStatuses)
	return restartCount, lastTerminatedReason
}

// PodProblemReason returns a short reason string for a problematic pod.
// Walks init containers first because when init is failing the pod stays
// Pending and main ContainerStatuses haven't been populated yet — without
// the init check the reason would fall through to "Pending", masking
// CrashLoopBackOff / ImagePullBackOff / etc. on the actual failing
// init container.
func PodProblemReason(pod *corev1.Pod) string {
	reason := podProblemReasonRaw(pod)
	// Stable-crashloop normalization: a
	// crashlooping container oscillates Waiting("CrashLoopBackOff") → Running
	// (just restarted) → Terminated → Waiting between polls. On the "Running"
	// tick the raw walk returns a bare phase ("Running") — which
	// issues/category.classifyProblem maps to `unknown`, flipping the
	// category (and the category-hashed issue_id) mid-cycle. When the stable
	// history fields say this is a crashloop, emit the canonical reason so the
	// row's category stays `crashloop` across the whole oscillation. We only
	// override when the raw reason isn't already a more-specific, stable
	// signal (ImagePullBackOff, OOMKilled, an init failure, …) — those win.
	// time.Now() is fine here: PodProblemReason is only called on pods already
	// classified as problems (recovered pods are filtered upstream by
	// ClassifyPodHealth), so the recovery guard inside podHasStableCrashLoop
	// never fires on this path — it's just reusing the same active-crashloop test.
	now := time.Now()
	if podHasStableCrashLoop(pod, now) && isPhaseOrCrashReason(reason) {
		return crashLoopReason
	}
	// Actively-thrashing-but-not-a-classic-backoff: a container churning on
	// failed readiness probes with clean (exit 0) terminations isn't a stable
	// crashloop, so the raw walk returns a bare phase ("Running") that would
	// classify as `unknown`. Name it HighRestartCount so the row lands in a
	// runtime category instead of the catch-all. Only override a bare phase —
	// a specific reason (ImagePullBackOff, an init failure, a real crash) wins.
	if podActiveThrashContainer(pod, now) && isPhaseOnlyReason(reason) {
		return highRestartReason
	}
	return reason
}

// podProblemReasonRaw is the original phase/state walk: init containers first
// (they block the pod Pending before main ContainerStatuses populate), then
// main containers, falling back to the bare phase string.
func podProblemReasonRaw(pod *corev1.Pod) string {
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}
	return string(pod.Status.Phase)
}

// isPhaseOrCrashReason reports whether reason is one that a stable-crashloop
// override may safely replace: a bare lifecycle phase / no-op waiting state
// (the instantaneous values that flap), or an already-crash-class reason
// (so the canonical string is used consistently). A distinct, more-specific
// reason like ImagePullBackOff or OOMKilled is NOT in this set and is left
// untouched.
func isPhaseOrCrashReason(reason string) bool {
	switch reason {
	case "Running", "Pending", "Succeeded", "Failed", "Unknown", "",
		"PodInitializing", "ContainerCreating",
		"CrashLoopBackOff", "Error":
		return true
	}
	return false
}

// isPhaseOnlyReason is the narrower set the HighRestartCount override may
// replace: bare lifecycle phases / no-op waiting states only. It deliberately
// excludes the crash-class reasons (CrashLoopBackOff/Error) and terminal phases
// (Succeeded/Failed) that isPhaseOrCrashReason allows, so a thrash override can
// never clobber a real crash or terminal signal — the stable-crashloop check
// above already owns those.
func isPhaseOnlyReason(reason string) bool {
	switch reason {
	case "Running", "Pending", "Unknown", "",
		"PodInitializing", "ContainerCreating":
		return true
	}
	return false
}

// NodeHealth describes the health of a single node.
type NodeHealth struct {
	Ready         bool
	Unschedulable bool
	Pressures     []string // "MemoryPressure", "DiskPressure", "PIDPressure"
	Version       string   // kubelet version
	Reason        string   // condition message if NotReady
}

// ClassifyNodeHealth evaluates a node's conditions and spec.
func ClassifyNodeHealth(node *corev1.Node) NodeHealth {
	h := NodeHealth{
		Unschedulable: node.Spec.Unschedulable,
		Version:       node.Status.NodeInfo.KubeletVersion,
	}

	for _, cond := range node.Status.Conditions {
		switch cond.Type {
		case corev1.NodeReady:
			h.Ready = cond.Status == corev1.ConditionTrue
			if !h.Ready && cond.Message != "" {
				h.Reason = cond.Message
			}
		case corev1.NodeMemoryPressure:
			if cond.Status == corev1.ConditionTrue {
				h.Pressures = append(h.Pressures, "MemoryPressure")
			}
		case corev1.NodeDiskPressure:
			if cond.Status == corev1.ConditionTrue {
				h.Pressures = append(h.Pressures, "DiskPressure")
			}
		case corev1.NodePIDPressure:
			if cond.Status == corev1.ConditionTrue {
				h.Pressures = append(h.Pressures, "PIDPressure")
			}
		}
	}

	return h
}

// NodeProblem describes a detected problem on a node.
type NodeProblem struct {
	NodeName string `json:"nodeName"`
	Problem  string `json:"problem"`
	Reason   string `json:"reason,omitempty"`
	Severity string `json:"severity"` // "critical", "high", or "medium"
}

// DetectNodeProblems scans nodes for NotReady, Cordoned, and pressure conditions.
func DetectNodeProblems(nodes []*corev1.Node) []NodeProblem {
	var problems []NodeProblem

	for _, node := range nodes {
		h := ClassifyNodeHealth(node)

		if !h.Ready {
			reason := "NotReady"
			if h.Reason != "" {
				reason = h.Reason
			}
			problems = append(problems, NodeProblem{
				NodeName: node.Name,
				Problem:  "NotReady",
				Reason:   reason,
				Severity: "critical",
			})
		} else if h.Unschedulable {
			problems = append(problems, NodeProblem{
				NodeName: node.Name,
				Problem:  "Cordoned",
				Reason:   "SchedulingDisabled",
				Severity: "medium",
			})
		}

		for _, pressure := range h.Pressures {
			problems = append(problems, NodeProblem{
				NodeName: node.Name,
				Problem:  pressure,
				Reason:   pressure,
				Severity: "critical",
			})
		}
	}

	return problems
}

// VersionSkew describes a detected minor version skew across cluster nodes.
type VersionSkew struct {
	Versions   map[string][]string `json:"versions"` // minor version -> node names
	MinVersion string              `json:"minVersion"`
	MaxVersion string              `json:"maxVersion"`
}

// DetectVersionSkew checks for minor version differences across nodes.
// Returns nil if all nodes are on the same minor version (patch-only differences are normal).
func DetectVersionSkew(nodes []*corev1.Node) *VersionSkew {
	if len(nodes) == 0 {
		return nil
	}

	versions := make(map[string][]string) // minor version -> node names
	for _, node := range nodes {
		ver := node.Status.NodeInfo.KubeletVersion
		minor := extractMinorVersion(ver)
		if minor == "" {
			continue
		}
		versions[minor] = append(versions[minor], node.Name)
	}

	if len(versions) <= 1 {
		return nil
	}

	// Find min and max versions
	var minV, maxV string
	for v := range versions {
		if minV == "" || v < minV {
			minV = v
		}
		if maxV == "" || v > maxV {
			maxV = v
		}
	}

	return &VersionSkew{
		Versions:   versions,
		MinVersion: minV,
		MaxVersion: maxV,
	}
}

// extractMinorVersion extracts "v1.28" from "v1.28.3" or "1.28" from "1.28.3".
func extractMinorVersion(version string) string {
	version = strings.TrimPrefix(version, "v")
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	return parts[0] + "." + parts[1]
}

// FormatAge formats a duration into a human-readable age string (e.g., "5d", "3h").
func FormatAge(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Truncate trims a string to maxLen characters, appending "..." if truncated.
func Truncate(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

// HPAProblem describes a detected issue with an HPA.
type HPAProblem struct {
	Name      string
	Namespace string
	Problem   string // "maxed"
	Reason    string
}

// DetectHPAProblems finds HPAs that have hit their replica ceiling OR that
// cannot scale because the autoscaler can't fetch metrics. The latter is
// the silent-broken-HPA case: spec is valid, target exists, but
// status.conditions[?type=ScalingActive].status=False means the controller
// gave up — metrics-server unavailable, broken adapter, missing resource
// requests on target pods, etc. K8s autoscaler condition reasons are
// stable across versions (FailedGetResourceMetric / FailedGetScale /
// FailedGetExternalMetric / FailedGetObjectMetric).
func DetectHPAProblems(hpas []*autoscalingv2.HorizontalPodAutoscaler) []HPAProblem {
	var problems []HPAProblem
	for _, hpa := range hpas {
		// "maxed" — at replica ceiling and wanting more.
		if hpa.Spec.MaxReplicas > 0 && hpa.Status.CurrentReplicas >= hpa.Spec.MaxReplicas && hpa.Status.DesiredReplicas >= hpa.Spec.MaxReplicas {
			problems = append(problems, HPAProblem{
				Name:      hpa.Name,
				Namespace: hpa.Namespace,
				Problem:   "maxed",
				Reason:    fmt.Sprintf("%d/%d replicas (wants %d)", hpa.Status.CurrentReplicas, hpa.Spec.MaxReplicas, hpa.Status.DesiredReplicas),
			})
		}
		// "cannot scale" — the autoscaler controller reports it can't get
		// metrics or scale calls are failing. Emitted as a separate problem
		// so the maxed-check above isn't masked by an unrelated metrics
		// outage on the same HPA.
		for _, cond := range hpa.Status.Conditions {
			if cond.Type == autoscalingv2.ScalingActive && cond.Status == corev1.ConditionFalse {
				reason := cond.Reason
				if reason == "" {
					reason = "ScalingActive=False"
				}
				msg := cond.Message
				if msg == "" {
					msg = "HPA controller reports it cannot scale this workload"
				}
				problems = append(problems, HPAProblem{
					Name:      hpa.Name,
					Namespace: hpa.Namespace,
					Problem:   "cannot-scale",
					Reason:    fmt.Sprintf("%s: %s", reason, msg),
				})
				break
			}
		}
	}
	return problems
}

// CronJobProblem describes a detected issue with a CronJob.
type CronJobProblem struct {
	Name      string
	Namespace string
	Problem   string // "stale" or "never-scheduled"
	Reason    string
}

// estimateCronMinInterval returns a coarse lower bound on the time between runs
// of a standard 5-field cron schedule (minute hour dom month dow), plus the
// common @-macros. It is deliberately approximate — its only job is to keep
// DetectCronJobProblems from flagging a rare-cadence job (weekly / monthly /
// quarterly) as "stale" against a flat daily threshold. ok=false for schedules
// it can't parse; the caller then falls back to the flat threshold.
func estimateCronMinInterval(schedule string) (time.Duration, bool) {
	const day = 24 * time.Hour
	s := strings.TrimSpace(schedule)
	switch s {
	case "@yearly", "@annually":
		return 365 * day, true
	case "@monthly":
		return 28 * day, true
	case "@weekly":
		return 7 * day, true
	case "@daily", "@midnight":
		return day, true
	case "@hourly":
		return time.Hour, true
	}
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return 0, false
	}
	hour, dom, month, dow := fields[1], fields[2], fields[3], fields[4]
	switch {
	case month != "*":
		// Constrained to certain months → at most monthly, often far less.
		return 28 * day, true
	case dom != "*":
		// Specific day(s)-of-month → monthly cadence.
		return 28 * day, true
	case dow != "*":
		// Specific day(s)-of-week → weekly is the conservative lower bound.
		return 7 * day, true
	case hour != "*" && !strings.HasPrefix(hour, "*/"):
		// Specific hour(s) each day → daily.
		return day, true
	default:
		// Intra-day cadence (every minute / */n minutes or hours).
		return time.Hour, true
	}
}

// DetectCronJobProblems finds non-suspended CronJobs that haven't run recently.
func DetectCronJobProblems(cronjobs []*batchv1.CronJob) []CronJobProblem {
	var problems []CronJobProblem
	now := time.Now()
	for _, cj := range cronjobs {
		if cj.Spec.Suspend != nil && *cj.Spec.Suspend {
			continue
		}
		// Staleness is relative to the schedule's cadence, not a flat day: a
		// quarterly job that ran on schedule 29 days ago is healthy, not stale.
		// Floor at 24h so frequent jobs keep the original sensitivity.
		threshold := 24 * time.Hour
		if interval, ok := estimateCronMinInterval(cj.Spec.Schedule); ok {
			if grace := interval + interval/2; grace > threshold {
				threshold = grace
			}
		}
		if cj.Status.LastScheduleTime != nil {
			sinceLast := now.Sub(cj.Status.LastScheduleTime.Time)
			if sinceLast > threshold {
				problems = append(problems, CronJobProblem{
					Name:      cj.Name,
					Namespace: cj.Namespace,
					Problem:   "stale",
					Reason:    fmt.Sprintf("last run %dh ago", int(sinceLast.Hours())),
				})
			}
		} else if now.Sub(cj.CreationTimestamp.Time) > threshold {
			problems = append(problems, CronJobProblem{
				Name:      cj.Name,
				Namespace: cj.Namespace,
				Problem:   "never-scheduled",
				Reason:    "created but never ran",
			})
		}
	}
	return problems
}

// ParseCPUToMillis parses CPU quantity strings like "250m", "1", "500n".
func ParseCPUToMillis(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if before, ok := strings.CutSuffix(s, "n"); ok {
		var val int64
		fmt.Sscanf(before, "%d", &val)
		return val / 1000000
	}
	if before, ok := strings.CutSuffix(s, "m"); ok {
		var val int64
		fmt.Sscanf(before, "%d", &val)
		return val
	}
	var val int64
	fmt.Sscanf(s, "%d", &val)
	return val * 1000
}

// ParseMemoryToBytes parses memory quantity strings like "1024Ki", "256Mi", "1Gi".
func ParseMemoryToBytes(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if before, ok := strings.CutSuffix(s, "Ki"); ok {
		var val int64
		fmt.Sscanf(before, "%d", &val)
		return val * 1024
	}
	if before, ok := strings.CutSuffix(s, "Mi"); ok {
		var val int64
		fmt.Sscanf(before, "%d", &val)
		return val * 1024 * 1024
	}
	if before, ok := strings.CutSuffix(s, "Gi"); ok {
		var val int64
		fmt.Sscanf(before, "%d", &val)
		return val * 1024 * 1024 * 1024
	}
	var val int64
	fmt.Sscanf(s, "%d", &val)
	return val
}
