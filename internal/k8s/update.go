package k8s

import (
	"context"
	"log"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/skyhook-io/radar/pkg/k8score"
)

// Re-export types from pkg/k8score for backward compatibility.
type WorkloadRevision = k8score.WorkloadRevision
type UpdateResourceOptions = k8score.UpdateResourceOptions
type DeleteResourceOptions = k8score.DeleteResourceOptions

func getWorkloadManager() *k8score.WorkloadManager {
	var disc *k8score.ResourceDiscovery
	if d := GetResourceDiscovery(); d != nil {
		disc = d.ResourceDiscovery
	} else {
		log.Printf("[k8s] Warning: resource discovery not initialized; workload operations will fail until cluster is ready")
	}
	return k8score.NewWorkloadManager(GetDynamicClient(), disc)
}

// UpdateResource updates a Kubernetes resource from YAML.
func UpdateResource(ctx context.Context, opts UpdateResourceOptions) (*unstructured.Unstructured, error) {
	return getWorkloadManager().UpdateResource(ctx, opts)
}

// DeleteResource deletes a Kubernetes resource.
func DeleteResource(ctx context.Context, opts DeleteResourceOptions) error {
	return getWorkloadManager().DeleteResource(ctx, opts)
}

// TriggerCronJob creates a Job from a CronJob.
func TriggerCronJob(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	return getWorkloadManager().TriggerCronJob(ctx, namespace, name)
}

// SetCronJobSuspend sets the suspend field on a CronJob.
func SetCronJobSuspend(ctx context.Context, namespace, name string, suspend bool) error {
	return getWorkloadManager().SetCronJobSuspend(ctx, namespace, name, suspend)
}

// RestartWorkload performs a rolling restart on a Deployment, StatefulSet, or DaemonSet.
func RestartWorkload(ctx context.Context, kind, namespace, name string) error {
	return getWorkloadManager().RestartWorkload(ctx, kind, namespace, name)
}

// ScaleWorkload scales a Deployment or StatefulSet to the specified replica count.
func ScaleWorkload(ctx context.Context, kind, namespace, name string, replicas int32) error {
	return getWorkloadManager().ScaleWorkload(ctx, kind, namespace, name, replicas)
}

// ListWorkloadRevisions returns the revision history for a Deployment, StatefulSet, or DaemonSet.
func ListWorkloadRevisions(ctx context.Context, kind, namespace, name string) ([]WorkloadRevision, error) {
	return getWorkloadManager().ListWorkloadRevisions(ctx, kind, namespace, name)
}

// RollbackWorkload rolls back a Deployment, StatefulSet, or DaemonSet to a specific revision.
func RollbackWorkload(ctx context.Context, kind, namespace, name string, revision int64) error {
	return getWorkloadManager().RollbackWorkload(ctx, kind, namespace, name, revision)
}
