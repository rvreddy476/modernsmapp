package main

import (
	"crypto/ed25519"
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testTF() map[string]string {
	return map[string]string{
		"aurora_cluster_endpoint":      "aurora.internal",
		"commerce_pii_kms_key_id":      "arn:aws:kms:ap-south-1:1:key/abc",
		"msk_scram_secret_name":        "AmazonMSK_atpost-test-atpost",
		"elasticache_primary_endpoint": "cache.internal",
	}
}

const (
	testISK    = "PLATFORM-INTERNAL-KEY-FROM-TERRAFORM-0001"
	testJWTPub = "-----BEGIN PUBLIC KEY-----\nTERRAFORMPUBLICKEYBODY0001\n-----END PUBLIC KEY-----\n"
	testSCRAM  = "MSK-SCRAM-PASSWORD-FROM-TERRAFORM-0001"
)

func testSources() map[string]map[string]string {
	return map[string]map[string]string{
		"platform-auth": {"internal_service_key": testISK, "jwt_public_key_pem": testJWTPub},
		"msk-scram":     {"username": "atpost", "password": testSCRAM},
	}
}

func keyOf(t *testing.T, p *Plan, secret, key string) KeyResult {
	t.Helper()
	for _, s := range p.Secrets {
		if s.Name != secret {
			continue
		}
		for _, k := range s.Keys {
			if k.Key == key {
				return k
			}
		}
	}
	t.Fatalf("%s.%s not in plan", secret, key)
	return KeyResult{}
}

func applyInputs(prompts map[string]string) Inputs {
	return Inputs{TF: testTF(), Sources: testSources(), Apply: true, Prompter: &MapPrompter{Answers: prompts}}
}

// payloadsOf turns a plan into what the emitted secrets/*.json would hold.
func payloadsOf(p *Plan) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, s := range p.Secrets {
		if s.Changed() {
			out[s.Name] = s.Payload()
		}
	}
	return out
}

func TestKeypairPairsCallerWithReceiver(t *testing.T) {
	m := mustParse(t, testManifest)
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	priv := keyOf(t, p, "commerce-service", "commerce_service_token_key")
	pub := keyOf(t, p, "payments-service", "caller_commerce_pubkey")
	if priv.Action != ActCreate || pub.Action != ActCreate {
		t.Fatalf("actions %s/%s", priv.Action, pub.Action)
	}
	seed, err := base64.StdEncoding.DecodeString(priv.Value)
	if err != nil {
		t.Fatal(err)
	}
	pubBytes, err := base64.StdEncoding.DecodeString(pub.Value)
	if err != nil {
		t.Fatal(err)
	}
	sig := ed25519.Sign(ed25519.NewKeyFromSeed(seed), []byte("x"))
	if !ed25519.Verify(ed25519.PublicKey(pubBytes), []byte("x"), sig) {
		t.Fatal("receiver's public key does not verify the caller's signature")
	}
	if keyOf(t, p, "commerce-service", "commerce_service_token_kid").Value != "c1" || keyOf(t, p, "payments-service", "caller_commerce_kid").Value != "c1" {
		t.Fatal("kids must match on both sides")
	}
	rsaPriv := keyOf(t, p, "identity-auth-service", "mini_app_session_private_key_pem").Value
	rebuilt, err := pairFromPrivate(GenRSA2048, rsaPriv)
	if err != nil || rebuilt.Public != p.PublicHalves["mini_rs256"].Public {
		t.Fatalf("rsa halves do not pair: %v", err)
	}
	if len(p.PairErrors) != 0 {
		t.Fatalf("pair errors on a fresh plan: %v", p.PairErrors)
	}
}

func TestCopyAndDeriveReadTerraform(t *testing.T) {
	m := mustParse(t, testManifest)
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ secret, key, want string }{
		{"commerce-service", "internal_service_key", testISK},
		{"payments-service", "internal_service_key", testISK},
		{"payments-service", "jwt_public_key_pem", testJWTPub},
		{"payments-service", "kafka_sasl_password", testSCRAM},
		{"payments-service", "redis_addr", "cache.internal:6379"},
		{"commerce-service", "commerce_kms_key_id", "arn:aws:kms:ap-south-1:1:key/abc"},
		{"commerce-service", "courier_provider", "stub"},
	} {
		k := keyOf(t, p, c.secret, c.key)
		if k.Value != c.want || k.Action != ActCreate {
			t.Errorf("%s.%s: %s %q", c.secret, c.key, k.Action, k.Value)
		}
	}
	if k := keyOf(t, p, "commerce-service", "jwt_kid_previous"); k.Action != ActEmpty {
		t.Errorf("empty const: %s", k.Action)
	}
	// A source that was not fetched leaves its keys pending, never generated.
	in := applyInputs(nil)
	in.Sources = map[string]map[string]string{"platform-auth": {"internal_service_key": testISK}}
	in.TF = map[string]string{"aurora_cluster_endpoint": "aurora.internal"}
	p2, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if k := keyOf(t, p2, "payments-service", "kafka_sasl_password"); k.Action != ActPending || k.Value != "" {
		t.Fatalf("missing source: %s", k.Action)
	}
	if k := keyOf(t, p2, "payments-service", "jwt_public_key_pem"); k.Action != ActPending {
		t.Fatalf("missing field: %s", k.Action)
	}
	if k := keyOf(t, p2, "payments-service", "redis_addr"); k.Action != ActPending {
		t.Fatalf("missing derive input: %s", k.Action)
	}
	if strings.Join(p2.MissingSources, ",") != "msk-scram,platform-auth#jwt_public_key_pem" {
		t.Fatalf("missing sources: %v", p2.MissingSources)
	}
	if strings.Join(p2.MissingTF, ",") != "commerce_pii_kms_key_id,elasticache_primary_endpoint" {
		t.Fatalf("missing tf: %v", p2.MissingTF)
	}
}

