package scylla

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// Every write in this file must address rows by primary key only.
//
// THE DEFECT THIS PINS (2026-09-09). DeleteHomeTimelineEntriesByAuthorForUser
// deleted with
//
//	DELETE FROM home_timeline_by_user
//	WHERE user_id = ? AND bucket = ? AND ts = ? AND post_id = ?
//
// while the table is keyed ((user_id, bucket), ts) — post_id is a regular
// column. Scylla rejects a DELETE that matches on a non-key column:
//
//	Cannot execute this query as it might involve data filtering and thus
//	may have unpredictable performance … use ALLOW FILTERING
//
// so the statement threw on every call and the unfollow purge deleted
// nothing for as long as it existed. Nothing failed at build or test time;
// the only evidence was a line in the consumer log, and the visible symptom
// was unfollowed authors staying in the feed — which reads as a feed-ranking
// complaint, not a broken DELETE.
//
// A unit test of the purge cannot catch this without a live Scylla (the
// store holds a concrete *gocql.Session), so this asserts it at the source
// level instead, the same way internal/service/coldstart_narrowing_test.go
// and reason_following_test.go pin their decisions. Re-add `AND post_id = ?`
// to that DELETE and this test fails.

// primaryKeys is the live schema, verified against the running cluster on
// 2026-09-09 (`docker exec atpost_stack-scylla-1 cqlsh -e "DESCRIBE KEYSPACE
// social_feed"`) and against Architecture/docker/scylla/schema.cql, which
// TestPinnedPrimaryKeysMatchTheCheckedInSchema re-checks on every run:
//
//	home_timeline_by_user      PRIMARY KEY ((user_id, bucket), ts)
//	author_timeline_by_author  PRIMARY KEY ((author_id, bucket), ts)
//	timeline_index_by_post     PRIMARY KEY ((post_id), timeline_kind, owner_id, bucket, ts)
//	user_post_interactions     PRIMARY KEY (user_id, post_id)
//
// Note what is NOT a key column: post_id and author_id on
// home_timeline_by_user, post_id on author_timeline_by_author. Those are
// exactly the columns a "delete this post for this user" statement reaches
// for by instinct.
var primaryKeys = map[string]map[string]bool{
	"home_timeline_by_user":     {"user_id": true, "bucket": true, "ts": true},
	"author_timeline_by_author": {"author_id": true, "bucket": true, "ts": true},
	"timeline_index_by_post":    {"post_id": true, "timeline_kind": true, "owner_id": true, "bucket": true, "ts": true},
	"user_post_interactions":    {"user_id": true, "post_id": true},
}

// minStatements guards against the test quietly checking nothing after a
// refactor moves the CQL somewhere this parser cannot see it.
const minStatements = 12

type cqlStatement struct {
	verb  string // SELECT / INSERT / UPDATE / DELETE
	table string
	where []string // lower-cased column names matched on in the WHERE clause
	raw   string
}

