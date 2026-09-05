package kmsclient

// B3 / D — the deployment manifests, asserted as a contract.
//
// These are text assertions over Helm values, and text assertions are usually
// weak. They are here because the things they check are exactly the things
// nobody notices in review and that no Go test can otherwise see:
//
//   - a static AWS credential quietly added "to unblock staging", which turns
//     a rotating IRSA identity into a permanent one sitting in a manifest;
//   - a wildcard KMS resource or action, which turns least privilege into
//     "this pod can decrypt anything in the account";
//   - prod and staging sharing a CMK or a role, which makes the encryption
//     context's environment binding meaningless;
//   - a required variable silently absent, which fails at boot in the one
//     environment nobody watches;
//   - documentation drifting from what the server enforces, which is how
//     COMMERCE_PAYMENT_METHODS came to advertise two methods the server has
//     refused since B6.
//
// They live in this package rather than a scripts directory so `go test ./...`
// runs them, which is the only way a guard like this stays alive.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	prodValues    = "../../../../../deploy/services/commerce-service/values-prod.yaml"
	stagingValues = "../../../../../deploy/services/commerce-service/values-staging.yaml"
)

func readValues(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(b)
}

func managedManifests(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"prod":    readValues(t, prodValues),
		"staging": readValues(t, stagingValues),
	}
}

// ─── No static AWS credentials, anywhere ─────────────────────────────

// The single most damaging thing that could be added to these files.
//
// IRSA credentials are short-lived and rotate on their own; a static key in a
// manifest is permanent, is in git, and is in every developer's checkout.
func TestNoStaticAWSCredentialsInManagedManifests(t *testing.T) {
	forbidden := []string{
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_PROFILE",
		"AWS_SHARED_CREDENTIALS_FILE",
		"aws_access_key",
		"aws_secret_access_key",
	}
	for env, body := range managedManifests(t) {
		for _, f := range forbidden {
			if strings.Contains(body, f) {
				t.Fatalf("%s manifest contains %q. Credentials come from the default chain "+
					"(IRSA) only — a static key here is permanent, is in git, and is in every "+
					"checkout.", env, f)
			}
		}
	}
}

// ─── IRSA is present and environment-specific ────────────────────────

func TestBothManagedEnvironmentsCarryAnIRSARole(t *testing.T) {
	for env, body := range managedManifests(t) {
		if !strings.Contains(body, "irsaRoleArn") {
			t.Fatalf("%s manifest has no irsaRoleArn; without it the pod has no AWS identity "+
				"and KMS is unreachable, so the service will refuse to start", env)
		}
	}
}

var roleARN = regexp.MustCompile(`role/[a-zA-Z0-9._-]+`)

// Prod and staging must not share a role. A shared role makes the CMK
// resource restriction the only separation, and one misconfiguration then
// gives staging the ability to decrypt production customer data.
func TestProdAndStagingUseDifferentIRSARoles(t *testing.T) {
	m := managedManifests(t)
	p := roleARN.FindString(m["prod"])
	s := roleARN.FindString(m["staging"])
	if p == "" || s == "" {
		t.Fatalf("could not find an IRSA role in both manifests (prod=%q staging=%q)", p, s)
	}
	if p == s {
		t.Fatalf("prod and staging share the IRSA role %q; the environments are not isolated", p)
	}
}

// ─── No wildcards ────────────────────────────────────────────────────

// A wildcard resource or action in anything KMS-shaped defeats the whole
// point of a customer-managed key.
func TestNoWildcardKMSGrantsInManifests(t *testing.T) {
	patterns := []*regexp.Regexp{
		regexp.MustCompile(`(?i)kms:\*`),
		regexp.MustCompile(`(?i)"Resource"\s*:\s*"\*"`),
		regexp.MustCompile(`(?i)resource:\s*['"]?\*`),
		regexp.MustCompile(`(?i)arn:aws:kms:[^\s'"]*:\*`),
	}
	for env, body := range managedManifests(t) {
		for _, p := range patterns {
			if p.MatchString(body) {
				t.Fatalf("%s manifest contains a wildcard KMS grant matching %s; the pod would be "+
					"able to decrypt beyond its own environment's CMK", env, p)
			}
		}
	}
}

// ─── Required configuration is present ───────────────────────────────

