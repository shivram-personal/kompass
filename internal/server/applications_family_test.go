package server

import (
	"encoding/json"
	"testing"
)

// famRow builds an app row the family resolver sees. key "" derives a
// label-tier key; images become one workload each.
func famRow(name, ns string, tier int, key string, images ...string) appRow {
	r := appRow{Key: key, Name: name, Namespace: ns, Namespaces: []string{ns}, Tier: tier, Health: "healthy"}
	if r.Key == "" {
		r.Key = ns + "/app/" + name
	}
	for _, img := range images {
		r.Workloads = append(r.Workloads, appWorkload{Kind: "Deployment", Namespace: ns, Name: name, Image: img, Health: "healthy"})
	}
	return r
}

func famOf(t *testing.T, rows []appRow, name string) *appFamily {
	t.Helper()
	for i := range rows {
		if rows[i].Name == name {
			return rows[i].Family
		}
	}
	t.Fatalf("row %q missing", name)
	return nil
}

// The billing shape: an Argo app with a declared env-overlay path plus a raw
// row in an env namespace, same image repo → one family; the declared member
// is high-confidence, the corroborated one medium.
func TestFamilies_DeclaredPathPlusRawNamespaceEnv(t *testing.T) {
	rows := []appRow{
		famRow("billing-staging", "staging", 3, "/Application/billing-staging", "repo.dev/koala/billing:b_2026-06-05_01"),
		famRow("billing", "dev", 0, "", "repo.dev/koala/billing:b_2026-05-18_00"),
	}
	resolveAppFamilies(rows, map[string]string{"billing-staging": "billing/deploy/overlays/staging"})

	st := famOf(t, rows, "billing-staging")
	if st == nil || st.Key != "billing" || st.Env != "staging" || st.Confidence != "high" {
		t.Fatalf("billing-staging family = %+v, want key=billing env=staging high", st)
	}
	dev := famOf(t, rows, "billing")
	if dev == nil || dev.Key != "billing" || dev.Env != "dev" || dev.Confidence != "medium" {
		t.Fatalf("billing(dev) family = %+v, want key=billing env=dev medium", dev)
	}
}

// The koala-backend shape: env-PREFIXED hub-spoke tracking-id pairs (no
// in-cluster Application objects), same repo → medium family; autopush is
// recognized (groups) even though unranked.
func TestFamilies_EnvPrefixTrackingPair(t *testing.T) {
	rows := []appRow{
		famRow("autopush-koala-backend-us-east1", "autopush", 3, "/Application/autopush-koala-backend-us-east1", "repo.dev/koala/koala-backend:m1"),
		famRow("staging-koala-backend-us-east1", "staging", 3, "/Application/staging-koala-backend-us-east1", "repo.dev/koala/koala-backend:m2"),
	}
	resolveAppFamilies(rows, nil)

	a := famOf(t, rows, "autopush-koala-backend-us-east1")
	b := famOf(t, rows, "staging-koala-backend-us-east1")
	if a == nil || b == nil || a.Key != "koala-backend-us-east1" || a.Key != b.Key {
		t.Fatalf("prefix pair families = %+v / %+v, want shared key koala-backend-us-east1", a, b)
	}
	if a.Env != "autopush" || b.Env != "staging" || a.Confidence != "medium" {
		t.Fatalf("prefix pair env/conf = %s/%s %s, want autopush/staging medium", a.Env, b.Env, a.Confidence)
	}
}

// The project-infra shape: identical name across three env namespaces, same
// repo → one family with env from the namespace.
func TestFamilies_SameNameAcrossEnvNamespaces(t *testing.T) {
	rows := []appRow{
		famRow("project-infra", "dev", 7, "dev/app/project-infra", "repo.dev/koala/project-infra:x"),
		famRow("project-infra", "staging", 7, "staging/app/project-infra", "repo.dev/koala/project-infra:y"),
		famRow("project-infra", "autopush", 7, "autopush/app/project-infra", "repo.dev/koala/project-infra:z"),
	}
	resolveAppFamilies(rows, nil)
	envs := map[string]bool{}
	for i := range rows {
		f := rows[i].Family
		if f == nil || f.Key != "project-infra" {
			t.Fatalf("row %d family = %+v, want key=project-infra", i, f)
		}
		envs[f.Env] = true
	}
	if !envs["dev"] || !envs["staging"] || !envs["autopush"] {
		t.Fatalf("envs = %v, want dev+staging+autopush", envs)
	}
}