func TestHomeTimelineStatementsOnlyMatchOnPrimaryKey(t *testing.T) {
	stmts := parseStatements(t, "timelines.go")
	if len(stmts) < minStatements {
		t.Fatalf("found only %d CQL statements in timelines.go, expected at least %d — "+
			"has the CQL moved out of string literals? This test would then pass while "+
			"checking nothing", len(stmts), minStatements)
	}

	checked := 0
	for _, s := range stmts {
		keys, known := primaryKeys[s.table]
		if !known {
			continue // a table this test does not carry the key for
		}
		// A SELECT may legitimately filter on a regular column when it says
		// ALLOW FILTERING out loud — UpdatePostContentType's pre-HF4
		// fallback does exactly that, deliberately and with a comment. A
		// write may never: there is no ALLOW FILTERING for DELETE/UPDATE.
		if s.verb == "SELECT" && strings.Contains(strings.ToUpper(s.raw), "ALLOW FILTERING") {
			continue
		}
		checked++
		for _, col := range s.where {
			if !keys[col] {
				t.Errorf("%s on %s matches on %q, which is not part of that table's "+
					"primary key (%s). Scylla rejects this at execution time with "+
					"\"might involve data filtering … use ALLOW FILTERING\", so the "+
					"statement fails every time it runs and the caller silently does "+
					"nothing.\n  statement: %s",
					s.verb, s.table, col, keyList(keys), s.raw)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no statements were actually checked — the parser or the table names drifted")
	}
}

// The unfollow purge specifically: it is the one place that walks a
// partition, filters client-side and then writes, and the one that was
// broken. Pinned by name so a rename has to come here and think.
func TestUnfollowPurgeDeletesByClusteringKeyOnly(t *testing.T) {
	stmts := parseStatementsInFunc(t, "timelines.go", "DeleteHomeTimelineEntriesByAuthorForUser")
	if len(stmts) == 0 {
		t.Fatal("DeleteHomeTimelineEntriesByAuthorForUser not found, or it no longer " +
			"contains CQL — has it been renamed?")
	}

	var deletes int
	for _, s := range stmts {
		if s.verb != "DELETE" || s.table != "home_timeline_by_user" {
			continue
		}
		deletes++
		got := strings.Join(s.where, ",")
		const want = "user_id,bucket,ts"
		if got != want {
			t.Fatalf("the unfollow purge deletes on (%s); the table is keyed ((user_id, "+
				"bucket), ts) so it must delete on exactly (%s). Adding post_id makes "+
				"the statement a filtering query and Scylla refuses it — that is the "+
				"2026-09-09 defect. Removing ts would delete the whole partition, "+
				"i.e. every author's rows.\n  statement: %s", got, want, s.raw)
		}
	}
	if deletes != 1 {
		t.Fatalf("expected exactly one DELETE from home_timeline_by_user in the purge, found %d", deletes)
	}
}

// The purge must also retire the HF4 reverse-index rows for what it deleted.
// UpdatePostContentType drives off that index and issues an UPDATE, and an
// UPDATE in Scylla creates the row when it is missing — so a left-behind
// index row resurrects a purged timeline entry as a ghost (content_type set,
// post_id/author_id/created_at null) on the next reclassification.
func TestUnfollowPurgeAlsoRetiresTheReverseIndex(t *testing.T) {
	stmts := parseStatementsInFunc(t, "timelines.go", "DeleteHomeTimelineEntriesByAuthorForUser")
	for _, s := range stmts {
		if s.verb == "DELETE" && s.table == "timeline_index_by_post" {
			return
		}
	}
	t.Fatal("the unfollow purge deletes home_timeline_by_user rows but leaves their " +
		"timeline_index_by_post entries behind; a later PostContentTypeChanged will " +
		"resurrect them as ghost rows via UPDATE-creates-row")
}

// The pinned keys above are a copy of the schema, and a copy can drift. This
// re-derives them from the checked-in DDL. Skips (rather than fails) when the
// file is out of reach, so the service still tests standalone.
func TestPinnedPrimaryKeysMatchTheCheckedInSchema(t *testing.T) {
	path := filepath.Join("..", "..", "..", "..", "..", "docker", "scylla", "schema.cql")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("checked-in schema not reachable from here (%v); the pinned keys stand", err)
	}
	fromDDL := parseSchemaKeys(string(raw))
	for table, want := range primaryKeys {
		got, ok := fromDDL[table]
		if !ok {
			t.Errorf("table %s is not in %s any more — pinned keys may be stale", table, path)
			continue
		}
		if keyList(got) != keyList(want) {
			t.Errorf("%s: schema.cql says the primary key is (%s), this test pins (%s). "+
				"The schema moved; every statement checked against the pinned set is "+
				"now checked against the wrong thing.", table, keyList(got), keyList(want))
		}
	}
}

func keyList(keys map[string]bool) string {
	out := make([]string, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	// Sorted for a stable message.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return strings.Join(out, ", ")
}

// parseStatements pulls every CQL string literal out of a Go source file.
func parseStatements(t *testing.T, filename string) []cqlStatement {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	return collect(t, file)
}

// parseStatementsInFunc narrows that to one function body.
func parseStatementsInFunc(t *testing.T, filename, funcName string) []cqlStatement {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != funcName || fn.Body == nil {
			continue
		}
		return collect(t, fn.Body)
	}
	return nil
}

func collect(t *testing.T, root ast.Node) []cqlStatement {
	t.Helper()
	var out []cqlStatement
	ast.Inspect(root, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		val, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		if s, ok := parseCQL(val); ok {
			out = append(out, s)
		}
		return true
	})
	return out
}

