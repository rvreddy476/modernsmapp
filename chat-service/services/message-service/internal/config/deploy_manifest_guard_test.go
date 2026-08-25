package config

// P0-3 / P0-4 correction-pass guard: the AWS values files must carry the
// chat-policy authority URL and the entitlement secret. The independent
// review found Compose correct while the canonical AWS manifests silently
// fell back to a wrong (Compose-only) default, which disabled policy refresh
// and the room path in every deployed environment. This test fails the Go
// gate if those keys disappear again.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "services")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("deploy/services not found walking up from the package directory")
	return ""
}

func mustContain(t *testing.T, path, needle, why string) {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if !strings.Contains(string(raw), needle) {
		t.Errorf("%s must contain %q — %s", path, needle, why)
	}
}

func TestAWSManifestsCarryIdentityAuthorityAndEntitlementSecret(t *testing.T) {
	root := repoRoot(t)
	msg := filepath.Join(root, "deploy", "services", "chat-message-service")
	gw := filepath.Join(root, "deploy", "services", "chat-ws-gateway")

	for _, env := range []string{"values-prod.yaml", "values-staging.yaml"} {
		mustContain(t, filepath.Join(msg, env),
			"IDENTITY_USER_SERVICE_URL: http://identity-user-service.atpost.svc.cluster.local:8082",
			"without it the policy fetch falls back to a Compose-only default and every "+
				"unknown policy fails closed in AWS (P0-3)")
		mustContain(t, filepath.Join(msg, env),
			"CHAT_ENTITLEMENT_SECRET",
			"without the shared secret the issuer mints nothing and the room path is dead in AWS (P0-4)")
		mustContain(t, filepath.Join(gw, env),
			"CHAT_ENTITLEMENT_SECRET",
			"the gateway must be able to verify what the issuer signs (P0-4)")
	}
}
