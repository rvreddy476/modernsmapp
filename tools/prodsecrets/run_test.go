package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cliFixture lays out what scripts/prodsecrets.sh leaves before a run:
// tf-outputs.json, sources/ and current/ (every secret an unversioned shell).
func cliFixture(t *testing.T) (manifest, out string) {
	t.Helper()
	dir := t.TempDir()
	manifest = filepath.Join(dir, "manifest.yaml")
	if err := os.WriteFile(manifest, []byte(testManifest), 0o600); err != nil {
		t.Fatal(err)
	}
	out = filepath.Join(dir, "out")
	tf, _ := json.Marshal(testTF())
	os.MkdirAll(filepath.Join(out, "current"), 0o700)
	os.MkdirAll(filepath.Join(out, "sources"), 0o700)
	os.WriteFile(filepath.Join(out, "tf-outputs.json"), tf, 0o600)
	for alias, fields := range testSources() {
		b, _ := json.Marshal(fields)
		os.WriteFile(filepath.Join(out, "sources", alias+".json"), b, 0o600)
	}
	for _, s := range []string{"commerce-service", "payments-service", "identity-auth-service", "empty-shell"} {
		os.WriteFile(filepath.Join(out, "current", s+".json"), nil, 0o600)
	}
	return manifest, out
}

func withPrompter(t *testing.T, p Prompter) {
	t.Helper()
	old := newPrompter
	newPrompter = func(io.Writer) Prompter { return p }
	t.Cleanup(func() { newPrompter = old })
}