// parseCQL recognises the shapes this package writes. Not a CQL parser: it
// needs the verb, the table and the columns the WHERE clause matches on.
func parseCQL(raw string) (cqlStatement, bool) {
	norm := strings.Join(strings.Fields(raw), " ")
	upper := strings.ToUpper(norm)

	var s cqlStatement
	s.raw = norm

	switch {
	case strings.HasPrefix(upper, "SELECT "):
		s.verb = "SELECT"
		i := strings.Index(upper, " FROM ")
		if i < 0 {
			return s, false
		}
		s.table = word(norm[i+len(" FROM "):])
	case strings.HasPrefix(upper, "INSERT INTO "):
		s.verb = "INSERT"
		s.table = word(norm[len("INSERT INTO "):])
	case strings.HasPrefix(upper, "UPDATE "):
		s.verb = "UPDATE"
		s.table = word(norm[len("UPDATE "):])
	case strings.HasPrefix(upper, "DELETE FROM "):
		s.verb = "DELETE"
		s.table = word(norm[len("DELETE FROM "):])
	default:
		return s, false
	}
	s.table = strings.TrimPrefix(s.table, "social_feed.")

	if i := strings.Index(upper, " WHERE "); i >= 0 {
		clause := norm[i+len(" WHERE "):]
		// Trim anything that follows the predicate.
		for _, tail := range []string{" ORDER BY ", " LIMIT ", " ALLOW FILTERING", " IF "} {
			if j := strings.Index(strings.ToUpper(clause), tail); j >= 0 {
				clause = clause[:j]
			}
		}
		for _, part := range splitAND(clause) {
			col := strings.ToLower(word(strings.TrimSpace(part)))
			if col != "" {
				s.where = append(s.where, col)
			}
		}
	}
	return s, true
}

func splitAND(clause string) []string {
	var out []string
	upper := strings.ToUpper(clause)
	start := 0
	for {
		i := strings.Index(upper[start:], " AND ")
		if i < 0 {
			out = append(out, clause[start:])
			return out
		}
		out = append(out, clause[start:start+i])
		start += i + len(" AND ")
	}
}

// word returns the first whitespace- or punctuation-delimited token.
func word(s string) string {
	s = strings.TrimSpace(s)
	for i, r := range s {
		switch r {
		case ' ', '\t', '\n', '(', ')', ',', '=', '<', '>':
			return s[:i]
		}
	}
	return s
}

// parseSchemaKeys reads PRIMARY KEY declarations out of the DDL. Handles both
// `PRIMARY KEY ((a, b), c)` and `PRIMARY KEY (a, b)`.
func parseSchemaKeys(ddl string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	blocks := strings.Split(ddl, "CREATE TABLE")
	for _, block := range blocks[1:] {
		open := strings.Index(block, "(")
		if open < 0 {
			continue
		}
		name := word(strings.TrimPrefix(strings.TrimSpace(block[:open]), "IF NOT EXISTS "))
		name = strings.TrimPrefix(strings.TrimSpace(name), "social_feed.")
		i := strings.Index(strings.ToUpper(block), "PRIMARY KEY")
		if i < 0 {
			continue
		}
		rest := block[i+len("PRIMARY KEY"):]
		start := strings.Index(rest, "(")
		if start < 0 {
			continue
		}
		depth := 0
		end := -1
		for j := start; j < len(rest); j++ {
			if rest[j] == '(' {
				depth++
			} else if rest[j] == ')' {
				depth--
				if depth == 0 {
					end = j
					break
				}
			}
		}
		if end < 0 {
			continue
		}
		inner := strings.NewReplacer("(", " ", ")", " ").Replace(rest[start:end])
		keys := map[string]bool{}
		for _, col := range strings.Split(inner, ",") {
			col = strings.ToLower(strings.TrimSpace(col))
			if col != "" {
				keys[col] = true
			}
		}
		out[name] = keys
	}
	return out
}
