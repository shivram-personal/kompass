package discovery

import (
	"context"
	"fmt"
	"strings"
)

// ProbeNewRelic detects a NewRelic Infrastructure install by scanning
// pods for the well-known image prefix. Reads any cluster license-key
// secrets we encounter to detect the account's region — EU license
// keys start with `eu01x*`. The region prefill lets the SPA pre-pick
// the EU MCP host instead of defaulting to US (and 401'ing).
//
// We do NOT prefill the User API Key (NRAK-*). Cluster license keys
// (NRII-* / NRAL-* / NRAK ingest variants) are ingestion-scoped; the
// MCP needs an account-read key minted in the customer's NR UI.
func ProbeNewRelic(ctx context.Context, env Env) (*Hit, error) {
	if env.Kube == nil {
		return nil, nil
	}
	pods, err := env.Kube.ListPods(ctx, "")
	if err != nil || pods == nil {
		return nil, nil
	}
	type found struct{ ns, pod, image string }
	var hits []found
	// Collect secret refs seen on NewRelic workloads so we can follow
	// them to the license key + read its region prefix without listing
	// every secret in the cluster.
	licenseRefs := map[string]nrSecretRef{}
	for _, p := range pods.Items {
		// Only harvest from pods that actually run a NewRelic-imaged
		// container. Walk every container of such a pod (the NR image and
		// the license env often live on different containers, e.g. the infra
		// agent), but never read license refs from unrelated workloads — an
		// EU-prefixed key on some other app must not flip the detected
		// agent's region.
		var nrImage string
		for _, c := range p.Spec.Containers {
			if strings.Contains(strings.ToLower(c.Image), "newrelic/") {
				nrImage = c.Image
				break
			}
		}
		if nrImage == "" {
			continue
		}
		hits = append(hits, found{ns: p.Namespace, pod: p.Name, image: nrImage})
		for _, c := range p.Spec.Containers {
			for _, e := range c.Env {
				if e.Name != "NRIA_LICENSE_KEY" && e.Name != "NEW_RELIC_LICENSE_KEY" {
					continue
				}
				if e.ValueFrom == nil || e.ValueFrom.SecretKeyRef == nil {
					continue
				}
				name := e.ValueFrom.SecretKeyRef.Name
				key := e.ValueFrom.SecretKeyRef.Key
				if name == "" || key == "" {
					continue
				}
				licenseRefs[p.Namespace+"/"+name+"#"+key] = nrSecretRef{ns: p.Namespace, name: name, key: key}
			}
		}
		if len(hits) >= 3 && len(licenseRefs) >= 1 {
			break
		}
	}
	if len(hits) == 0 {
		return nil, nil
	}

	prefill := map[string]string{}
	region := newRelicRegionFromSecrets(ctx, env, licenseRefs)
	if region == "eu" {
		prefill["mcp_host"] = "mcp.eu.newrelic.com"
	} else if region == "us" {
		prefill["mcp_host"] = "mcp.newrelic.com"
	}

	evidence := fmt.Sprintf("NewRelic agent image on %d workload(s); first: %s/%s (%s)",
		len(hits), hits[0].ns, hits[0].pod, hits[0].image)
	if region != "" {
		evidence += fmt.Sprintf(", region=%s (from license prefix)", region)
	}
	return &Hit{
		Types:    []string{"newrelic", "newrelic_mcp"},
		Variant:  "newrelic",
		Evidence: evidence,
		Prefill:  prefill,
	}, nil
}

// newRelicRegionFromSecrets follows known license-key secret refs and
// reads the prefix. NewRelic license keys carry an account-region
// prefix as their first 4-5 chars:
//   - `eu01x...` → EU
//   - everything else → US (NRII-, NRAK-, plain hex license keys)
// Returns "" when no secret could be read.
type nrSecretRef struct{ ns, name, key string }

func newRelicRegionFromSecrets(ctx context.Context, env Env, refs map[string]nrSecretRef) string {
	// Scan EVERY readable license ref, not just the first: refs iterates a
	// map (random order) and a single account can carry multiple
	// NRIA_LICENSE_KEY refs. Any EU signal wins; we only fall back to "us"
	// after seeing at least one key and finding no EU signal, so an EU
	// account never gets the US mcp_host prefilled by iteration luck.
	sawKey := false
	for _, r := range refs {
		sec, err := env.Kube.GetSecret(ctx, r.ns, r.name)
		if err != nil || sec == nil {
			continue
		}
		raw, ok := sec.Data[r.key]
		if !ok || len(raw) < 5 {
			continue
		}
		// EU New Relic license keys are the only ones prefixed "eu" (e.g.
		// eu01xx…); US/other keys never start with "eu".
		if strings.HasPrefix(strings.ToLower(string(raw)), "eu") {
			return "eu"
		}
		sawKey = true
	}
	if sawKey {
		return "us"
	}
	return ""
}