// Each of these is something whose absence fails at boot. Catching it here
// costs a test run; catching it in staging costs a deploy, and in production
// costs an outage.
func TestManagedManifestsCarryEveryRequiredPIISetting(t *testing.T) {
	required := map[string]string{
		"COMMERCE_KMS_KEY_ID":      "the CMK that wraps every data key",
		"COMMERCE_PII_LOOKUP_SALT": "the deterministic address lookup hash",
		"COMMERCE_PII_CUTOVER":     "which half of the two-deploy PII cutover this image runs",
		"ENV":                      "the environment, which binds the KMS encryption context",
	}
	for env, body := range managedManifests(t) {
		for key, why := range required {
			if !strings.Contains(body, key) {
				t.Fatalf("%s manifest is missing %s (%s)", env, key, why)
			}
		}
	}
}

// Managed environments must never carry development keys. A static data key
// in prod or staging is the exact failure buildPIICipher refuses at boot; if
// one appeared here it would mean somebody intended to use it.
func TestManagedManifestsCarryNoDevelopmentKeys(t *testing.T) {
	for env, body := range managedManifests(t) {
		for _, k := range []string{"COMMERCE_PII_DEV_KEY_PROFILE", "COMMERCE_PII_DEV_KEY_SNAPSHOT"} {
			if strings.Contains(body, k) {
				t.Fatalf("%s manifest carries %s; a process-local data key is not key management, "+
					"and the service refuses to start with one", env, k)
			}
		}
	}
}

// The ENV value must be one the service classifies as managed. A manifest
// saying "production" while the code only recognises "prod" would fail
// closed — correctly, but at boot, in production.
func TestManagedManifestsDeclareARecognisedEnvironment(t *testing.T) {
	cases := map[string][]string{
		"prod":    {"ENV: prod", "ENV: production"},
		"staging": {"ENV: staging", "ENV: stage"},
	}
	m := managedManifests(t)
	for env, accepted := range cases {
		ok := false
		for _, a := range accepted {
			if strings.Contains(m[env], a) {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("%s manifest does not declare a recognised ENV (want one of %v); the "+
				"service fails closed on an unclassifiable environment", env, accepted)
		}
	}
}

// ─── D: the deployed vocabulary must match the enforced one ──────────

// COMMERCE_PAYMENT_METHODS is inert documentation. This is what keeps it from
// becoming a lie: it advertised 'upi,card,net_banking,wallet' while the server
// had refused all but two since B6.
func TestDeployedPaymentMethodsMatchTheEnforcedVocabulary(t *testing.T) {
	// Deliberately the literal expected value rather than an import of
	// shared/paymentmethod: this asserts that the MANIFEST says what the
	// launch decision is. The Go-side drift guard lives in CI and compares
	// the Go allowlist with the Android enum.
	const want = "COMMERCE_PAYMENT_METHODS: 'upi,card'"
	for env, body := range managedManifests(t) {
		if !strings.Contains(body, want) {
			t.Fatalf("%s manifest does not declare exactly %s. The deployed documentation must "+
				"not contradict what the handler, the service, the store and the gated CHECK "+
				"enforce.", env, want)
		}
	}
}

// The cutover must start in the safe mode. Shipping 'ciphertext' before the
// backfill has run would make every legacy address unreadable at once.
func TestManagedManifestsStartInDualCutoverMode(t *testing.T) {
	for env, body := range managedManifests(t) {
		if !strings.Contains(body, "COMMERCE_PII_CUTOVER: 'dual'") {
			t.Fatalf("%s manifest does not start in dual cutover mode. Shipping 'ciphertext' "+
				"before the backfill completes makes every legacy address unreadable, and the "+
				"switch is a deliberate second deploy.", env)
		}
	}
}

// ─── Azure is parked and must not be edited ──────────────────────────

// Founder decision: AWS is canonical, Azure is parked. This asserts the Azure
// values were not dragged along by a well-meaning search-and-replace.
func TestAzureValuesAreUntouchedByTheKMSWork(t *testing.T) {
	for _, p := range []string{
		"../../../../../deploy/services/commerce-service/values-azure-prod.yaml",
		"../../../../../deploy/services/commerce-service/values-azure-staging.yaml",
	} {
		b, err := os.ReadFile(filepath.Clean(p))
		if err != nil {
			t.Skipf("%s not present: %v", p, err)
		}
		if strings.Contains(string(b), "COMMERCE_PII_CUTOVER") {
			t.Fatalf("%s was edited by this pass; Azure is parked and out of scope", p)
		}
	}
}
