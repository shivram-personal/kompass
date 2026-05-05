package helm

import "testing"

func TestFindBestUpgradeVersion(t *testing.T) {
	tests := []struct {
		name        string
		candidates  []repoVersionInfo
		sourceHosts []string
		wantVersion string
		wantRepo    string
	}{
		{
			name:        "no candidates returns empty",
			candidates:  nil,
			wantVersion: "",
			wantRepo:    "",
		},
		{
			name: "single repo with current version",
			candidates: []repoVersionInfo{
				{repoName: "metallb", latestVersion: "0.15.3", hasCurrentVersion: true},
			},
			wantVersion: "0.15.3",
			wantRepo:    "metallb",
		},
		{
			name: "multiple repos only one has current version - picks source repo",
			candidates: []repoVersionInfo{
				{repoName: "bitnami", latestVersion: "6.4.22", hasCurrentVersion: false},
				{repoName: "metallb", latestVersion: "0.15.3", hasCurrentVersion: true},
			},
			wantVersion: "0.15.3",
			wantRepo:    "metallb",
		},
		{
			name: "multiple repos both have current version - picks highest among them",
			candidates: []repoVersionInfo{
				{repoName: "repo-a", latestVersion: "2.0.0", hasCurrentVersion: true},
				{repoName: "repo-b", latestVersion: "3.0.0", hasCurrentVersion: true},
			},
			wantVersion: "3.0.0",
			wantRepo:    "repo-b",
		},
		{
			name: "source repo has lower latest than non-source - still picks source",
			candidates: []repoVersionInfo{
				{repoName: "community", latestVersion: "10.0.0", hasCurrentVersion: false},
				{repoName: "official", latestVersion: "1.2.0", hasCurrentVersion: true},
			},
			wantVersion: "1.2.0",
			wantRepo:    "official",
		},
		{
			name: "ambiguous chart-name collision without affinity - bail out",
			candidates: []repoVersionInfo{
				{repoName: "bitnami", latestVersion: "6.4.22", hasCurrentVersion: false, repoURL: "https://charts.bitnami.com/bitnami"},
				{repoName: "argo", latestVersion: "8.5.0", hasCurrentVersion: false, repoURL: "https://argoproj.github.io/argo-helm"},
			},
			wantVersion: "",
			wantRepo:    "",
		},
		{
			name: "single candidate without current version - accept (stale index case)",
			candidates: []repoVersionInfo{
				{repoName: "argo", latestVersion: "8.5.0", hasCurrentVersion: false, repoURL: "https://argoproj.github.io/argo-helm"},
			},
			wantVersion: "8.5.0",
			wantRepo:    "argo",
		},
		{
			name: "source-affinity host match picks correct repo",
			candidates: []repoVersionInfo{
				{repoName: "bitnami", latestVersion: "6.4.22", hasCurrentVersion: false, repoURL: "https://charts.bitnami.com/bitnami"},
				{repoName: "argo", latestVersion: "8.5.0", hasCurrentVersion: false, repoURL: "https://argoproj.github.io/argo-helm"},
			},
			sourceHosts: []string{"argoproj.github.io"},
			wantVersion: "8.5.0",
			wantRepo:    "argo",
		},
		{
			name: "source-affinity registered-domain match (charts.bitnami.com vs bitnami.com)",
			candidates: []repoVersionInfo{
				{repoName: "bitnami", latestVersion: "12.0.0", hasCurrentVersion: false, repoURL: "https://charts.bitnami.com/bitnami"},
				{repoName: "argo", latestVersion: "8.5.0", hasCurrentVersion: false, repoURL: "https://argoproj.github.io/argo-helm"},
			},
			sourceHosts: []string{"bitnami.com"},
			wantVersion: "12.0.0",
			wantRepo:    "bitnami",
		},
		{
			name: "source-affinity hosts present but none match - bail out",
			candidates: []repoVersionInfo{
				{repoName: "bitnami", latestVersion: "6.4.22", hasCurrentVersion: false, repoURL: "https://charts.bitnami.com/bitnami"},
				{repoName: "argo", latestVersion: "8.5.0", hasCurrentVersion: false, repoURL: "https://argoproj.github.io/argo-helm"},
			},
			sourceHosts: []string{"github.com"}, // chart-declared, but not the repo's host
			wantVersion: "",
			wantRepo:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotVersion, gotRepo := findBestUpgradeVersion(tt.candidates, tt.sourceHosts)
			if gotVersion != tt.wantVersion {
				t.Errorf("findBestUpgradeVersion() version = %q, want %q", gotVersion, tt.wantVersion)
			}
			if gotRepo != tt.wantRepo {
				t.Errorf("findBestUpgradeVersion() repo = %q, want %q", gotRepo, tt.wantRepo)
			}
		})
	}
}

func TestChartSourceHosts(t *testing.T) {
	tests := []struct {
		name    string
		home    string
		sources []string
		want    []string
	}{
		{
			name: "empty inputs",
			want: nil,
		},
		{
			name: "bitnami home only",
			home: "https://bitnami.com",
			want: []string{"bitnami.com"},
		},
		{
			name:    "subdomain expands to registered domain",
			home:    "https://charts.bitnami.com",
			want:    []string{"charts.bitnami.com", "bitnami.com"},
		},
		{
			name:    "deduplicates across home and sources",
			home:    "https://github.com/argoproj/argo-helm",
			sources: []string{"https://github.com/argoproj/argo-cd"},
			want:    []string{"github.com"},
		},
		{
			name:    "skips invalid urls",
			sources: []string{"not a url", "ftp://", ""},
			want:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := chartSourceHosts(tt.home, tt.sources)
			if !equalStringSlices(got, tt.want) {
				t.Errorf("chartSourceHosts() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRepoURLMatchesAny(t *testing.T) {
	tests := []struct {
		name    string
		repoURL string
		hosts   []string
		want    bool
	}{
		{name: "empty repo url", repoURL: "", hosts: []string{"bitnami.com"}, want: false},
		{name: "empty hosts", repoURL: "https://charts.bitnami.com", hosts: nil, want: false},
		{name: "exact host match", repoURL: "https://argoproj.github.io/argo-helm", hosts: []string{"argoproj.github.io"}, want: true},
		{name: "registered-domain match", repoURL: "https://charts.bitnami.com/bitnami", hosts: []string{"bitnami.com"}, want: true},
		{name: "no match", repoURL: "https://charts.bitnami.com", hosts: []string{"argoproj.github.io"}, want: false},
		{name: "invalid url", repoURL: "://broken", hosts: []string{"bitnami.com"}, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := repoURLMatchesAny(tt.repoURL, tt.hosts); got != tt.want {
				t.Errorf("repoURLMatchesAny(%q, %v) = %v, want %v", tt.repoURL, tt.hosts, got, tt.want)
			}
		})
	}
}

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1, v2 string
		want   int
	}{
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0", "2.0.0", -1},
		{"0.15.3", "6.4.22", -1},
		{"6.4.22", "0.15.3", 1},
		{"v1.0.0", "1.0.0", 0},
	}

	for _, tt := range tests {
		t.Run(tt.v1+"_vs_"+tt.v2, func(t *testing.T) {
			got := compareVersions(tt.v1, tt.v2)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
