package cloud

import (
	"context"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// DiscoverAPIServerURL reads `kube-public/cluster-info` and returns the
// external API server URL the cluster advertises to clients. The hub
// stores this so the fleet GitOps view can correlate Argo CD's
// `destination.server` references against the hub's connected clusters.
//
// Returns "" (not an error) when:
//   - the ConfigMap doesn't exist (typical on managed K8s services —
//     EKS, GKE, AKS don't run the kubeadm bootstrap step that creates it)
//   - RBAC denies the read (cluster-info is system:unauthenticated-readable
//     by convention but some hardened deployments lock it down)
//   - the embedded kubeconfig has no clusters or no server URL
//
// All of the empty-string cases are fine: the hub falls back to name-based
// correlation. A non-empty URL just gives correlation a stronger signal.
// Caller should pass a short timeout via ctx — a single ConfigMap GET
// should resolve in well under a second.
func DiscoverAPIServerURL(ctx context.Context, client kubernetes.Interface) string {
	if client == nil {
		return ""
	}
	cm, err := client.CoreV1().ConfigMaps("kube-public").Get(ctx, "cluster-info", metav1.GetOptions{})
	if err != nil {
		// 404, RBAC, or transient error — all silent. The agent connect
		// path is best-effort for this field.
		return ""
	}
	kubeconfig, ok := cm.Data["kubeconfig"]
	if !ok || kubeconfig == "" {
		return ""
	}
	// clientcmd.Load parses the embedded kubeconfig YAML; we read the
	// `clusters[0].cluster.server` field, which kubeadm writes as the
	// canonical external API server URL.
	cfg, err := clientcmd.Load([]byte(kubeconfig))
	if err != nil {
		return ""
	}
	for _, cluster := range cfg.Clusters {
		if cluster == nil {
			continue
		}
		server := strings.TrimSpace(cluster.Server)
		// Validate shape — reject anything that doesn't look like an http(s)
		// URL. The hub validates again, but trimming garbage here keeps
		// the wire clean.
		if server == "" || (!strings.HasPrefix(server, "https://") && !strings.HasPrefix(server, "http://")) {
			continue
		}
		return server
	}
	return ""
}

// validateAPIServerURL is a defensive header-value check. We send what
// DiscoverAPIServerURL returns, but a corrupted ConfigMap shouldn't be
// able to inject newlines / null bytes / oversized garbage into the
// X-Radar-API-Server-URL header. Returns the input on success, "" on
// rejection. Caller logs at info level when it rejects so operators have
// a breadcrumb without spamming.
func validateAPIServerURL(s string) (string, error) {
	if s == "" {
		return "", nil
	}
	if len(s) > 512 {
		return "", fmt.Errorf("api server url too long (%d > 512)", len(s))
	}
	if strings.ContainsAny(s, "\n\r\x00") {
		return "", fmt.Errorf("api server url contains control characters")
	}
	if !strings.HasPrefix(s, "https://") && !strings.HasPrefix(s, "http://") {
		return "", fmt.Errorf("api server url missing scheme")
	}
	return s, nil
}
