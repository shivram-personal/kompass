package helm

import (
	"context"

	"github.com/skyhook-io/radar/internal/k8s"
	"github.com/skyhook-io/radar/pkg/k8score"
)

// ResolveNoAuthListNamespaces returns a namespace fanout set for Helm list-style
// reads in no-auth mode. nil means keep the single cluster-wide Helm list.
func ResolveNoAuthListNamespaces(ctx context.Context) []string {
	accessible, authoritative := k8s.GetAccessibleNamespaces(ctx)
	if !authoritative && len(accessible) > 0 {
		return accessible
	}
	if authoritative && len(accessible) > 0 {
		allowed, apiErr := k8score.CanI(ctx, k8s.GetClient(), "", "", "secrets", "list")
		if !apiErr && !allowed {
			return accessible
		}
	}
	return nil
}