func TestDSNDerivation(t *testing.T) {
	m := mustParse(t, testManifest)
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	dsn := keyOf(t, p, "commerce-service", "postgres_dsn")
	u, err := url.Parse(dsn.Value)
	if err != nil {
		t.Fatal(err)
	}
	pw, _ := u.User.Password()
	if u.Scheme != "postgres" || u.User.Username() != "commerce_service" || u.Host != "aurora.internal:5432" || u.Path != "/commerce_db" || u.Query().Get("sslmode") != "require" {
		t.Fatalf("dsn parts: %s %s %s %s %s", u.Scheme, u.User.Username(), u.Host, u.Path, u.RawQuery)
	}
	if len(pw) != 32 || pw != p.RolePasswords["commerce_service"] {
		t.Fatal("role password must be the generated one, recorded in the plan for roles.sql")
	}
	if strings.Contains(keyOf(t, p, "payments-service", "postgres_dsn").Value, pw) {
		t.Fatal("each role has its own password")
	}
	if len(p.Roles) != 3 || p.Roles[0].Database != "commerce_db" || p.Roles[2] != (RoleGrant{Role: "identity_auth", Database: "identity_db"}) {
		t.Fatalf("roles: %+v", p.Roles)
	}
	// The role password inside a DSN Secrets Manager holds is adopted, so
	// roles.sql keeps matching what the service connects with.
	in := applyInputs(nil)
	in.Current = map[string]map[string]string{"commerce-service": {"postgres_dsn": "postgres://commerce_service:ADOPTEDPASSWORD0001@aurora.internal:5432/commerce_db?sslmode=require"}}
	p2, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if p2.RolePasswords["commerce_service"] != "ADOPTEDPASSWORD0001" || keyOf(t, p2, "commerce-service", "postgres_dsn").Action != ActKeep {
		t.Fatal("current DSN password not adopted")
	}
	// Without the Aurora endpoint the DSN is pending.
	in = applyInputs(nil)
	in.TF = map[string]string{}
	p3, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if k := keyOf(t, p3, "commerce-service", "postgres_dsn"); k.Action != ActPending || k.Value != "" {
		t.Fatalf("expected pending, got %s", k.Action)
	}
}

