package push

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// CALL-LB-4B: the CALLS_ENABLED ↔ CALL_PUSH_REQUIRED/FCM dependency is a
// TWO-SERVICE deployment contract, and comments cannot enforce it. This
// guard runs on every notification-service gate and validates the ACTUAL
// prod/staging values files STRUCTURALLY — parsed as YAML and checked at the
// exact paths the Helm chart consumes (`.Values.env` → container env;
// `.Values.externalSecret.data` → the generated secret). Review 6 proved the
// previous raw-text guard could be satisfied by correctly spelled keys at
// the wrong YAML scope or by a comment mentioning the secret name; a YAML
// parser makes those bypasses structurally impossible: comments are not
// data, and top-level or wrongly indented keys do not populate `env:` /
// `externalSecret:`.

// secretItem mirrors one externalSecret.data entry as the chart consumes it.
type secretItem struct {
	SecretKey string `yaml:"secretKey"`
	RemoteRef string `yaml:"remoteRef"`
}

// serviceValues models exactly the paths the contract depends on. Everything
// else in the file is ignored — which is the point: a key outside these
// paths does not exist as far as the deployed container is concerned.
type serviceValues struct {
	Env            map[string]interface{} `yaml:"env"`
	ExternalSecret struct {
		Enabled bool         `yaml:"enabled"`
		Data    []secretItem `yaml:"data"`
	} `yaml:"externalSecret"`
}

func parseValues(t *testing.T, content []byte, origin string) serviceValues {
	t.Helper()
	var v serviceValues
	if err := yaml.Unmarshal(content, &v); err != nil {
		t.Fatalf("%s is not valid YAML — the deployment contract cannot be verified: %v", origin, err)
	}
	return v
}

// envString reads env.<key> — the ONLY scope the chart injects into the
// container environment.
func envString(v serviceValues, key string) (string, bool) {
	raw, ok := v.Env[key]
	if !ok {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprintf("%v", raw)), true
}

// hasSecretEntry reports whether externalSecret is enabled AND carries a
// REAL data item with the wanted secretKey and a non-empty remoteRef — the
// exact shape charts/atpost-service/templates/externalsecret.yaml renders.
func hasSecretEntry(v serviceValues, secretKey string) bool {
	if !v.ExternalSecret.Enabled {
		return false
	}
	for _, item := range v.ExternalSecret.Data {
		if item.SecretKey == secretKey && strings.TrimSpace(item.RemoteRef) != "" {
			return true
		}
	}
	return false
}

