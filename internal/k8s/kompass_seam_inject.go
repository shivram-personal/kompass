// KOMPASS SEAM 3: in-memory kubeconfig injection — see docs/SPEC.md ADR-001.
//
// This file is the ONLY net-new engine logic for seam #3 (alongside the
// loopback bind and the rebrand). It lets kompass-core hand the engine an
// already-decrypted kubeconfig over loopback; the engine holds it in process
// memory ONLY, keyed by cluster id, and never writes it to any filesystem path
// (no disk, no tmpfs, no temp file). The engine never sees the encrypted store,
// the wrapped DEK, or any KMS reference — all of that stays in Python core.
//
// RE-SYNC POINT (rebase): kompassSwitchInjected below INTENTIONALLY MIRRORS the
// client-construction + global-swap tail of SwitchContext (client.go). It is
// duplicated here on purpose so the upstream hook stays a 5-line append. If a
// future upstream rebase changes how SwitchContext builds clients or swaps the
// package globals, this function must be re-checked and re-synced to match.
//
// CONCURRENCY: the engine is single-active-context by design — there is exactly
// one active client at a time, swapped under clientMu. This seam inherits that
// property verbatim (same lock, same globals, same order); it introduces no new
// race. Per-cluster credentials are isolated in kompassCreds, but selecting a
// cluster flips the one global active client. Callers must treat select + the
// dependent reads as a serialized unit against the active context.
package k8s

import (
	"fmt"
	"strings"
	"sync"

	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

// kompassSelectPrefix marks a SwitchContext name as an injected cluster.
// Format: "kompass:<clusterID>".
const kompassSelectPrefix = "kompass:"

// kompassCred is a decrypted kubeconfig held only in process memory.
type kompassCred struct {
	apiConfig   *clientcmdapi.Config // parsed kubeconfig; never serialized to disk
	contextName string               // current-context within apiConfig
}

// kompassCreds maps clusterID -> *kompassCred. Memory only.
var kompassCreds sync.Map

// kompassInject parses an already-decrypted kubeconfig and stores it in memory
// keyed by clusterID. Re-injecting the same clusterID replaces the prior
// credential (rotation). Returns the resolved current-context name (non-secret).
// No filesystem write occurs at any point.
func KompassInject(clusterID string, kubeconfig []byte) (string, error) {
	if clusterID == "" {
		return "", fmt.Errorf("kompass: cluster id is required")
	}
	apiConfig, err := clientcmd.Load(kubeconfig) // parses bytes in memory; no disk
	if err != nil {
		return "", fmt.Errorf("kompass: invalid kubeconfig")
	}
	ctxName := apiConfig.CurrentContext
	if ctxName == "" {
		for name := range apiConfig.Contexts {
			ctxName = name
			break
		}
	}
	if _, ok := apiConfig.Contexts[ctxName]; !ok || ctxName == "" {
		return "", fmt.Errorf("kompass: kubeconfig has no usable context")
	}
	kompassCreds.Store(clusterID, &kompassCred{apiConfig: apiConfig, contextName: ctxName})
	return ctxName, nil
}

// kompassEvict removes a cluster's credential from memory (cluster delete /
// lifecycle end). No-op if absent.
func KompassEvict(clusterID string) {
	kompassCreds.Delete(clusterID)
}

// kompassHasInjected reports whether a clusterID currently has injected creds.
func KompassHasInjected(clusterID string) bool {
	_, ok := kompassCreds.Load(clusterID)
	return ok
}

// kompassClientConfigFor builds a *rest.Config from a cluster's IN-MEMORY
// kubeconfig — no ExplicitPath, no file load — and has NO side effects (it does
// not touch package globals), so it is safe to unit-test. Returns ok=false when
// the cluster is not injected. This is the disk-free counterpart of the
// loadingRules/ClientConfig construction in SwitchContext.
func kompassClientConfigFor(clusterID string) (*rest.Config, *kompassCred, bool, error) {
	v, ok := kompassCreds.Load(clusterID)
	if !ok {
		return nil, nil, false, nil
	}
	cred := v.(*kompassCred)
	if _, ok := cred.apiConfig.Contexts[cred.contextName]; !ok {
		return nil, cred, true, fmt.Errorf("kompass: injected context not found")
	}
	clientConfig := clientcmd.NewNonInteractiveClientConfig(
		*cred.apiConfig, cred.contextName, &clientcmd.ConfigOverrides{}, nil,
	)
	config, err := clientConfig.ClientConfig()
	if err != nil {
		return nil, cred, true, fmt.Errorf("kompass: failed to build config for %q: %w", clusterID, err)
	}
	// Mirror SwitchContext's QPS/Burst to avoid client-side throttling.
	config.QPS = 50
	config.Burst = 100
	return config, cred, true, nil
}

// kompassSwitchInjected is the hook called from SwitchContext. If `name` refers
// to an injected cluster ("kompass:<id>"), it builds the active client entirely
// from the in-memory kubeconfig and swaps the engine globals, returning
// handled=true. Otherwise handled=false and SwitchContext proceeds normally.
//
// The body below mirrors SwitchContext's tail (see RE-SYNC POINT above).
func kompassSwitchInjected(name string) (handled bool, err error) {
	if !strings.HasPrefix(name, kompassSelectPrefix) {
		return false, nil
	}
	if IsInCluster() {
		return true, fmt.Errorf("cannot switch context when running in-cluster")
	}
	clusterID := strings.TrimPrefix(name, kompassSelectPrefix)
	config, cred, ok, err := kompassClientConfigFor(clusterID)
	if err != nil {
		return true, err
	}
	if !ok {
		return true, fmt.Errorf("kompass: cluster %q is not injected", clusterID)
	}
	ctx := cred.apiConfig.Contexts[cred.contextName]

	newK8sClient, err := kubernetes.NewForConfig(config)
	if err != nil {
		return true, fmt.Errorf("kompass: failed to create k8s client: %w", err)
	}
	newDiscoveryClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return true, fmt.Errorf("kompass: failed to create discovery client: %w", err)
	}
	newDynamicClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return true, fmt.Errorf("kompass: failed to create dynamic client: %w", err)
	}

	usesExec := false
	if ai, ok := cred.apiConfig.AuthInfos[ctx.AuthInfo]; ok && ai.Exec != nil {
		usesExec = true
	}
	execCmds, emptyAIs := collectExecPluginCommands(cred.apiConfig)
	if len(emptyAIs) > 0 {
		recordEmptyCommandWarning("kompass-inject-switch", emptyAIs)
	}

	// Atomic global swap — identical to SwitchContext, same lock and order.
	clientMu.Lock()
	k8sConfig = config
	k8sClient = newK8sClient
	discoveryClient = newDiscoveryClient
	dynamicClient = newDynamicClient
	contextName = name
	clusterName = ctx.Cluster
	contextNamespace = ctx.Namespace
	contextUsesExec = usesExec
	totalContextCount = len(cred.apiConfig.Contexts)
	execPluginCommands = execCmds
	clientMu.Unlock()

	return true, nil
}