// The never-overwrite guard: whatever Secrets Manager holds is kept unless
// --rotate names it, and --rotate replaces exactly what it names.
func TestNeverOverwriteUnlessRotated(t *testing.T) {
	m := mustParse(t, testManifest)
	in := applyInputs(nil)
	in.Current = map[string]map[string]string{
		"commerce-service": {
			"commerce_pii_lookup_salt": "EXISTING-SALT-VALUE-0001",
			"internal_service_key":     "OLD-INTERNAL-KEY-0001",
			"courier_provider":         "shiprocket",
			"shiprocket_email":         "ops@example.invalid",
		},
		"payments-service": {"postgres_dsn": "postgres://payments_service:OLDPASSWORD0001@old/commerce_db"},
	}
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ secret, key, want string }{
		{"commerce-service", "commerce_pii_lookup_salt", "EXISTING-SALT-VALUE-0001"},
		{"commerce-service", "internal_service_key", "OLD-INTERNAL-KEY-0001"},
		{"commerce-service", "courier_provider", "shiprocket"},
		{"commerce-service", "shiprocket_email", "ops@example.invalid"},
		{"payments-service", "postgres_dsn", "postgres://payments_service:OLDPASSWORD0001@old/commerce_db"},
	} {
		k := keyOf(t, p, c.secret, c.key)
		if k.Action != ActKeep || k.Value != c.want || k.Origin != OriginCurrent {
			t.Errorf("%s.%s: %s %s", c.secret, c.key, k.Action, k.Origin)
		}
	}
	// A kept copy that no longer matches its source is reported, not fixed.
	if !strings.Contains(strings.Join(p.Warnings, "\n"), "commerce-service.internal_service_key differs from its source") {
		t.Fatalf("drift not reported: %v", p.Warnings)
	}

	in.Rotate = map[string]bool{
		"commerce-service/commerce_pii_lookup_salt": true,
		"source/platform-auth":                      true,
		"role/payments_service":                     true,
		"commerce-service/courier_provider":         true,
	}
	p2, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	salt := keyOf(t, p2, "commerce-service", "commerce_pii_lookup_salt")
	if salt.Action != ActUpdate || salt.Value == "EXISTING-SALT-VALUE-0001" || len(salt.Value) != 64 {
		t.Errorf("rotated salt: %s", salt.Action)
	}
	if k := keyOf(t, p2, "commerce-service", "internal_service_key"); k.Action != ActUpdate || k.Value != testISK {
		t.Errorf("re-copied key: %s", k.Action)
	}
	if k := keyOf(t, p2, "commerce-service", "courier_provider"); k.Action != ActUpdate || k.Value != "stub" {
		t.Errorf("rotated const: %s %q", k.Action, k.Value)
	}
	if k := keyOf(t, p2, "commerce-service", "shiprocket_email"); k.Action != ActKeep {
		t.Errorf("unnamed key touched: %s", k.Action)
	}
	if k := keyOf(t, p2, "payments-service", "postgres_dsn"); k.Action != ActUpdate || strings.Contains(k.Value, "OLDPASSWORD0001") {
		t.Errorf("rotated dsn: %s", k.Action)
	}
	if !p2.RotatedRoles["payments_service"] || p2.RotatedRoles["commerce_service"] {
		t.Errorf("rotated roles: %v", p2.RotatedRoles)
	}
	for _, bad := range []string{"nope", "shared/none", "commerce-service/none", "none/x", "payments-service/caller_commerce_pubkey", "commerce-service/postgres_dsn", "shared/jwt_prev", "role/nobody", "source/none"} {
		in.Rotate = map[string]bool{bad: true}
		if _, err := Resolve(m, in); err == nil {
			t.Errorf("--rotate %s should be refused", bad)
		}
	}
}

// Idempotency without local values: the second apply finds the first one's
// unpushed payloads and reuses them; after the push, everything is kept.
func TestApplyIsIdempotentAndPlanIsReadOnly(t *testing.T) {
	m := mustParse(t, testManifest)
	dir := t.TempDir()
	fcm := filepath.Join(dir, "fcm.json")
	os.WriteFile(fcm, []byte(`{"type":"service_account"}`), 0o600)
	answers := map[string]string{"Razorpay key id": "rzp_live_abc0123456", "Path to the Firebase JSON": fcm}
	first, err := Resolve(m, applyInputs(answers))
	if err != nil {
		t.Fatal(err)
	}
	in := applyInputs(nil)
	in.Pending = payloadsOf(first)
	prompter := in.Prompter.(*MapPrompter)
	second, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	for i, s := range first.Secrets {
		for j, k := range s.Keys {
			k2 := second.Secrets[i].Keys[j]
			if k.Value != k2.Value {
				t.Errorf("%s.%s changed between runs", s.Name, k.Key)
			}
			if k.Action == ActCreate && k2.Action != ActReuse {
				t.Errorf("%s.%s: second run %s, want reuse", s.Name, k.Key, k2.Action)
			}
		}
	}
	for _, asked := range prompter.Asked {
		if asked == "Razorpay key id" || asked == "Path to the Firebase JSON" {
			t.Errorf("answered prompt asked again: %s", asked)
		}
	}
	if first.RolePasswords["commerce_service"] != second.RolePasswords["commerce_service"] {
		t.Fatal("role password changed between applies before the push")
	}

	// After the push: Secrets Manager holds the payloads; every key is kept.
	in = applyInputs(nil)
	in.Current = payloadsOf(first)
	in.Current["empty-shell"] = map[string]string{}
	third, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range third.Secrets {
		if s.Changed() {
			t.Errorf("%s would be pushed again", s.Name)
		}
	}

	// Plan mode never prompts and leaves prompts pending.
	planIn := Inputs{TF: testTF(), Sources: testSources(), Prompter: &MapPrompter{Answers: answers}}
	planOut, err := Resolve(m, planIn)
	if err != nil {
		t.Fatal(err)
	}
	if len(planIn.Prompter.(*MapPrompter).Asked) != 0 {
		t.Fatal("plan mode prompted")
	}
	if k := keyOf(t, planOut, "identity-auth-service", "fcm_service_account_key"); k.Action != ActPending {
		t.Fatalf("plan should leave prompts pending, got %s", k.Action)
	}
}

