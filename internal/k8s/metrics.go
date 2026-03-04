package k8s

import (
	"context"

	"github.com/skyhook-io/radar/pkg/k8score"
)

// GVR aliases for metrics_history.go which shares this package.
var (
	podMetricsGVR  = k8score.PodMetricsGVR
	nodeMetricsGVR = k8score.NodeMetricsGVR
)

// Re-export types from pkg/k8score for backward compatibility with existing callers.
type PodMetrics = k8score.PodMetrics
type NodeMetrics = k8score.NodeMetrics
type MetricsMeta = k8score.MetricsMeta
type ContainerMetrics = k8score.ContainerMetrics
type ResourceUsage = k8score.ResourceUsage

// GetPodMetrics fetches metrics for a specific pod from the metrics.k8s.io API.
func GetPodMetrics(ctx context.Context, namespace, name string) (*PodMetrics, error) {
	return k8score.GetPodMetrics(ctx, GetDynamicClient(), namespace, name)
}

// GetNodeMetrics fetches metrics for a specific node from the metrics.k8s.io API.
func GetNodeMetrics(ctx context.Context, name string) (*NodeMetrics, error) {
	return k8score.GetNodeMetrics(ctx, GetDynamicClient(), name)
}
