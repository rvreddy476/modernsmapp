package main

import (
	"os"
	"regexp"
	"strings"
	"testing"
	"time"
)

// chat_call is read-write on its own call_db and read-only on chat_db: one
// LOGIN role, two modes in two databases.
const roManifest = `
version: 1
prefix: atpost/test
secrets:
  chat-message-service:
    postgres_dsn: dsn:chat_db/chat_message
  chat-ws-gateway:
    chat_postgres_dsn: dsn-ro:chat_db/chat_ws_gateway
  chat-call-service:
    postgres_dsn: dsn:call_db/chat_call
    chat_postgres_dsn: dsn-ro:chat_db/chat_call
`

func entryOf(t *testing.T, m *Manifest, secret, key string) Entry {
	t.Helper()
	for _, s := range m.Secrets {
		if s.Name != secret {
			continue
		}
		for _, k := range s.Keys {
			if k.Name == key {
				return k
			}
		}
	}
	t.Fatalf("manifest has no %s.%s", secret, key)
	return Entry{}
}

func roRolesSQL(t *testing.T, src string) (*Plan, string) {
	t.Helper()
	m := mustParse(t, src)
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	return p, rolesSQL(p, time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC))
}

// ownerGrantRe matches a membership grant: GRANT <db>_owner TO <role>;
var ownerGrantRe = regexp.MustCompile(`(?m)^GRANT ([a-z0-9_]+_owner) TO ([a-z0-9_]+);$`)

// ownerMembers is every (owner group, role) membership roles.sql grants.
func ownerMembers(sql string) map[[2]string]bool {
	out := map[[2]string]bool{}
	for _, m := range ownerGrantRe.FindAllStringSubmatch(sql, -1) {
		out[[2]string{m[1], m[2]}] = true
	}
	return out
}

// dbSection is the part of roles.sql run while connected to db.
func dbSection(t *testing.T, sql, db string) string {
	t.Helper()
	_, after, ok := strings.Cut(sql, "\\connect "+db+"\n")
	if !ok {
		t.Fatalf("roles.sql never connects to %s", db)
	}
	section, _, _ := strings.Cut(after, "\\connect postgres\n")
	return section
}

func TestReadOnlyDSNParsesAndDerivesLikeDSN(t *testing.T) {
	m := mustParse(t, roManifest)
	e := entryOf(t, m, "chat-ws-gateway", "chat_postgres_dsn")
	if e.Spec.Kind != KindDSNReadOnly || !e.Spec.IsDSN() || e.Spec.Category() != "derive" {
		t.Fatalf("dsn-ro spec: %+v %s", e.Spec, e.Spec.Category())
	}
	p, err := Resolve(m, applyInputs(nil))
	if err != nil {
		t.Fatal(err)
	}
	v := keyOf(t, p, "chat-ws-gateway", "chat_postgres_dsn").Value
	if !strings.HasPrefix(v, "postgres://chat_ws_gateway:"+p.RolePasswords["chat_ws_gateway"]+"@aurora.internal:5432/chat_db?sslmode=require") {
		t.Fatal("dsn-ro must build the same DSN shape as dsn (value not shown)")
	}
	// One password per LOGIN role, whatever its mode in each database.
	if keyOf(t, p, "chat-call-service", "postgres_dsn").Value == "" ||
		!strings.Contains(keyOf(t, p, "chat-call-service", "chat_postgres_dsn").Value, "chat_call:"+p.RolePasswords["chat_call"]+"@") {
		t.Fatal("chat_call must use one password on both databases")
	}
	want := []RoleGrant{
		{Role: "chat_call", Database: "call_db"},
		{Role: "chat_call", Database: "chat_db", ReadOnly: true},
		{Role: "chat_message", Database: "chat_db"},
		{Role: "chat_ws_gateway", Database: "chat_db", ReadOnly: true},
	}
	if len(p.Roles) != len(want) {
		t.Fatalf("roles: %+v", p.Roles)
	}
	for i := range want {
		if p.Roles[i] != want[i] {
			t.Errorf("role %d: %+v, want %+v", i, p.Roles[i], want[i])
		}
	}
	// --rotate role/<name> reaches a role that only dsn-ro uses.
	in := applyInputs(nil)
	in.Rotate = map[string]bool{"role/chat_ws_gateway": true}
	if _, err := Resolve(m, in); err != nil {
		t.Fatalf("rotate a read-only role: %v", err)
	}
}

