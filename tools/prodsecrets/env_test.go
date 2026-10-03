package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// manifestStrings is every expanded string of a manifest that ends up in a
// secret name, a source id, a value or a prompt label.
func manifestStrings(m *Manifest) []string {
	out := []string{m.Prefix}
	for _, s := range m.Sources {
		out = append(out, s.Ref)
	}
	for _, e := range m.Shared {
		out = append(out, e.Spec.Arg)
	}
	for _, s := range m.Secrets {
		out = append(out, m.FullName(s.Name))
		for _, k := range s.Keys {
			out = append(out, k.Spec.Arg)
		}
	}
	return out
}

// A production host is app./api./ws./admin./media.cleestudio.com or the bare
// domain (no-reply@cleestudio.com); the QA names (api-qa., qa.) are not.
var prodHostRe = regexp.MustCompile(`(^|[^a-z0-9.-])((app|api|ws|admin|media)\.)?cleestudio\.com`)

func TestProdHostPatternItself(t *testing.T) {
	for _, s := range []string{"https://api.cleestudio.com/v1", "app.cleestudio.com", "no-reply@cleestudio.com", "wss://ws.cleestudio.com", "media.cleestudio.com"} {
		if !prodHostRe.MatchString(s) {
			t.Errorf("%q should count as a production host", s)
		}
	}
	for _, s := range []string{"https://api-qa.cleestudio.com/v1", "qa.cleestudio.com", "no-reply@qa.cleestudio.com", "ws-qa.cleestudio.com", "admin-qa.cleestudio.com", "media-qa.cleestudio.com"} {
		if prodHostRe.MatchString(s) {
			t.Errorf("%q is a QA host", s)
		}
	}
}

// QA names and labels never carry a production host, prefix or live key
// hint; production ones never carry a QA marker.
func TestEnvTemplatingKeepsEnvironmentsApart(t *testing.T) {
	qa := loadCommitted(t, "qa")
	prod := loadCommitted(t, "prod")
	checked := 0
	for _, s := range manifestStrings(qa) {
		checked++
		if prodHostRe.MatchString(s) {
			t.Errorf("qa: %q names a production host", s)
		}
		low := strings.ToLower(s)
		for _, bad := range []string{"atpost/prod", "atpost-prod", "rzp_live", "production", " live "} {
			if strings.Contains(low, bad) {
				t.Errorf("qa: %q contains %q", s, bad)
			}
		}
	}
	for _, s := range manifestStrings(prod) {
		for _, bad := range []string{"-qa", "atpost/qa", "qa.cleestudio.com", "rzp_test", "test-mode", "${"} {
			if strings.Contains(strings.ToLower(s), bad) {
				t.Errorf("prod: %q contains %q", s, bad)
			}
		}
	}
	if checked < 300 {
		t.Fatalf("only %d qa strings checked", checked)
	}
	for _, s := range qa.Secrets {
		if !strings.HasPrefix(qa.FullName(s.Name), "atpost/qa/") {
			t.Errorf("qa secret %s", qa.FullName(s.Name))
		}
	}
	for _, s := range qa.Sources {
		if !strings.HasPrefix(s.Ref, "atpost/qa/") && !strings.HasPrefix(s.Ref, "tf:") {
			t.Errorf("qa source %s reads %s", s.Alias, s.Ref)
		}
	}
	src, _ := qa.SourceByAlias("platform-auth")
	if src.Ref != "atpost/qa/platform-auth" {
		t.Errorf("qa platform-auth source is %s", src.Ref)
	}
}

// The contract's QA names, exactly.
func TestQAVarsMatchTheContract(t *testing.T) {
	qa := loadCommitted(t, "qa")
	want := map[string]string{
		"web_host":            "qa.cleestudio.com",
		"api_host":            "api-qa.cleestudio.com",
		"ws_host":             "ws-qa.cleestudio.com",
		"admin_host":          "admin-qa.cleestudio.com",
		"media_host":          "media-qa.cleestudio.com",
		"mail_from":           "no-reply@qa.cleestudio.com",
		"razorpay_key_prefix": "rzp_test_",
	}
	for k, v := range want {
		if qa.Vars[k] != v {
			t.Errorf("qa %s = %q, want %q", k, qa.Vars[k], v)
		}
	}
	prod := loadCommitted(t, "prod")
	if prod.Vars["api_host"] != "api.cleestudio.com" || prod.Vars["razorpay_key_prefix"] != "rzp_live_" {
		t.Errorf("prod vars changed: %v", prod.Vars)
	}
	// The label's prefix and the enforced rule must agree.
	for env, m := range map[string]*Manifest{"qa": qa, "prod": prod} {
		e, _ := m.SharedEntry("razorpay_key_id")
		want, _ := requiredPrefix(env, "razorpay_key_id")
		if !strings.Contains(e.Spec.Arg, want) {
			t.Errorf("%s: razorpay_key_id label %q does not ask for %s", env, e.Spec.Arg, want)
		}
	}
	webhook, _ := qa.SharedEntry("razorpay_webhook_secret")
	if !strings.Contains(webhook.Spec.Arg, "https://api-qa.cleestudio.com/v1/payments/webhook") {
		t.Errorf("qa webhook label: %q", webhook.Spec.Arg)
	}
}