func TestPrompts(t *testing.T) {
	m := mustParse(t, testManifest)
	dir := t.TempDir()
	fcm := filepath.Join(dir, "fcm.json")
	os.WriteFile(fcm, []byte("{\"type\":\"service_account\"}\n"), 0o600)
	in := applyInputs(map[string]string{"Path to the Firebase JSON": fcm, "Shiprocket email": ""})
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if k := keyOf(t, p, "identity-auth-service", "fcm_service_account_key"); k.Value != "{\"type\":\"service_account\"}" || k.Origin != OriginPrompt {
		t.Fatalf("prompt-file: %s", k.Origin)
	}
	if k := keyOf(t, p, "commerce-service", "shiprocket_email"); k.Action != ActEmpty {
		t.Fatalf("optional empty: %s", k.Action)
	}
	if k := keyOf(t, p, "payments-service", "razorpay_key_id"); k.Action != ActPending {
		t.Fatalf("required empty: %s", k.Action)
	}
	if len(p.PendingPrompts) != 2 {
		t.Fatalf("pending prompts: %v", p.PendingPrompts)
	}
	in = applyInputs(map[string]string{"Path to the Firebase JSON": filepath.Join(dir, "missing.json")})
	if _, err := Resolve(m, in); err == nil {
		t.Fatal("missing prompt-file must fail")
	}
}

// The pairing guard: a receiver may never be left holding a public key that
// does not belong to the caller's private key.
func TestPairingGuard(t *testing.T) {
	m := mustParse(t, testManifest)
	pair, _ := Generator{}.Pair(GenEd25519)
	// The caller's private key is in Secrets Manager → the receiver gets the
	// matching public half.
	in := applyInputs(nil)
	in.Current = map[string]map[string]string{"commerce-service": {"commerce_service_token_key": pair.Private}}
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	if keyOf(t, p, "payments-service", "caller_commerce_pubkey").Value != pair.Public {
		t.Fatal("public half not derived from the current private half")
	}
	// Only the receiver's public half survives → a fresh caller key would not
	// match: apply refuses, plan reports it.
	in = applyInputs(nil)
	in.Current = map[string]map[string]string{"payments-service": {"caller_commerce_pubkey": pair.Public}}
	if _, err := Resolve(m, in); err == nil || !strings.Contains(err.Error(), "--rotate shared/svc_commerce") {
		t.Fatalf("apply must refuse a mismatched pair, got %v", err)
	}
	in.Apply, in.Prompter = false, nil
	p, err = Resolve(m, in)
	if err != nil || len(p.PairErrors) != 1 {
		t.Fatalf("plan must report the mismatch: %v %v", err, p.PairErrors)
	}
	// Two holders disagreeing (hand-edited secret) is caught too.
	other, _ := Generator{}.Pair(GenEd25519)
	in = applyInputs(nil)
	in.Current = map[string]map[string]string{
		"commerce-service": {"commerce_service_token_key": pair.Private},
		"payments-service": {"caller_commerce_pubkey": other.Public},
	}
	if _, err := Resolve(m, in); err == nil {
		t.Fatal("apply must refuse a receiver key from another pair")
	}
	// --rotate shared/svc_commerce re-keys both sides together.
	in.Rotate = map[string]bool{"shared/svc_commerce": true}
	p, err = Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	priv := keyOf(t, p, "commerce-service", "commerce_service_token_key")
	pub := keyOf(t, p, "payments-service", "caller_commerce_pubkey")
	if priv.Action != ActUpdate || pub.Action != ActUpdate || priv.Value == pair.Private {
		t.Fatalf("rotation: %s %s", priv.Action, pub.Action)
	}
	if rebuilt, _ := pairFromPrivate(GenEd25519, priv.Value); rebuilt.Public != pub.Value {
		t.Fatal("rotated halves do not pair")
	}
}

func TestEmptyShellSecretHasNoKeys(t *testing.T) {
	m := mustParse(t, testManifest)
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	last := p.Secrets[len(p.Secrets)-1]
	// Not fetched / no version yet: pushed once as {} so External Secrets
	// can sync it.
	if last.Name != "empty-shell" || len(last.Keys) != 0 || len(last.Payload()) != 0 || !last.Changed() {
		t.Fatalf("empty shell without a version: %+v", last)
	}
	in := applyInputs(nil)
	in.Current = map[string]map[string]string{"empty-shell": {}}
	in.Unversioned = map[string]bool{"empty-shell": true}
	if p, _ = Resolve(m, in); !p.Secrets[len(p.Secrets)-1].Changed() {
		t.Fatal("an unversioned shell must get a first version")
	}
	in.Unversioned = nil
	if p, _ = Resolve(m, in); p.Secrets[len(p.Secrets)-1].Changed() {
		t.Fatal("a shell already holding {} needs no push")
	}
}
