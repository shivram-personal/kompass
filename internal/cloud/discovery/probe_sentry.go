package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// ProbeSentry detects Sentry instrumentation by scanning workloads for
// SENTRY_* env vars. The two we look at:
//
//   - SENTRY_DSN     — the secret that lets a workload report events.
//                      If present we read the value (DSN encodes
//                      project, but NOT a user-installable token, so we
//                      can't prefill auth_token from it).
//   - SENTRY_ENVIRONMENT — the env tag (prod/staging/etc). Useful
//                          context but the cloud upstream catalog doesn't yet have
//                          an `environment` field, so it's not prefilled
//                          today — we surface it in evidence only.
//
// We do NOT prefill `org_slug` or `auth_token` because they live in the
// customer's Sentry account, not in the cluster. The badge still helps:
// it tells the user "yes you use Sentry, click here to wire it up."
func ProbeSentry(ctx context.Context, env Env) (*Hit, error) {
	if env.Kube == nil {
		return nil, nil
	}
	pods, err := env.Kube.ListPods(ctx, "")
	if err != nil {
		return nil, nil
	}
	if pods == nil || len(pods.Items) == 0 {
		return nil, nil
	}

	type instrumented struct {
		ns      string
		name    string
		envTag  string
		hasDSN  bool
	}
	var found []instrumented
	for _, pod := range pods.Items {
		for _, c := range pod.Spec.Containers {
			rec := instrumented{ns: pod.Namespace, name: pod.Name}
			any := false
			for _, e := range c.Env {
				if !strings.HasPrefix(e.Name, "SENTRY_") {
					continue
				}
				any = true
				switch e.Name {
				case "SENTRY_DSN":
					if e.Value != "" {
						rec.hasDSN = true
					}
				case "SENTRY_ENVIRONMENT":
					rec.envTag = e.Value
				}
			}
			if any {
				found = append(found, rec)
				break // one record per pod is enough
			}
		}
	}
	if len(found) == 0 {
		return nil, nil
	}

	// Stable order so the evidence string doesn't churn between runs.
	sort.Slice(found, func(i, j int) bool {
		if found[i].ns != found[j].ns {
			return found[i].ns < found[j].ns
		}
		return found[i].name < found[j].name
	})

	first := found[0]
	evidence := fmt.Sprintf("SENTRY_* env vars on %d workload(s); first: %s/%s",
		len(found), first.ns, first.name)
	if first.envTag != "" {
		evidence += fmt.Sprintf(" (env=%q)", first.envTag)
	}

	return &Hit{
		Types:    []string{"sentry"},
		Variant:  "sentry",
		Evidence: evidence,
		Prefill:  map[string]string{},
	}, nil
}