// Everything environment-specific lives in the environments block: the
// unexpanded file names no host, prefix or key mode anywhere else.
func TestManifestHasNoHardCodedEnvironment(t *testing.T) {
	f, err := os.Open("manifest.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	root, err := parseYAMLSubset(f)
	if err != nil {
		t.Fatal(err)
	}
	var walk func(n *node)
	n := 0
	walk = func(nd *node) {
		for _, c := range nd.children {
			if nd == root && c.key == "environments" {
				continue
			}
			if c.hasValue {
				n++
				for _, bad := range []string{"cleestudio.com", "atpost/prod", "atpost/qa", "atpost-prod", "atpost-qa", "rzp_live", "rzp_test", "PRODUCTION"} {
					if strings.Contains(c.value, bad) {
						t.Errorf("manifest line %d: %q is hard-coded (use an environments var)", c.line, bad)
					}
				}
			}
			walk(c)
		}
	}
	walk(root)
	if n < 300 {
		t.Fatalf("only %d values walked", n)
	}
}

// prod leaves out what its Terraform creates no shell for; qa fills it.
func TestOmitSecretsPerEnvironment(t *testing.T) {
	prod := loadCommitted(t, "prod")
	qa := loadCommitted(t, "qa")
	has := func(m *Manifest, name string) bool {
		for _, s := range m.Secrets {
			if s.Name == name {
				return true
			}
		}
		return false
	}
	if has(prod, "argocd-repo-modernsmapp") || !has(qa, "argocd-repo-modernsmapp") {
		t.Fatalf("argocd-repo-modernsmapp: prod %v, qa %v", has(prod, "argocd-repo-modernsmapp"), has(qa, "argocd-repo-modernsmapp"))
	}
	if qa.FullName("argocd-repo-modernsmapp") != "atpost/qa/argocd-repo-modernsmapp" {
		t.Fatal(qa.FullName("argocd-repo-modernsmapp"))
	}
	if len(prod.Secrets) != 34 || len(qa.Secrets) != 35 {
		t.Fatalf("secrets: prod %d, qa %d", len(prod.Secrets), len(qa.Secrets))
	}
}

func TestEnvironmentsBlockRejects(t *testing.T) {
	base := "version: 1\nprefix: atpost/${env}\n"
	cases := map[string]string{
		"unknown env":        base + "environments:\n  prod:\n    vars:\n      a: x\n",
		"unknown token":      base + "environments:\n  qa:\n    vars:\n      a: x\nshared:\n  s: const:${nope}\n",
		"missing var":        base + "environments:\n  qa:\n    vars:\n      a: x\n  prod:\n    vars:\n      b: y\n",
		"env redefined":      base + "environments:\n  qa:\n    vars:\n      env: x\n",
		"omit unknown":       base + "environments:\n  qa:\n    omit_secrets: ghost\n",
		"omit typo in other": base + "environments:\n  qa:\n    vars:\n      a: x\n  prod:\n    omit_secrets: ghost\n    vars:\n      a: y\n",
		"unknown env key":    base + "environments:\n  qa:\n    hosts:\n      a: x\n",
		"malformed token":    base + "environments:\n  qa:\n    vars:\n      a: x\nshared:\n  s: const:${a\n",
		"token in var":       base + "environments:\n  qa:\n    vars:\n      a: ${env}\n",
		"bad env name":       "",
	}
	for name, src := range cases {
		env := "qa"
		if name == "bad env name" {
			if _, err := ParseManifestEnv(strings.NewReader(base), "QA!"); err == nil {
				t.Errorf("%s: expected an error", name)
			}
			continue
		}
		if _, err := ParseManifestEnv(strings.NewReader(src), env); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
	// No environments block: only ${env} expands.
	m, err := ParseManifestEnv(strings.NewReader(base+"shared:\n  s: const:x-${env}-y\n"), "qa")
	if err != nil {
		t.Fatal(err)
	}
	if e, _ := m.SharedEntry("s"); m.Prefix != "atpost/qa" || e.Spec.Arg != "x-qa-y" {
		t.Fatalf("expanded: %q %q", m.Prefix, e.Spec.Arg)
	}
}

// ---------------------------------------------------------------------------
// Razorpay key mode

func TestRazorpayPrefixRule(t *testing.T) {
	cases := []struct {
		env, value string
		ok         bool
	}{
		{"prod", "rzp_live_ABC123", true},
		{"prod", "rzp_test_ABC123", false},
		{"prod", "ABC123", false},
		{"qa", "rzp_test_ABC123", true},
		{"qa", "rzp_live_ABC123", false},
		{"qa", " rzp_test_ABC123", false},
		{"qa", "RZP_TEST_ABC123", false},
		{"qa", "", true}, // empty is "not answered", handled as pending
	}
	for _, c := range cases {
		err := checkPrefix(c.env, "razorpay_key_id", c.value)
		if (err == nil) != c.ok {
			t.Errorf("%s %q: err %v, want ok=%v", c.env, c.value, err, c.ok)
		}
		if err != nil && c.value != "" && strings.Contains(err.Error(), c.value) {
			t.Errorf("the refusal echoes the value")
		}
	}
	if err := checkPrefix("qa", "razorpay_key_secret", "anything"); err != nil {
		t.Errorf("only the key id has a prefix rule: %v", err)
	}
}

// refusingPrompter always types the wrong-mode key id.
type refusingPrompter struct{ asked []string }

func (r *refusingPrompter) Ask(label string) (string, error) {
	r.asked = append(r.asked, label)
	if strings.Contains(label, "Razorpay") {
		return "rzp_live_REALMONEYKEY000001", nil
	}
	return "", nil
}

func TestQAPromptRefusesALiveKey(t *testing.T) {
	m, err := ParseManifestEnv(strings.NewReader(testManifest), "qa")
	if err != nil {
		t.Fatal(err)
	}
	// Typed once wrong, then left empty: the live key is never kept.
	in := applyInputs(map[string]string{"Razorpay key id": "rzp_live_REALMONEYKEY000001"})
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	k := keyOf(t, p, "payments-service", "razorpay_key_id")
	if k.Value != "" || k.Action != ActPending {
		t.Fatalf("a live key id was accepted in qa: action %s", k.Action)
	}
	asked := in.Prompter.(*MapPrompter).Asked
	reasked := false
	for _, a := range asked {
		if strings.HasPrefix(a, "Razorpay key id\n") && strings.Contains(a, "REFUSED") && strings.Contains(a, "rzp_test_") {
			reasked = true
		}
	}
	if !reasked {
		t.Fatalf("expected a re-ask that says why: %q", asked)
	}
	for _, a := range asked {
		if strings.Contains(a, "REALMONEYKEY") {
			t.Fatal("the re-ask label echoes the value")
		}
	}
	// Typed wrong every time: refused after maxPromptAttempts, value not shown.
	rp := &refusingPrompter{}
	in = applyInputs(nil)
	in.Prompter = rp
	_, err = Resolve(m, in)
	if err == nil || !strings.Contains(err.Error(), "rzp_test_") {
		t.Fatalf("expected refusal, got %v", err)
	}
	if strings.Contains(err.Error(), "REALMONEYKEY") {
		t.Fatal("the error echoes the value")
	}
	n := 0
	for _, a := range rp.asked {
		if strings.Contains(a, "Razorpay") {
			n++
		}
	}
	if n != maxPromptAttempts {
		t.Fatalf("asked %d times, want %d", n, maxPromptAttempts)
	}
	// A test-mode key is accepted in qa.
	p, err = Resolve(m, applyInputs(map[string]string{"Razorpay key id": "rzp_test_OK0000000001"}))
	if err != nil {
		t.Fatal(err)
	}
	if keyOf(t, p, "payments-service", "razorpay_key_id").Value != "rzp_test_OK0000000001" {
		t.Fatal("a test key id was not accepted in qa")
	}
}

func TestProdPromptRefusesATestKey(t *testing.T) {
	m := mustParse(t, testManifest) // prod
	in := applyInputs(map[string]string{"Razorpay key id": "rzp_test_NOTFORPROD0001"})
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if v := keyOf(t, p, "payments-service", "razorpay_key_id").Value; v != "" {
		t.Fatal("a test key id was accepted in prod")
	}
	p, err = Resolve(m, applyInputs(map[string]string{"Razorpay key id": "rzp_live_FORPROD000001"}))
	if err != nil || keyOf(t, p, "payments-service", "razorpay_key_id").Value != "rzp_live_FORPROD000001" {
		t.Fatalf("a live key id was not accepted in prod: %v", err)
	}
}

// A wrong-mode value already in Secrets Manager (or an unpushed payload)
// never went through the prompt: plan reports it, apply refuses.
func TestKeptWrongModeKeyIsRefused(t *testing.T) {
	m, err := ParseManifestEnv(strings.NewReader(testManifest), "qa")
	if err != nil {
		t.Fatal(err)
	}
	in := applyInputs(nil)
	in.Current = map[string]map[string]string{"payments-service": {"razorpay_key_id": "rzp_live_ALREADYTHERE0001"}}
	p, err := Resolve(m, in)
	if err == nil || !strings.Contains(err.Error(), "--rotate shared/razorpay_key_id") {
		t.Fatalf("apply must refuse a kept live key in qa, got %v", err)
	}
	if strings.Contains(err.Error(), "ALREADYTHERE") {
		t.Fatal("the error echoes the value")
	}
	var buf bytes.Buffer
	PrintPlan(&buf, m, p, false, false)
	if !strings.Contains(buf.String(), "WRONG MODE FOR QA") || strings.Contains(buf.String(), "ALREADYTHERE") {
		t.Fatalf("plan report:\n%s", buf.String())
	}
	// Rotating it asks again and replaces it.
	in = applyInputs(map[string]string{"Razorpay key id": "rzp_test_REPLACED00001"})
	in.Current = map[string]map[string]string{"payments-service": {"razorpay_key_id": "rzp_live_ALREADYTHERE0001"}}
	in.Rotate = map[string]bool{"shared/razorpay_key_id": true}
	p, err = Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if k := keyOf(t, p, "payments-service", "razorpay_key_id"); k.Action != ActUpdate || k.Value != "rzp_test_REPLACED00001" {
		t.Fatalf("rotation: %s", k.Action)
	}
	// The same kept live key is fine in prod.
	in = applyInputs(nil)
	in.Current = map[string]map[string]string{"payments-service": {"razorpay_key_id": "rzp_live_ALREADYTHERE0001"}}
	if _, err := Resolve(mustParse(t, testManifest), in); err != nil {
		t.Fatalf("prod: %v", err)
	}
}

// ---------------------------------------------------------------------------
// CLI

const envManifest = `
version: 1
environments:
  prod:
    vars:
      host: api.example.com
  qa:
    vars:
      host: api-qa.example.com
prefix: atpost/${env}
sources:
  platform-auth: atpost/${env}/platform-auth
shared:
  razorpay_key_id: prompt:Razorpay key id (webhook https://${host})
secrets:
  payments-service:
    internal_service_key: copy:platform-auth#internal_service_key
    razorpay_key_id: shared:razorpay_key_id
`

func TestCLIEnvironmentFlag(t *testing.T) {
	dir := t.TempDir()
	manifest := filepath.Join(dir, "manifest.yaml")
	os.WriteFile(manifest, []byte(envManifest), 0o600)
	var stdout, stderr bytes.Buffer

	qaOut := filepath.Join(dir, "qa")
	if err := run([]string{"--env", "qa", "--list", "--manifest", manifest, "--out", qaOut}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "payments-service atpost/qa/payments-service\n" {
		t.Fatalf("qa list: %q", stdout.String())
	}
	stdout.Reset()
	os.MkdirAll(qaOut, 0o700)
	os.WriteFile(filepath.Join(qaOut, "tf-outputs.json"), []byte("{}"), 0o600)
	if err := run([]string{"--env", "qa", "--list-sources", "--manifest", manifest, "--out", qaOut}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "platform-auth atpost/qa/platform-auth\n" {
		t.Fatalf("qa sources: %q", stdout.String())
	}
	stdout.Reset()
	if err := run([]string{"--plan", "--env", "qa", "--manifest", manifest, "--out", qaOut}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "environment qa") || !strings.Contains(stdout.String(), "scripts/prodsecrets.sh --env qa apply") {
		t.Fatalf("qa plan report:\n%s", stdout.String())
	}

	// Default is prod, unchanged.
	stdout.Reset()
	if err := run([]string{"--list", "--manifest", manifest, "--out", filepath.Join(dir, "prod")}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "payments-service atpost/prod/payments-service\n" {
		t.Fatalf("prod list: %q", stdout.String())
	}

	// Unknown environment, and another environment's working directory.
	if err := run([]string{"--env", "staging", "--list", "--manifest", manifest, "--out", qaOut}, &stdout, &stderr); err == nil {
		t.Fatal("an unknown environment must be refused")
	}
	if err := run([]string{"--env", "qa", "--list", "--manifest", manifest, "--out", filepath.Join(dir, "prod")}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "belongs to the prod environment") {
		t.Fatalf("qa with the prod directory must be refused, got %v", err)
	}
	if err := run([]string{"--list", "--manifest", manifest, "--out", qaOut}, &stdout, &stderr); err == nil {
		t.Fatal("prod with the qa directory must be refused")
	}
	if !strings.HasSuffix(DefaultOutDir("qa"), filepath.Join("prodsecrets", "qa")) {
		t.Fatalf("default qa dir %s", DefaultOutDir("qa"))
	}
	if wrapperCmd("prod") != "scripts/prodsecrets.sh" {
		t.Fatal("the production wording must not change")
	}
}
