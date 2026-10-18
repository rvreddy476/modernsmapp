package scylla

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The integration suites in this package build their tables from the same
// CQL file the stack applies (scylla/schema.cql, mounted into scylla-init),
// then converge them with EnsureMessageColumns exactly as the service does at
// boot. A hand-written copy drifted once (reply_to_id); this can't.

// realSchemaPath is message-service/scylla/schema.cql relative to this
// package directory, which is the working directory of `go test`.
var realSchemaPath = filepath.Join("..", "..", "..", "scylla", "schema.cql")

// defaultIntegrationKeyspace is used when CHAT_SCYLLA_KEYSPACE is unset.
const defaultIntegrationKeyspace = "chatservice_m5_test"

// prodKeyspace is the keyspace name schema.cql is written against.
const prodKeyspace = "chatservice"

var (
	cqlLineComment = regexp.MustCompile(`(?m)--.*$`)
	prodKeyspaceRe = regexp.MustCompile(`\b` + prodKeyspace + `\b`)
	keyspaceNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)
)

// integrationKeyspace returns the keyspace an integration test may use. Like
// the Postgres suites refuse any database not ending in _test, it refuses any
// keyspace whose name does not end in _test, so a test can never truncate or
// reshape a dev or live keyspace.
func integrationKeyspace(getenv func(string) string) (string, error) {
	ks := strings.TrimSpace(getenv("CHAT_SCYLLA_KEYSPACE"))
	if ks == "" {
		ks = defaultIntegrationKeyspace
	}
	if !strings.HasSuffix(ks, "_test") {
		return "", fmt.Errorf("refusing Scylla keyspace %q: name must end in _test", ks)
	}
	if !keyspaceNameRe.MatchString(ks) {
		return "", fmt.Errorf("refusing Scylla keyspace %q: not a plain lower-case identifier", ks)
	}
	return ks, nil
}

// schemaStatementsFor turns schema.cql into executable statements against
// keyspace. Every statement must name the target keyspace explicitly, so
// nothing in the file can land in the session's default keyspace or in the
// production one.
func schemaStatementsFor(cql, keyspace string) ([]string, error) {
	if !strings.HasSuffix(keyspace, "_test") {
		return nil, fmt.Errorf("refusing Scylla keyspace %q: name must end in _test", keyspace)
	}
	body := cqlLineComment.ReplaceAllString(strings.ReplaceAll(cql, "\r", ""), "")
	var out []string
	for _, raw := range strings.Split(body, ";") {
		stmt := strings.TrimSpace(raw)
		if stmt == "" {
			continue
		}
		stmt = prodKeyspaceRe.ReplaceAllString(stmt, keyspace)
		if !strings.Contains(stmt, keyspace) {
			return nil, fmt.Errorf("schema statement does not name keyspace %s: %q", keyspace, stmt)
		}
		if prodKeyspaceRe.MatchString(stmt) {
			return nil, fmt.Errorf("schema statement still names %s: %q", prodKeyspace, stmt)
		}
		out = append(out, stmt)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("schema has no statements")
	}
	return out, nil
}

// loadRealSchema reads scylla/schema.cql and rewrites it for keyspace.
func loadRealSchema(keyspace string) ([]string, error) {
	raw, err := os.ReadFile(realSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", realSchemaPath, err)
	}
	return schemaStatementsFor(string(raw), keyspace)
}

func TestIntegrationKeyspaceRefusesNonTestNames(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == "CHAT_SCYLLA_KEYSPACE" {
				return v
			}
			return ""
		}
	}
	if ks, err := integrationKeyspace(env("")); err != nil || ks != defaultIntegrationKeyspace {
		t.Fatalf("default keyspace = %q, %v; want %q", ks, err, defaultIntegrationKeyspace)
	}
	if ks, err := integrationKeyspace(env("chat_parity_test")); err != nil || ks != "chat_parity_test" {
		t.Fatalf("explicit _test keyspace = %q, %v", ks, err)
	}
	for _, bad := range []string{"chatservice", "chatservice_m5integration", "chatservice_test_live", "Chat_test", "x; DROP_test"} {
		if ks, err := integrationKeyspace(env(bad)); err == nil {
			t.Errorf("keyspace %q accepted as %q; must be refused", bad, ks)
		}
	}
}

func TestSchemaStatementsTargetOnlyTheTestKeyspace(t *testing.T) {
	stmts, err := loadRealSchema("chatservice_m5_test")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for _, s := range stmts {
		if prodKeyspaceRe.MatchString(s) {
			t.Errorf("statement still targets %s: %q", prodKeyspace, s)
		}
		if m := regexp.MustCompile(`(?i)CREATE TABLE IF NOT EXISTS chatservice_m5_test\.(\w+)`).FindStringSubmatch(s); m != nil {
			tables = append(tables, m[1])
		}
	}
	if !strings.HasPrefix(stmts[0], "CREATE KEYSPACE IF NOT EXISTS chatservice_m5_test") {
		t.Fatalf("first statement = %q, want the test keyspace", stmts[0])
	}
	if got := strings.Join(tables, ","); got != "messages,conversations_by_user,message_reactions" {
		t.Fatalf("tables from schema.cql = %s", got)
	}
	// Every column the message queries name is in the real CREATE TABLE.
	for _, c := range lateMessageColumns {
		if !strings.Contains(stmts[1], c.name+" ") {
			t.Errorf("schema.cql messages table lacks %s", c.name)
		}
	}

	if _, err := schemaStatementsFor("CREATE TABLE IF NOT EXISTS messages (a int PRIMARY KEY);", "x_test"); err == nil {
		t.Error("an unqualified statement must be refused")
	}
	if _, err := schemaStatementsFor(stmts[0], "chatservice"); err == nil {
		t.Error("a non-_test keyspace must be refused")
	}
}
