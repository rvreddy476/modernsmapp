package service

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Module 3 LB-7 — suggestion block safety must be DEPLOYABLE.
//
// The filtering is fail-closed: with no GRAPH_SERVICE_URL, every suggestion
// surface returns an empty list rather than an unfiltered one. That is the
// right direction (before SR-7 the service recommended accounts the viewer
// had blocked), but `deploy/services/suggestion-service/` did not exist — so
// there was nowhere to configure the dependency and production would have had
// a permanently empty "People you may know".
//
// Correct behaviour with undeployable configuration is still a launch blocker,
// so this test reads the manifests.
//
// It is deliberately dependency-free (no YAML library): these values files are
// flat `key: value` maps, and adding a parser to a vendored module for a test
// is churn this pass excludes.

const deployRoot = "../../../../../deploy/services/suggestion-service"

// AWS only. Azure is not a target for this platform, so there are no
// values-azure-*.yaml files for this service and none are asserted here.
var environments = []struct {
	name string
	file string
}{
	{"prod", "values-prod.yaml"},
	{"staging", "values-staging.yaml"},
}

// section extracts a top-level block's `key: value` pairs.
func section(t *testing.T, path, want string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close()

	out := map[string]string{}
	inSection := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			inSection = trimmed == want+":"
			continue
		}
		if !inSection {
			continue
		}
		key, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			continue
		}
		if i := strings.Index(value, " #"); i >= 0 {
			value = value[:i]
		}
		out[strings.TrimSpace(strings.TrimPrefix(key, "- "))] =
			strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return out
}

func fileText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// codeText strips comment lines.
//
// The manifests document WHY a key is present or deliberately absent, and
// those comments name the keys. A check that cannot tell YAML from prose fails
// on its own documentation — which is how such a check ends up deleted.
func codeText(t *testing.T, path string) string {
	t.Helper()
	var b strings.Builder
	for _, line := range strings.Split(fileText(t, path), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		b.WriteString(line)
		b.WriteString("\n")
	}
	return b.String()
}

// THE ONE THAT MATTERS: without this, every suggestion surface is empty.
func TestEveryEnvironmentConfiguresGraphServiceURL(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			path := filepath.Join(deployRoot, env.file)
			envs := section(t, path, "env")

			url := envs["GRAPH_SERVICE_URL"]
			if url == "" {
				t.Fatalf("%s does not set GRAPH_SERVICE_URL. Block filtering is "+
					"fail-closed, so EVERY suggestion surface would return an empty "+
					"list in this environment.", env.file)
			}
			if !strings.Contains(url, "graph-service") {
				t.Errorf("GRAPH_SERVICE_URL=%q does not point at graph-service", url)
			}
			if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
				t.Errorf("GRAPH_SERVICE_URL=%q has no scheme", url)
			}
		})
	}
}

// The call to graph-service's blocked-and-muted endpoint is internal-key
// gated. Without the key the lookup 401s and — fail-closed — the surface is
// empty, which looks identical to a missing URL.
func TestInternalServiceKeyIsProvisioned(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			text := codeText(t, filepath.Join(deployRoot, env.file))
			if !strings.Contains(text, "INTERNAL_SERVICE_KEY") {
				t.Fatalf("%s does not provision INTERNAL_SERVICE_KEY: the "+
					"blocked-and-muted lookup would 401 and every surface would be "+
					"empty", env.file)
			}
		})
	}
}

// The datastores the service actually reads must be present, or it cannot boot.
func TestRequiredDatastoresAreProvisioned(t *testing.T) {
	required := []string{
		"POSTGRES_DSN",          // candidates, cooldowns, impressions
		"IDENTITY_POSTGRES_DSN", // profile enrichment
		"REDIS_ADDR",            // suggestion cache + consumer dedupe
		"KAFKA_BROKERS",         // graph safety events
	}
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			text := codeText(t, filepath.Join(deployRoot, env.file))
			for _, key := range required {
				if !strings.Contains(text, key) {
					t.Errorf("%s does not provision %s", env.file, key)
				}
			}
		})
	}
}

// No static AWS credential may appear in a manifest. Workload credentials come
// from IRSA (AWS) or ESO (Azure).
func TestNoStaticAWSCredentialsInManifests(t *testing.T) {
	banned := []string{"AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY", "AWS_SESSION_TOKEN"}
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			text := codeText(t, filepath.Join(deployRoot, env.file))
			for _, key := range banned {
				if strings.Contains(text, key) {
					t.Errorf("%s references %s. Workload credentials must come from "+
						"IRSA/ESO, never a manifest.", env.file, key)
				}
			}
		})
	}
}

// Every environment must carry an IRSA role. Workload credentials come from
// the pod's identity, never from a manifest — which is what
// TestNoStaticAWSCredentialsInManifests above enforces from the other side.
func TestEveryEnvironmentUsesIRSA(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			text := codeText(t, filepath.Join(deployRoot, env.file))
			if !strings.Contains(text, "irsaRoleArn") {
				t.Errorf("%s has no serviceAccount.irsaRoleArn, so the pod has no "+
					"identity to reach AWS with", env.file)
			}
		})
	}
}

// AWS is the only deployment target. An Azure overlay for this service would
// be an unmaintained manifest that nothing deploys — and, because block
// filtering is fail-closed, one whose drift would silently empty every
// suggestion surface in whatever environment did use it.
func TestNoAzureOverlaysExistForThisService(t *testing.T) {
	matches, err := filepath.Glob(filepath.Join(deployRoot, "values-azure-*.yaml"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(matches) > 0 {
		t.Errorf("found Azure overlays %v. This platform deploys to AWS only.", matches)
	}
}

// Resource limits and a service port must be set, or the pod is unschedulable
// or unreachable.
func TestResourceLimitsAndPortAreSet(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			text := codeText(t, filepath.Join(deployRoot, env.file))
			for _, needed := range []string{"limits:", "requests:", "port: 8100"} {
				if !strings.Contains(text, needed) {
					t.Errorf("%s is missing %q", env.file, needed)
				}
			}
		})
	}
}

// Internal-only: api-gateway is the external surface. An ingress here would
// expose suggestions directly, bypassing the edge's identity handling.
func TestSuggestionServiceIsNotExposedDirectly(t *testing.T) {
	for _, env := range environments {
		t.Run(env.name, func(t *testing.T) {
			ing := section(t, filepath.Join(deployRoot, env.file), "ingress")
			if ing["enabled"] == "true" {
				t.Errorf("%s exposes an ingress; suggestions must be reached through "+
					"api-gateway so identity headers are derived from a verified token",
					env.file)
			}
		})
	}
}
