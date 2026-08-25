package schemabootstrap

import (
	"strings"
	"testing"
)

// The splitter had no tests despite having already caused an outage: a
// semicolon inside a `--` comment cut a statement in half, PostgreSQL answered
// 42601, and auth-service could not start against a fresh database. Every case
// below is a place a semicolon is NOT a statement terminator.

func TestSplitSQLStatements(t *testing.T) {
	cases := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "plain statements",
			sql:  "CREATE TABLE a (id INT); CREATE TABLE b (id INT);",
			want: []string{"CREATE TABLE a (id INT)", "CREATE TABLE b (id INT)"},
		},
		{
			// The regression. `strings.Split(sql, ";")` cut here.
			name: "semicolon inside a line comment",
			sql: "CREATE TABLE a (\n" +
				"  -- verified at login; the handle is unique\n" +
				"  id INT\n" +
				");",
			want: []string{"CREATE TABLE a (\n  -- verified at login; the handle is unique\n  id INT\n)"},
		},
		{
			name: "semicolon inside a block comment",
			sql:  "/* one; two; three */ CREATE TABLE a (id INT);",
			want: []string{"/* one; two; three */ CREATE TABLE a (id INT)"},
		},
		{
			name: "nested block comments",
			sql:  "/* outer /* inner; */ still outer; */ SELECT 1;",
			want: []string{"/* outer /* inner; */ still outer; */ SELECT 1"},
		},
		{
			name: "semicolon inside a string literal",
			sql:  "INSERT INTO a VALUES ('x;y');",
			want: []string{"INSERT INTO a VALUES ('x;y')"},
		},
		{
			name: "escaped quote inside a string literal",
			sql:  "INSERT INTO a VALUES ('it''s; fine');",
			want: []string{"INSERT INTO a VALUES ('it''s; fine')"},
		},
		{
			name: "semicolon inside a quoted identifier",
			sql:  `CREATE TABLE "odd;name" (id INT);`,
			want: []string{`CREATE TABLE "odd;name" (id INT)`},
		},
		{
			// Function bodies are the reason dollar quoting is handled at all.
			name: "dollar-quoted body",
			sql: "CREATE FUNCTION f() RETURNS INT AS $$\n" +
				"BEGIN\n  RETURN 1;\nEND;\n$$ LANGUAGE plpgsql;\n" +
				"SELECT 2;",
			want: []string{
				"CREATE FUNCTION f() RETURNS INT AS $$\nBEGIN\n  RETURN 1;\nEND;\n$$ LANGUAGE plpgsql",
				"SELECT 2",
			},
		},
		{
			name: "tagged dollar quoting",
			sql:  "DO $body$ BEGIN PERFORM 1; END $body$;",
			want: []string{"DO $body$ BEGIN PERFORM 1; END $body$"},
		},
		{
			// A trailing comment block must not be sent as an empty statement.
			name: "trailing comment produces no statement",
			sql:  "SELECT 1;\n-- nothing executable follows\n",
			want: []string{"SELECT 1"},
		},
		{
			name: "consecutive semicolons produce no empty statements",
			sql:  "SELECT 1;;;SELECT 2;",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "final statement without a trailing semicolon is kept",
			sql:  "SELECT 1;\nSELECT 2",
			want: []string{"SELECT 1", "SELECT 2"},
		},
		{
			name: "empty input",
			sql:  "",
			want: nil,
		},
		{
			name: "comments only",
			sql:  "-- a\n/* b */\n",
			want: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := splitSQLStatements(tc.sql)
			if len(got) != len(tc.want) {
				t.Fatalf("got %d statements %q, want %d %q",
					len(got), got, len(tc.want), tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("statement %d:\n got: %q\nwant: %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// TestSplitPreservesEveryStatementOfARealFile guards the property that actually
// matters. The outage was not a mangled string, it was a file that silently
// stopped applying part way through: every table declared after the bad line
// was never created, and nothing reported an error the operator would connect
// to the cause.
func TestSplitPreservesEveryStatementOfARealFile(t *testing.T) {
	sql := `
CREATE SCHEMA IF NOT EXISTS demo;

-- public key + sign_count verify assertions at login; credential_id
-- is the authenticator's handle (unique).
CREATE TABLE IF NOT EXISTS demo.creds (
    id    UUID PRIMARY KEY,
    label TEXT NOT NULL DEFAULT 'a;b'
);

CREATE INDEX IF NOT EXISTS idx_creds ON demo.creds (label);

COMMENT ON TABLE demo.creds IS 'holds; semicolons';
`
	got := splitSQLStatements(sql)
	if len(got) != 4 {
		t.Fatalf("expected 4 statements, got %d: %q", len(got), got)
	}
	for _, want := range []string{"CREATE SCHEMA", "CREATE TABLE", "CREATE INDEX", "COMMENT ON"} {
		found := false
		for _, stmt := range got {
			if strings.Contains(stmt, want) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("no statement contains %q; the file stopped applying early", want)
		}
	}
}

func TestDollarTag(t *testing.T) {
	cases := []struct {
		in      string
		wantTag string
		wantOK  bool
	}{
		{"$$body$$", "$$", true},
		{"$fn$body$fn$", "$fn$", true},
		{"$a_1$x$a_1$", "$a_1$", true},
		// A lone $ (parameter placeholder) must not open a dollar quote, or the
		// splitter would swallow the rest of the file looking for a closing tag.
		{"$1", "", false},
		{"$", "", false},
		{"$ $", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			tag, ok := dollarTag([]rune(tc.in), 0)
			if ok != tc.wantOK || tag != tc.wantTag {
				t.Fatalf("dollarTag(%q) = (%q, %v), want (%q, %v)",
					tc.in, tag, ok, tc.wantTag, tc.wantOK)
			}
		})
	}
}

func TestApplyRejectsUnusableInput(t *testing.T) {
	// Both are caller mistakes that would otherwise surface as a service that
	// booted "successfully" against a database with no schema.
	if err := Apply(t.Context(), nil, "SELECT 1;"); err == nil {
		t.Error("expected an error for a nil pool")
	}
	if err := Apply(t.Context(), nil, "   \n\t "); err == nil {
		t.Error("expected an error for empty schema SQL")
	}
}

// TestFirstLineNamesTheSQLNotAComment guards the debugging experience. These
// schema files are mostly comments, so the naive "first line" named a comment
// about a different table than the one that actually failed.
func TestFirstLineNamesTheSQLNotAComment(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "skips leading comment lines",
			in:   "-- profile.profiles is NOT created here.\n-- It is provisioned by auth-service.\nCREATE TABLE profile.user_links (",
			want: "CREATE TABLE profile.user_links (",
		},
		{
			name: "skips blank lines",
			in:   "\n\n   \nCREATE INDEX idx_a ON t (c);",
			want: "CREATE INDEX idx_a ON t (c);",
		},
		{
			name: "plain statement is unchanged",
			in:   "SELECT 1",
			want: "SELECT 1",
		},
		{
			name: "all-comment falls back rather than returning empty",
			in:   "-- only a comment",
			want: "-- only a comment",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := firstLine(tc.in); got != tc.want {
				t.Fatalf("firstLine() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFirstLineTruncatesLongStatements(t *testing.T) {
	got := firstLine(strings.Repeat("x", 200))
	if len([]rune(got)) != 81 { // 80 runes + the ellipsis
		t.Fatalf("expected an 80-rune truncation plus ellipsis, got %d runes", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("truncated output should be marked with an ellipsis, got %q", got)
	}
}