func TestReadOnlyLoginRolesSQL(t *testing.T) {
	_, sql := roRolesSQL(t, roManifest)

	for _, want := range []string{
		"-- database chat_db: owner group chat_db_owner; members chat_message; read-only chat_call, chat_ws_gateway\n",
		// The read-write member is unchanged.
		"GRANT chat_db_owner TO chat_message;\n",
		"ALTER ROLE chat_message IN DATABASE chat_db SET role = 'chat_db_owner';\n",
		// chat_call stays read-write in its own database.
		"GRANT call_db_owner TO chat_call;\n",
		"ALTER ROLE chat_call IN DATABASE call_db SET role = 'call_db_owner';\n",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("roles.sql lacks %q", want)
		}
	}
	section := dbSection(t, sql, "chat_db")
	for _, role := range []string{"chat_ws_gateway", "chat_call"} {
		for _, want := range []string{
			"GRANT CONNECT ON DATABASE chat_db TO " + role + ";\n",
			// Converges a login an earlier roles.sql made a member.
			"REVOKE chat_db_owner FROM " + role + ";\n",
			"ALTER ROLE " + role + " IN DATABASE chat_db RESET role;\n",
			"ALTER ROLE " + role + " IN DATABASE chat_db SET default_transaction_read_only = on;\n",
		} {
			if !strings.Contains(sql, want) {
				t.Errorf("roles.sql lacks %q", want)
			}
		}
		// Inside chat_db: USAGE + SELECT on every user schema (chat lives
		// in schema chat, not public) and default privileges for the
		// schemas and tables the owner group creates later.
		for _, want := range []string{
			"DO $$ DECLARE s name; BEGIN FOR s IN SELECT nspname FROM pg_namespace WHERE nspname NOT LIKE 'pg\\_%' AND nspname <> 'information_schema' LOOP " +
				"EXECUTE format('GRANT USAGE ON SCHEMA %I TO " + role + "', s); " +
				"EXECUTE format('GRANT SELECT ON ALL TABLES IN SCHEMA %I TO " + role + "', s); END LOOP; END $$;\n",
			"REVOKE CREATE ON SCHEMA public FROM PUBLIC;\n",
			"ALTER DEFAULT PRIVILEGES FOR ROLE chat_db_owner GRANT USAGE ON SCHEMAS TO " + role + ";\n",
			"ALTER DEFAULT PRIVILEGES FOR ROLE chat_db_owner GRANT SELECT ON TABLES TO " + role + ";\n",
		} {
			if !strings.Contains(section, want) {
				t.Errorf("chat_db section lacks %q", want)
			}
		}
		// Never a member, never assuming the owner, never a write grant.
		for _, bad := range []string{
			"GRANT chat_db_owner TO " + role + ";",
			"ALTER ROLE " + role + " IN DATABASE chat_db SET role",
		} {
			if strings.Contains(sql, bad) {
				t.Errorf("read-only %s: roles.sql contains %q", role, bad)
			}
		}
		// Every privilege granted to the reader is SELECT, USAGE or CONNECT.
		grantRe := regexp.MustCompile(`GRANT ([A-Z, ]+?) ON [^;']*? TO ` + role + `\b`)
		for _, g := range grantRe.FindAllStringSubmatch(sql, -1) {
			for _, priv := range strings.Split(g[1], ",") {
				switch strings.TrimSpace(priv) {
				case "SELECT", "USAGE", "CONNECT":
				default:
					t.Errorf("read-only %s is granted %s: %s", role, priv, g[0])
				}
			}
		}
	}
	if m := ownerMembers(sql); m[[2]string{"chat_db_owner", "chat_call"}] || m[[2]string{"chat_db_owner", "chat_ws_gateway"}] || !m[[2]string{"chat_db_owner", "chat_message"}] || !m[[2]string{"call_db_owner", "chat_call"}] {
		t.Errorf("owner memberships: %v", m)
	}
	// One LOGIN role each, however many databases it is in.
	if n := strings.Count(sql, "CREATE ROLE chat_call LOGIN"); n != 1 {
		t.Errorf("chat_call created %d times", n)
	}
}