// TestNoSecretValueEverReachesTheLogs runs full applies through the CLI,
// capturing stdout and stderr, and checks that no value from the payloads,
// roles.sql, the Terraform sources or a prompt answer appears in the logs
// or in state.json.
func TestNoSecretValueEverReachesTheLogs(t *testing.T) {
	manifest, out := cliFixture(t)
	dir := filepath.Dir(out)
	fcm := filepath.Join(dir, "fcm.json")
	os.WriteFile(fcm, []byte(`{"type":"service_account","private_key":"FIREBASEPRIVATEKEYMATERIAL0001"}`), 0o600)
	withPrompter(t, &MapPrompter{Answers: map[string]string{
		"Razorpay key id":           "rzp_live_ABCDEF0123456789",
		"Path to the Firebase JSON": fcm,
	}})

	var stdout, stderr bytes.Buffer
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out, "--verbose"}, &stdout, &stderr); err != nil {
		t.Fatalf("run: %v\n%s", err, stderr.String())
	}
	logs := stdout.String() + stderr.String()
	if !strings.Contains(logs, "prodsecrets APPLY") || !strings.Contains(logs, "put-secret-values.sh") {
		t.Fatalf("report missing:\n%s", logs)
	}

	var values []string
	payloads, _, err := LoadValueDir(filepath.Join(out, "secrets"))
	if err != nil {
		t.Fatal(err)
	}
	for _, keys := range payloads {
		for _, v := range keys {
			values = append(values, v)
		}
	}
	sql, _ := os.ReadFile(filepath.Join(out, "roles.sql"))
	for _, line := range strings.Split(string(sql), "\n") {
		if i := strings.Index(line, "PASSWORD '"); i >= 0 {
			values = append(values, strings.TrimSuffix(line[i+len("PASSWORD '"):], "';"))
		}
	}
	for _, fields := range testSources() {
		for _, v := range fields {
			values = append(values, v)
		}
	}
	values = append(values, "rzp_live_ABCDEF0123456789", "FIREBASEPRIVATEKEYMATERIAL0001")
	// Public halves are public (written to out/public on purpose).
	public := map[string]bool{}
	pubFiles, _ := filepath.Glob(filepath.Join(out, "public", "*"))
	for _, f := range pubFiles {
		b, _ := os.ReadFile(f)
		for _, line := range strings.Split(string(b), "\n") {
			public[strings.TrimSpace(line)] = true
		}
	}
	if len(pubFiles) != 2 {
		t.Fatalf("public files: %v", pubFiles)
	}
	state, _ := os.ReadFile(filepath.Join(out, "state.json"))
	checked := 0
	for _, v := range values {
		for _, line := range strings.Split(v, "\n") {
			line = strings.TrimSpace(line)
			if len(line) < 8 || strings.HasPrefix(line, "-----") || public[line] {
				continue
			}
			checked++
			if strings.Contains(logs, line) {
				t.Fatalf("a secret value appeared in the logs (%d chars)", len(line))
			}
			if strings.Contains(string(state), line) {
				t.Fatalf("a secret value appeared in state.json (%d chars)", len(line))
			}
		}
	}
	if checked < 25 {
		t.Fatalf("only %d values checked; the fixture is not what the test expects", checked)
	}

	// Second apply before any push: reuses the unpushed payloads.
	stdout.Reset()
	stderr.Reset()
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "reuse") || strings.Contains(stdout.String(), " create ") {
		t.Fatalf("second run should reuse everything:\n%s", stdout.String())
	}

	// Simulate the push: Secrets Manager now holds every payload.
	for name, keys := range payloads {
		b, _ := json.Marshal(keys)
		os.WriteFile(filepath.Join(out, "current", name+".json"), b, 0o600)
		os.Remove(filepath.Join(out, "secrets", name+".json"))
	}
	before, _ := os.ReadFile(filepath.Join(out, "state.json"))
	stdout.Reset()
	if err := run([]string{"--plan", "--manifest", manifest, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(filepath.Join(out, "state.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("plan changed state.json")
	}
	if !strings.Contains(stdout.String(), "PLAN (nothing written)") || strings.Contains(stdout.String(), " create ") || strings.Contains(stdout.String(), " reuse ") {
		t.Fatalf("plan after push should keep everything:\n%s", stdout.String())
	}
}

func TestRedactorMasksKnownValuesInOutput(t *testing.T) {
	m := mustParse(t, testManifest)
	in := applyInputs(nil)
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	red := NewRedactor(p, in)
	var buf bytes.Buffer
	w := red.Writer(&buf)
	secret := keyOf(t, p, "commerce-service", "commerce_pii_lookup_salt").Value
	pem := keyOf(t, p, "identity-auth-service", "mini_app_session_private_key_pem").Value
	pemLine := strings.Split(pem, "\n")[1]
	io.WriteString(w, "oops "+secret+" and a pem line "+pemLine+" and the dsn "+keyOf(t, p, "commerce-service", "postgres_dsn").Value+" and "+testSCRAM+"\n")
	got := buf.String()
	if strings.Contains(got, secret) || strings.Contains(got, pemLine) || strings.Contains(got, p.RolePasswords["commerce_service"]) || strings.Contains(got, testSCRAM) {
		t.Fatalf("redactor let a value through")
	}
	if !strings.Contains(got, "[REDACTED]") {
		t.Fatal("nothing redacted")
	}
	buf.Reset()
	io.WriteString(w, "kid c1 hs-1 ok\n")
	if buf.String() != "kid c1 hs-1 ok\n" {
		t.Fatalf("over-redaction: %q", buf.String())
	}
}

func TestApplyGuards(t *testing.T) {
	var stdout, stderr bytes.Buffer
	withPrompter(t, &MapPrompter{})

	// Without current/ the tool cannot know what is filled: refuse.
	manifest, out := cliFixture(t)
	os.RemoveAll(filepath.Join(out, "current"))
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "needs what Secrets Manager holds") {
		t.Fatalf("expected refusal without current/, got %v", err)
	}
	// One secret missing from current/ is refused too.
	manifest, out = cliFixture(t)
	os.Remove(filepath.Join(out, "current", "payments-service.json"))
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil {
		t.Fatal("expected refusal with a secret not fetched")
	}
	// Terraform values missing: plan shows them, apply refuses and writes nothing.
	manifest, out = cliFixture(t)
	os.RemoveAll(filepath.Join(out, "sources"))
	stdout.Reset()
	if err := run([]string{"--plan", "--manifest", manifest, "--out", out}, &stdout, &stderr); err != nil || !strings.Contains(stdout.String(), "Source secrets not fetched") {
		t.Fatalf("plan: %v\n%s", err, stdout.String())
	}
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "Terraform values missing") {
		t.Fatalf("expected refusal with sources missing, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "secrets")); !os.IsNotExist(err) {
		t.Fatal("a refused apply must not write payloads")
	}
	// A mismatched key pair is refused before anything is written.
	manifest, out = cliFixture(t)
	pair, _ := Generator{}.Pair(GenEd25519)
	b, _ := json.Marshal(map[string]string{"caller_commerce_pubkey": pair.Public})
	os.WriteFile(filepath.Join(out, "current", "payments-service.json"), b, 0o600)
	stdout.Reset()
	if err := run([]string{"--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil || !strings.Contains(stdout.String(), "KEY PAIR MISMATCH") {
		t.Fatalf("expected pair refusal, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "secrets")); !os.IsNotExist(err) {
		t.Fatal("a refused apply must not write payloads")
	}
}

func TestOutDirInsideRepoRefused(t *testing.T) {
	repo := t.TempDir()
	os.Mkdir(filepath.Join(repo, ".git"), 0o700)
	if err := checkOutDir(filepath.Join(repo, "anything", "out")); err == nil {
		t.Fatal("an out dir inside a checkout must be refused")
	}
	if err := checkOutDir(filepath.Join(repo, "tools", "prodsecrets", "out")); err != nil {
		t.Fatalf("the gitignored out dir is allowed: %v", err)
	}
	if err := checkOutDir(t.TempDir()); err != nil {
		t.Fatalf("outside a checkout: %v", err)
	}
	if !strings.Contains(DefaultOutDir(), filepath.Join(".atpost", "prodsecrets")) {
		t.Fatalf("default out dir %s", DefaultOutDir())
	}
}

func TestCLIFlags(t *testing.T) {
	manifest, out := cliFixture(t)
	var stdout, stderr bytes.Buffer
	if err := run([]string{"--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil || !strings.Contains(err.Error(), "--plan or --apply") {
		t.Fatalf("expected mode error, got %v", err)
	}
	if err := run([]string{"--plan", "--apply", "--manifest", manifest, "--out", out}, &stdout, &stderr); err == nil {
		t.Fatal("both modes must be refused")
	}
	if err := run([]string{"--plan", "--manifest", manifest, "--out", out, "stray"}, &stdout, &stderr); err == nil {
		t.Fatal("stray arguments must be refused")
	}
	stdout.Reset()
	if err := run([]string{"--list", "--manifest", manifest, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "commerce-service atpost/test/commerce-service\n") {
		t.Fatalf("list: %q", stdout.String())
	}
	stdout.Reset()
	if err := run([]string{"--list-sources", "--manifest", manifest, "--out", out}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if stdout.String() != "platform-auth atpost/test/platform-auth\nmsk-scram AmazonMSK_atpost-test-atpost\n" {
		t.Fatalf("list-sources: %q", stdout.String())
	}
}