// Repo corroboration is mandatory for F2: a name-stem coincidence with no
// shared image repo must NOT group.
func TestFamilies_NoRepoOverlapRefuses(t *testing.T) {
	rows := []appRow{
		famRow("api", "dev", 0, "", "repo.dev/teamA/api:1"),
		famRow("api", "staging", 0, "", "repo.dev/teamB/other:1"),
	}
	resolveAppFamilies(rows, nil)
	if rows[0].Family != nil || rows[1].Family != nil {
		t.Fatalf("uncorroborated stem match grouped: %+v / %+v", rows[0].Family, rows[1].Family)
	}
}

// Same env twice is replicas/shards, not a family — distinct envs required.
func TestFamilies_SingleEnvRefuses(t *testing.T) {
	rows := []appRow{
		famRow("worker", "staging", 0, "staging/app/worker-a", "repo.dev/koala/worker:1"),
		famRow("worker", "staging", 0, "staging/app/worker-b", "repo.dev/koala/worker:1"),
	}
	resolveAppFamilies(rows, nil)
	if rows[0].Family != nil {
		t.Fatalf("single-env group formed: %+v", rows[0].Family)
	}
}

// Conflicting DECLARED path stems never merge through a shared name stem.
func TestFamilies_ConflictingDeclaredStemsRefuse(t *testing.T) {
	rows := []appRow{
		famRow("shop-staging", "staging", 3, "/Application/shop-staging", "repo.dev/a/shop:1"),
		famRow("shop-dev", "dev", 3, "/Application/shop-dev", "repo.dev/a/shop:2"),
	}
	resolveAppFamilies(rows, map[string]string{
		"shop-staging": "teamA/shop/overlays/staging",
		"shop-dev":     "teamB/legacy-shop/overlays/dev",
	})
	if rows[0].Family != nil || rows[1].Family != nil {
		t.Fatalf("conflicting declarations grouped: %+v / %+v", rows[0].Family, rows[1].Family)
	}
}

// A name affix outside the conservative set is not env evidence: "-test" apps
// don't family via name, only via an env namespace.
func TestFamilies_GenericTokensNotNameEvidence(t *testing.T) {
	rows := []appRow{
		famRow("load-test", "apps", 0, "", "repo.dev/koala/load:1"),
		famRow("load", "dev", 0, "", "repo.dev/koala/load:1"),
	}
	resolveAppFamilies(rows, nil)
	if rows[0].Family != nil {
		t.Fatalf("'-test' suffix treated as env: %+v", rows[0].Family)
	}
}

// Synonyms canonicalize so "production"/"stage" land on the ladder tokens.
func TestFamilies_EnvSynonymsCanonicalize(t *testing.T) {
	rows := []appRow{
		famRow("pay-production", "payments", 0, "", "repo.dev/koala/pay:1"),
		famRow("pay-stage", "payments", 0, "", "repo.dev/koala/pay:2"),
	}
	resolveAppFamilies(rows, nil)
	a, b := rows[0].Family, rows[1].Family
	if a == nil || b == nil || a.Env != "prod" || b.Env != "staging" {
		t.Fatalf("synonyms = %+v / %+v, want prod / staging", a, b)
	}
}

// THE CONTRACT: family is classification, never identity — a tagged row is
// byte-identical to the untagged row minus the family block.
func TestFamilies_ClassificationNotIdentity(t *testing.T) {
	mk := func() []appRow {
		return []appRow{
			famRow("billing-staging", "staging", 3, "/Application/billing-staging", "repo.dev/koala/billing:1"),
			famRow("billing", "dev", 0, "", "repo.dev/koala/billing:2"),
		}
	}
	tagged := mk()
	resolveAppFamilies(tagged, nil)
	for i := range tagged {
		tagged[i].Family = nil
	}
	want, _ := json.Marshal(mk())
	got, _ := json.Marshal(tagged)
	if string(want) != string(got) {
		t.Fatalf("family tagging mutated row identity:\nwant %s\ngot  %s", want, got)
	}
}