// A database whose only logins are readers still gets its owner group, and
// no LOGIN is put in it.
func TestReadOnlyOnlyDatabase(t *testing.T) {
	_, sql := roRolesSQL(t, "version: 1\nprefix: p\nsecrets:\n  s:\n    k: dsn-ro:identity_db/graph_service\n")
	if !strings.Contains(sql, "-- database identity_db: owner group identity_db_owner; members (none); read-only graph_service\n") ||
		!strings.Contains(sql, "CREATE ROLE identity_db_owner NOLOGIN") {
		t.Fatalf("roles.sql:\n%s", strings.ReplaceAll(sql, "PASSWORD", "PASSWORD(redacted)"))
	}
	if len(ownerMembers(sql)) != 0 {
		t.Fatalf("a reader joined an owner group: %v", ownerMembers(sql))
	}
}

func TestReadOnlyDSNRejects(t *testing.T) {
	for name, src := range map[string]string{
		"bad dsn-ro":       "version: 1\nprefix: p\nsecrets:\n  s:\n    k: dsn-ro:chat_db\n",
		"dsn-ro in shared": "version: 1\nprefix: p\nshared:\n  a: dsn-ro:app/r\n",
		// One login cannot be inside and outside the owner group at once.
		"dsn and dsn-ro on one database": "version: 1\nprefix: p\nsecrets:\n  a:\n    k: dsn:chat_db/r\n  b:\n    k: dsn-ro:chat_db/r\n",
		"dsn-ro then dsn":                "version: 1\nprefix: p\nsecrets:\n  a:\n    k: dsn-ro:chat_db/r\n    j: dsn:chat_db/r\n",
	} {
		if _, err := ParseManifest(strings.NewReader(src)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// The committed manifest declares exactly these logins read-only (each one's
// code was checked to only SELECT), and for every environment no read-only
// login ever gets owner membership in its database.
func TestCommittedManifestReadOnlyLogins(t *testing.T) {
	wantRO := map[string]bool{
		"graph_service@identity_db": true, // legacy profile.blocks reconciler
		"chat_ws_gateway@chat_db":   true, // presence fan-out DM peers
		"chat_call@chat_db":         true, // call mute + hidden chats
	}
	forEachEnv(t, func(t *testing.T, env string) {
		m := loadCommitted(t, env)
		p, err := Resolve(m, Inputs{TF: map[string]string{"aurora_cluster_endpoint": "aurora.example.internal"}, Prompter: &MapPrompter{}})
		if err != nil {
			t.Fatal(err)
		}
		gotRO := map[string]bool{}
		for _, g := range p.Roles {
			if g.ReadOnly {
				gotRO[g.Role+"@"+g.Database] = true
			}
		}
		for id := range wantRO {
			if !gotRO[id] {
				t.Errorf("%s must be dsn-ro", id)
			}
		}
		for id := range gotRO {
			if !wantRO[id] {
				t.Errorf("%s is dsn-ro but this test does not know it; check its code only reads, then add it here", id)
			}
		}
		sql := rolesSQL(&Plan{Roles: p.Roles}, time.Now())
		members := ownerMembers(sql)
		for _, g := range p.Roles {
			member := members[[2]string{g.Database + "_owner", g.Role}]
			if g.ReadOnly && member {
				t.Errorf("read-only %s@%s is granted %s_owner", g.Role, g.Database, g.Database)
			}
			if !g.ReadOnly && !member {
				t.Errorf("read-write %s@%s lost %s_owner", g.Role, g.Database, g.Database)
			}
		}
	})
}

// CHAT_KMS_KEY_ID comes from Terraform, and the key Terraform creates admits
// exactly the encryption context message-service sets, in every environment.
func TestChatFieldKMSMatchesMessageService(t *testing.T) {
	read := func(parts ...string) string {
		t.Helper()
		b, err := os.ReadFile(repoPath(t, parts...))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	module := read("infra", "terraform", "modules", "chat-field-kms", "main.tf")
	fieldkeys := read("chat-service", "services", "message-service", "internal", "fieldkeys", "fieldkeys.go")
	kmsclient := read("chat-service", "services", "message-service", "internal", "kmsclient", "kmsclient.go")

	constOf := func(name string) string {
		m := regexp.MustCompile(name + `\s*=\s*"([^"]+)"`).FindStringSubmatch(fieldkeys)
		if m == nil {
			t.Fatalf("fieldkeys.go: no %s", name)
		}
		return m[1]
	}
	if !regexp.MustCompile(`(?m)^\s*purpose\s*=\s*"` + regexp.QuoteMeta(constOf("KMSPurpose")) + `"$`).MatchString(module) {
		t.Errorf("chat-field-kms does not pin purpose=%s", constOf("KMSPurpose"))
	}
	if !regexp.MustCompile(`(?m)^\s*scope\s*=\s*"` + regexp.QuoteMeta(constOf("KMSRingScope")) + `"$`).MatchString(module) {
		t.Errorf("chat-field-kms does not pin scope=%s", constOf("KMSRingScope"))
	}
	// The key grants what kmsclient calls, nothing more. A new KMS call in
	// the adapter must be granted in Terraform in the same change.
	api := regexp.MustCompile(`(?s)type api interface \{(.*?)\n\}`).FindStringSubmatch(kmsclient)
	if api == nil {
		t.Fatal("kmsclient.go: no api interface")
	}
	calls := regexp.MustCompile(`(?m)^\s*([A-Z][A-Za-z]+)\(`).FindAllStringSubmatch(api[1], -1)
	if len(calls) != 2 || calls[0][1] != "GenerateDataKey" || calls[1][1] != "Decrypt" {
		t.Errorf("kmsclient calls %v; update modules/chat-field-kms grants and this test together", calls)
	}
	allowRe := regexp.MustCompile(`(?s)effect\s*=\s*"Allow"\s*actions\s*=\s*\[(.*?)\]`)
	for _, grant := range allowRe.FindAllStringSubmatch(module, -1) {
		if strings.Contains(grant[1], "kms:*") {
			continue // the account-root administration statement
		}
		got := strings.Join(strings.Fields(grant[1]), " ")
		if got != `"kms:GenerateDataKey", "kms:Decrypt",` {
			t.Errorf("chat-field-kms grants %s", got)
		}
	}
	if !strings.Contains(module, "enable_key_rotation     = true") {
		t.Error("chat-field-kms: rotation must be enabled")
	}

	forEachEnv(t, func(t *testing.T, env string) {
		m := loadCommitted(t, env)
		if e := entryOf(t, m, "chat-message-service", "chat_kms_key_id"); e.Spec.String() != "tf:chat_field_kms_key_id" {
			t.Errorf("chat-message-service.chat_kms_key_id must be tf:chat_field_kms_key_id, got %v", e.Spec)
		}
		tf := terraformText(t, envTerraformDir(t, env))
		if !strings.Contains(tf, `policy_arn = module.chat_field_kms.client_policy_arn`) ||
			!regexp.MustCompile(`role\s*=\s*module\.service_irsa\["chat-message-service"\]\.role_name\s*\n\s*policy_arn = module\.chat_field_kms\.client_policy_arn`).MatchString(tf) {
			t.Error("the chat field key's client policy must be attached to chat-message-service's IRSA role")
		}
		if !strings.Contains(tf, `principal_role_arns            = [module.service_irsa["chat-message-service"].role_arn]`) {
			t.Error("the chat field key policy must name chat-message-service's IRSA role")
		}
		// The pinned environment is message-service's canonical APP_ENV.
		values := read("deploy", "services", "chat-message-service", "values-"+env+".yaml")
		appEnv := regexp.MustCompile(`(?m)^\s*APP_ENV:\s*"?([A-Za-z]+)"?\s*$`).FindStringSubmatch(values)
		if appEnv == nil {
			t.Fatalf("values-%s.yaml: no APP_ENV", env)
		}
		want := strings.ToLower(appEnv[1])
		if want == "prod" {
			want = "production"
		}
		if !strings.Contains(tf, `encryption_context_environment = "`+want+`"`) {
			t.Errorf("envs/%s must pin encryption_context_environment = %q (APP_ENV=%s); any other value fails every boot", env, want, appEnv[1])
		}
	})
}