// validateCallDeploymentContract is the shared core: given the two parsed
// values files for one environment, it returns the list of missing pieces of
// the required posture — empty means calling may be enabled there. The real-
// manifest guard and the wrong-scope/comment/empty-ref scenario tests all
// run THIS function, so they are load-bearing for the same logic.
func validateCallDeploymentContract(call, notif serviceValues) (callsEnabled string, missing []string) {
	callsEnabled, ok := envString(call, "CALLS_ENABLED")
	if !ok {
		return "", []string{`chat-call-service env.CALLS_ENABLED must be declared explicitly`}
	}
	if callsEnabled == "false" {
		return callsEnabled, nil // calling disabled: posture not required
	}
	if v, ok := envString(notif, "CALL_PUSH_REQUIRED"); !ok || v != "true" {
		missing = append(missing, `notification-service env.CALL_PUSH_REQUIRED: "true"`)
	}
	if v, ok := envString(notif, "FCM_PROJECT_ID"); !ok || v == "" {
		missing = append(missing, "notification-service env.FCM_PROJECT_ID (non-empty)")
	}
	if !hasSecretEntry(notif, "FCM_SERVICE_ACCOUNT_KEY") {
		missing = append(missing,
			"notification-service externalSecret.enabled=true with a data item "+
				"{secretKey: FCM_SERVICE_ACCOUNT_KEY, remoteRef: <non-empty>}")
	}
	return callsEnabled, missing
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for range [10]int{} {
		if _, err := os.Stat(filepath.Join(dir, "deploy", "services", "chat-call-service")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("repository root with deploy/services/chat-call-service not found — the deployment contract CANNOT be verified, refusing to pass")
	return ""
}

func loadManifest(t *testing.T, root, service, file string) serviceValues {
	t.Helper()
	path := filepath.Join(root, "deploy", "services", service, file)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("deployment contract cannot be verified: %v", err)
	}
	return parseValues(t, b, path)
}

// The real-manifest guard: fails the standard gate the moment anyone sets
// env.CALLS_ENABLED to anything but "false" in prod/staging without the full
// notification-service push posture at the chart-consumed paths.
func TestDeployedManifestsCoupleCallsToRequiredPush(t *testing.T) {
	root := repoRoot(t)
	for _, valuesFile := range []string{"values-prod.yaml", "values-staging.yaml"} {
		call := loadManifest(t, root, "chat-call-service", valuesFile)
		notif := loadManifest(t, root, "notification-service", valuesFile)
		callsEnabled, missing := validateCallDeploymentContract(call, notif)
		if len(missing) > 0 {
			t.Fatalf("%s enables calling (env.CALLS_ENABLED=%q) WITHOUT the required push posture — "+
				"a background recipient would never ring and the event would still commit (CALL-LB-4B).\nMissing:\n  %s",
				valuesFile, callsEnabled, strings.Join(missing, "\n  "))
		}
	}
}

// The knob must be DECLARED at the chart-consumed path (env.), explicitly.
func TestDeployedManifestsDeclareCallPushRequired(t *testing.T) {
	root := repoRoot(t)
	for _, valuesFile := range []string{"values-prod.yaml", "values-staging.yaml"} {
		notif := loadManifest(t, root, "notification-service", valuesFile)
		v, ok := envString(notif, "CALL_PUSH_REQUIRED")
		if !ok {
			t.Fatalf("%s: notification-service must declare env.CALL_PUSH_REQUIRED explicitly", valuesFile)
		}
		if v != "true" && v != "false" {
			t.Fatalf("%s: env.CALL_PUSH_REQUIRED must be \"true\" or \"false\", got %q", valuesFile, v)
		}
	}
}

// ── load-bearing bypass controls (review 6's demonstrated defeat) ─────────
//
// These scenarios run the SAME validateCallDeploymentContract the manifest
// guard uses, so a validator that regresses to raw-text matching fails here.

const callsEnabledYAML = `
env:
  ENV: prod
  CALLS_ENABLED: "true"
`

func TestDeploymentContractRejectsWrongScopeKeys(t *testing.T) {
	// Review 6's exact bypass: correctly spelled keys at TOP level, not
	// under env — the chart injects nothing, so the guard must refuse.
	notif := parseValues(t, []byte(`
CALL_PUSH_REQUIRED: "true"
FCM_PROJECT_ID: project
env:
  ENV: prod
  LOG_LEVEL: info
externalSecret:
  enabled: true
  data:
    - { secretKey: INTERNAL_SERVICE_KEY, remoteRef: internal_service_key }
`), "wrong-scope fixture")
	call := parseValues(t, []byte(callsEnabledYAML), "calls-enabled fixture")

	_, missing := validateCallDeploymentContract(call, notif)
	joined := strings.Join(missing, "\n")
	if !strings.Contains(joined, "env.CALL_PUSH_REQUIRED") ||
		!strings.Contains(joined, "env.FCM_PROJECT_ID") {
		t.Fatalf("top-level keys satisfied the guard — the chart would inject neither (CALL-LB-4B bypass): %v", missing)
	}
}

func TestDeploymentContractRejectsCommentOnlySecret(t *testing.T) {
	// The secret name appearing ONLY in a comment must not count: comments
	// are not data to a YAML parser, and the chart renders no secret.
	notif := parseValues(t, []byte(`
env:
  ENV: prod
  CALL_PUSH_REQUIRED: "true"
  FCM_PROJECT_ID: project
  # the FCM_SERVICE_ACCOUNT_KEY secret must be configured before launch
externalSecret:
  enabled: true
  data:
    - { secretKey: INTERNAL_SERVICE_KEY, remoteRef: internal_service_key }
`), "comment-only fixture")
	call := parseValues(t, []byte(callsEnabledYAML), "calls-enabled fixture")

	_, missing := validateCallDeploymentContract(call, notif)
	if !strings.Contains(strings.Join(missing, "\n"), "FCM_SERVICE_ACCOUNT_KEY") {
		t.Fatalf("a comment satisfied the secret requirement (CALL-LB-4B bypass): %v", missing)
	}
}

func TestDeploymentContractRejectsEmptyRemoteRefAndDisabledSecrets(t *testing.T) {
	for name, fixture := range map[string]string{
		"empty remoteRef": `
env:
  CALL_PUSH_REQUIRED: "true"
  FCM_PROJECT_ID: project
externalSecret:
  enabled: true
  data:
    - { secretKey: FCM_SERVICE_ACCOUNT_KEY, remoteRef: "" }
`,
		"externalSecret disabled": `
env:
  CALL_PUSH_REQUIRED: "true"
  FCM_PROJECT_ID: project
externalSecret:
  enabled: false
  data:
    - { secretKey: FCM_SERVICE_ACCOUNT_KEY, remoteRef: fcm_service_account_key }
`,
	} {
		notif := parseValues(t, []byte(fixture), name)
		call := parseValues(t, []byte(callsEnabledYAML), "calls-enabled fixture")
		_, missing := validateCallDeploymentContract(call, notif)
		if !strings.Contains(strings.Join(missing, "\n"), "FCM_SERVICE_ACCOUNT_KEY") {
			t.Fatalf("%s satisfied the secret requirement (CALL-LB-4B): %v", name, missing)
		}
	}
}

func TestDeploymentContractAcceptsTheCompletePosture(t *testing.T) {
	notif := parseValues(t, []byte(`
env:
  ENV: prod
  CALL_PUSH_REQUIRED: "true"
  FCM_PROJECT_ID: atpost-prod
externalSecret:
  enabled: true
  data:
    - { secretKey: INTERNAL_SERVICE_KEY, remoteRef: internal_service_key }
    - { secretKey: FCM_SERVICE_ACCOUNT_KEY, remoteRef: fcm_service_account_key }
`), "complete fixture")
	call := parseValues(t, []byte(callsEnabledYAML), "calls-enabled fixture")

	if _, missing := validateCallDeploymentContract(call, notif); len(missing) != 0 {
		t.Fatalf("complete posture refused: %v", missing)
	}
}

func TestDeploymentContractIgnoresCallsDisabled(t *testing.T) {
	call := parseValues(t, []byte("env:\n  CALLS_ENABLED: \"false\"\n"), "calls-disabled fixture")
	notif := parseValues(t, []byte("env:\n  ENV: prod\n"), "bare fixture")
	if enabled, missing := validateCallDeploymentContract(call, notif); enabled != "false" || len(missing) != 0 {
		t.Fatalf("disabled calling demanded posture: %v %v", enabled, missing)
	}
}
