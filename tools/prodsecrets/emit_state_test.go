package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestWriteOutputs(t *testing.T) {
	m := mustParse(t, testManifest)
	in := applyInputs(nil)
	// identity-auth already holds everything: nothing to push for it.
	in.Current = map[string]map[string]string{"empty-shell": {}}
	p, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	// A stale payload for a secret that now needs nothing is removed.
	os.MkdirAll(filepath.Join(dir, "secrets"), 0o700)
	os.WriteFile(filepath.Join(dir, "secrets", "empty-shell.json"), []byte("{}\n"), 0o600)
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	out, err := WriteOutputs(dir, m, p, "ap-south-1", now)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.SecretFiles) != 3 || len(out.Removed) != 1 {
		t.Fatalf("files: %v removed: %v", out.SecretFiles, out.Removed)
	}
	if _, err := os.Stat(filepath.Join(dir, "secrets", "empty-shell.json")); !os.IsNotExist(err) {
		t.Fatal("stale payload not removed")
	}
	b, _ := os.ReadFile(filepath.Join(dir, "secrets", "commerce-service.json"))
	var payload map[string]string
	if err := json.Unmarshal(b, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["commerce_service_token_key"] != keyOf(t, p, "commerce-service", "commerce_service_token_key").Value {
		t.Fatal("payload differs from plan")
	}
	if _, ok := payload["shiprocket_email"]; !ok {
		t.Fatal("empty keys must still be present for External Secrets")
	}
	if runtime.GOOS != "windows" {
		for _, f := range append(out.SecretFiles, out.RolesSQL) {
			if fi, _ := os.Stat(f); fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode %o", f, fi.Mode().Perm())
			}
		}
	}
	push, _ := os.ReadFile(out.PushScript)
	s := string(push)
	for _, want := range []string{
		"put atpost/test/commerce-service commerce-service",
		"put atpost/test/payments-service payments-service",
		"# atpost/test/empty-shell: nothing to push this run",
		`--secret-string "file://$file"`,
		`rm -f "$file"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("push script lacks %q", want)
		}
	}
	if strings.Contains(s, "$(cat") {
		t.Error("the value must never travel on the command line")
	}
	red := NewRedactor(p, in)
	if red.Redact(s) != s {
		t.Fatal("push script contains a secret value")
	}
	sql, _ := os.ReadFile(out.RolesSQL)
	q := string(sql)
	for _, want := range []string{
		"CREATE ROLE commerce_service LOGIN",
		"ALTER ROLE commerce_service WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD '" + p.RolePasswords["commerce_service"] + "'",
		"CREATE ROLE commerce_db_owner NOLOGIN",
		"ALTER DATABASE commerce_db OWNER TO commerce_db_owner",
		"REVOKE CONNECT ON DATABASE commerce_db FROM PUBLIC",
		"GRANT CONNECT ON DATABASE commerce_db TO payments_service",
		"GRANT commerce_db_owner TO payments_service",
		"ALTER ROLE payments_service IN DATABASE commerce_db SET role = 'commerce_db_owner'",
		"ALTER ROLE identity_auth IN DATABASE identity_db SET role = 'identity_db_owner'",
		"\\connect identity_db",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("roles.sql lacks %q", want)
		}
	}
	if strings.Count(q, "CREATE ROLE") != 3+2 {
		t.Errorf("expected 3 service roles + 2 owner groups, got %d CREATE ROLE", strings.Count(q, "CREATE ROLE"))
	}
	if len(out.PublicFiles) != 2 {
		t.Fatalf("public files: %v", out.PublicFiles)
	}
	pub, _ := os.ReadFile(filepath.Join(dir, "public", "svc_commerce.pub.b64"))
	if strings.TrimSpace(string(pub)) != p.PublicHalves["svc_commerce"].Public {
		t.Fatal("public half file differs")
	}
	if pem, _ := os.ReadFile(filepath.Join(dir, "public", "mini_rs256.pub.pem")); !strings.HasPrefix(string(pem), "-----BEGIN PUBLIC KEY-----") {
		t.Fatal("rsa public half must be PEM")
	}
	if red.Redact(string(pub)) != string(pub) {
		t.Fatal("public halves must not be treated as secrets")
	}
}

// state.json holds key ids, kinds and timestamps, never a value.
func TestStateRecordsIDsAndTimestampsOnly(t *testing.T) {
	m := mustParse(t, testManifest)
	p, err := Resolve(m, applyInputs(map[string]string{"Razorpay key id": "rzp_live_STATECHECK0001"}))
	if err != nil {
		t.Fatal(err)
	}
	st := NewState()
	t1 := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	st.Record(p, nil, t1)
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := SaveState(path, st); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(path)
	for _, s := range p.Secrets {
		for _, k := range s.Keys {
			for _, line := range strings.Split(k.Value, "\n") {
				line = strings.TrimSpace(line)
				if len(line) >= 6 && !strings.HasPrefix(line, "-----") && strings.Contains(string(raw), line) {
					t.Fatalf("state.json holds the value of %s.%s", s.Name, k.Key)
				}
			}
		}
	}
	for _, pw := range p.RolePasswords {
		if strings.Contains(string(raw), pw) {
			t.Fatal("state.json holds a role password")
		}
	}
	back, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	rec, ok := back.Keys["commerce-service/commerce_pii_lookup_salt"]
	if !ok || rec.Kind != "generate:hex64" || !rec.CreatedAt.Equal(t1) || rec.Rotations != 0 {
		t.Fatalf("record: %+v", rec)
	}
	if _, ok := back.Keys["role/commerce_service"]; !ok {
		t.Fatal("role not recorded")
	}
	if len(back.Runs) != 1 || back.Runs[0].Created == 0 {
		t.Fatalf("runs: %+v", back.Runs)
	}
	// A rotation bumps the counter and the timestamp, keeps created_at.
	in := applyInputs(nil)
	in.Current = payloadsOf(p)
	in.Rotate = map[string]bool{"commerce-service/commerce_pii_lookup_salt": true}
	p2, err := Resolve(m, in)
	if err != nil {
		t.Fatal(err)
	}
	t2 := t1.Add(time.Hour)
	back.Record(p2, []string{"commerce-service/commerce_pii_lookup_salt"}, t2)
	rec = back.Keys["commerce-service/commerce_pii_lookup_salt"]
	if rec.Rotations != 1 || !rec.UpdatedAt.Equal(t2) || !rec.CreatedAt.Equal(t1) {
		t.Fatalf("rotation record: %+v", rec)
	}
	// A state file from the old value-holding format is refused.
	os.WriteFile(path, []byte(`{"version":1,"shared":{"jwt_secret":{"value":"x"}}}`), 0o600)
	if _, err := LoadState(path); err == nil || !strings.Contains(err.Error(), "delete it") {
		t.Fatalf("v1 state must be refused, got %v", err)
	}
}

func TestLoadInputs(t *testing.T) {
	dir := t.TempDir()
	tfPath := filepath.Join(dir, "tf.json")
	os.WriteFile(tfPath, []byte(`{"a":{"value":"x","sensitive":true,"type":"string"},"b":"y","n":{"value":5432,"sensitive":false},"l":{"value":["h1","h2"]},"flat_list":["p","q"]}`), 0o600)
	tf, err := LoadTFOutputs(tfPath)
	if err != nil {
		t.Fatal(err)
	}
	for k, want := range map[string]string{"a": "x", "b": "y", "n": "5432", "l": "h1,h2", "flat_list": "p,q"} {
		if tf[k] != want {
			t.Errorf("%s = %q, want %q", k, tf[k], want)
		}
	}
	if m, err := LoadTFOutputs(filepath.Join(dir, "none.json")); err != nil || len(m) != 0 {
		t.Fatal("missing outputs file must be empty, not an error")
	}
	cur := filepath.Join(dir, "current")
	if _, fetched, err := LoadValueDir(cur); fetched || err != nil {
		t.Fatal("a missing directory is 'not fetched'")
	}
	os.MkdirAll(cur, 0o700)
	os.WriteFile(filepath.Join(cur, "svc.json"), []byte(`{"k":"v","n":1}`), 0o600)
	os.WriteFile(filepath.Join(cur, "shell.json"), nil, 0o600)
	os.WriteFile(filepath.Join(cur, "obj.json"), []byte("{}\n"), 0o600)
	os.WriteFile(filepath.Join(cur, "notes.txt"), []byte("ignored"), 0o600)
	c, fetched, err := LoadValueDir(cur)
	if err != nil || !fetched {
		t.Fatal(err)
	}
	if c["svc"]["k"] != "v" || c["svc"]["n"] != "1" || len(c["shell"]) != 0 || len(c["obj"]) != 0 || len(c) != 3 {
		t.Fatalf("current: %+v", c)
	}
	un := UnversionedIn(cur)
	if !un["shell"] || un["obj"] || un["svc"] || len(un) != 1 {
		t.Fatalf("unversioned: %v", un)
	}
	// A broken file is an error that never quotes the content.
	os.WriteFile(filepath.Join(cur, "bad.json"), []byte("SUPERSECRETNOTJSON"), 0o600)
	if _, _, err := LoadValueDir(cur); err == nil || strings.Contains(err.Error(), "SUPERSECRET") {
		t.Fatalf("bad file error: %v", err)
	}
}
