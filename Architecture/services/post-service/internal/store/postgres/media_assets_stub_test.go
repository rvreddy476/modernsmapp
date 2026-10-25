package postgres

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The media_assets stub guard.
//
// # WHY
//
// The integration database gets media-service's media_assets table from a
// hand-written stub (scripts/ci/sql/post-service-stubs.sql), not from
// media-service's setup.sql (the stub explains why). The stub drifted: the
// production queries read alt_text, alt_decorative, width, height and
// hls_master_key, the stub had none of them, and every suite that ran before
// a helper happened to ADD COLUMN them failed with 42703. Nothing compared
// the stub with the queries, so this test derives both from the source:
//
//   - the columns post-service reads are taken from every string literal in
//     the module's production Go files: `ma.<col>` anywhere (ma is the
//     media_assets alias everywhere in this module), and, in a literal that
//     names media_assets without an alias, every identifier that is a real
//     media_assets column;
//   - the real columns come from media-service's setup.sql and migrations;
//   - the stub's columns come from its CREATE TABLE media_assets.
//
// It fails when a read column is missing from the stub, or when post-service
// reads an `ma.` column media-service does not have.

var (
	reMAAlias      = regexp.MustCompile(`\bma\.([a-z_][a-z0-9_]*)`)
	reMediaAssets  = regexp.MustCompile(`\bmedia_assets\b`)
	reMediaAliased = regexp.MustCompile(`\bmedia_assets\s+(?:AS\s+)?ma\b`)
	reIdent        = regexp.MustCompile(`\b[a-z_][a-z0-9_]*\b`)
	reSQLComment   = regexp.MustCompile(`--[^\n]*`)
	reAddColumn    = regexp.MustCompile(`(?i)\bADD\s+COLUMN\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-z_][a-z0-9_]*)`)
	reRenameColumn = regexp.MustCompile(`(?i)\bRENAME\s+COLUMN\s+[a-z_][a-z0-9_]*\s+TO\s+([a-z_][a-z0-9_]*)`)
	reAlterMedia   = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?(?:ONLY\s+)?(?:public\.)?media_assets\b`)
	reCreateMedia  = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(?:public\.)?media_assets\s*\(`)
)

// findUp walks up from the working directory until rel exists.
func findUp(t *testing.T, rel string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		p := filepath.Join(dir, rel)
		if _, err := os.Stat(p); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("%s not found above the working directory", rel)
		}
		dir = parent
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// createTableColumns returns the column names of the first
// CREATE TABLE media_assets (...) in sql, or nil when there is none.
func createTableColumns(sql string) []string {
	sql = reSQLComment.ReplaceAllString(sql, "")
	loc := reCreateMedia.FindStringIndex(sql)
	if loc == nil {
		return nil
	}
	body, depth, start := sql[loc[1]:], 1, 0
	var entries []string
	for i, r := range body {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				entries = append(entries, body[start:i])
				goto done
			}
		case ',':
			if depth == 1 {
				entries = append(entries, body[start:i])
				start = i + 1
			}
		}
	}
done:
	var cols []string
	for _, e := range entries {
		f := strings.Fields(e)
		if len(f) == 0 {
			continue
		}
		switch strings.ToUpper(f[0]) {
		case "PRIMARY", "CONSTRAINT", "UNIQUE", "CHECK", "FOREIGN", "EXCLUDE", "LIKE":
			continue
		}
		cols = append(cols, strings.ToLower(strings.Trim(f[0], `"`)))
	}
	return cols
}

// realMediaAssetsColumns is media-service's media_assets as setup.sql and its
// migrations build it.
func realMediaAssetsColumns(t *testing.T, mediaDB string) map[string]bool {
	t.Helper()
	files := []string{filepath.Join(mediaDB, "setup.sql")}
	migs, err := filepath.Glob(filepath.Join(mediaDB, "migrations", "*.sql"))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, migs...)
	cols := map[string]bool{}
	for _, f := range files {
		sql := reSQLComment.ReplaceAllString(readFile(t, f), "")
		for _, c := range createTableColumns(sql) {
			cols[c] = true
		}
		for _, stmt := range strings.Split(sql, ";") {
			if !reAlterMedia.MatchString(stmt) {
				continue
			}
			for _, m := range reAddColumn.FindAllStringSubmatch(stmt, -1) {
				cols[strings.ToLower(m[1])] = true
			}
			for _, m := range reRenameColumn.FindAllStringSubmatch(stmt, -1) {
				cols[strings.ToLower(m[1])] = true
			}
		}
	}
	if len(cols) < 10 || !cols["uploader_id"] || !cols["hls_master_key"] {
		t.Fatalf("media-service schema parse looks wrong: %v", sortedKeys(cols))
	}
	return cols
}

// mediaAssetsReads returns column -> the files that read it, from every string
// literal in the module's production Go files.
func mediaAssetsReads(t *testing.T, moduleRoot string, real map[string]bool) map[string][]string {
	t.Helper()
	reads := map[string][]string{}
	add := func(col, where string) {
		for _, w := range reads[col] {
			if w == where {
				return
			}
		}
		reads[col] = append(reads[col], where)
	}
	fset := token.NewFileSet()
	err := filepath.WalkDir(moduleRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "vendor" || name == "testdata" || (strings.HasPrefix(name, ".") && path != moduleRoot) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(moduleRoot, path)
		rel = filepath.ToSlash(rel)
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			for _, m := range reMAAlias.FindAllStringSubmatch(s, -1) {
				add(m[1], rel)
			}
			if reMediaAssets.MatchString(s) && !reMediaAliased.MatchString(s) {
				for _, id := range reIdent.FindAllString(s, -1) {
					if real[id] {
						add(id, rel)
					}
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return reads
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestMediaAssetsStubCoversEveryColumnPostServiceReads(t *testing.T) {
	moduleRoot := findUp(t, "go.mod")
	repoRoot := findUp(t, filepath.Join("scripts", "ci", "sql", "post-service-stubs.sql"))
	stubPath := filepath.Join(repoRoot, "scripts", "ci", "sql", "post-service-stubs.sql")
	mediaDB := filepath.Join(repoRoot, "Architecture", "services", "media-service", "database")

	stubCols := createTableColumns(readFile(t, stubPath))
	if len(stubCols) == 0 {
		t.Fatalf("%s has no CREATE TABLE media_assets", stubPath)
	}
	stub := map[string]bool{}
	for _, c := range stubCols {
		stub[c] = true
	}
	real := realMediaAssetsColumns(t, mediaDB)
	reads := mediaAssetsReads(t, moduleRoot, real)
	if len(reads) == 0 {
		t.Fatal("found no media_assets reads in post-service; the scan is broken")
	}

	cols := make([]string, 0, len(reads))
	for c := range reads {
		cols = append(cols, c)
	}
	sort.Strings(cols)
	for _, c := range cols {
		if !real[c] {
			t.Errorf("post-service reads ma.%s (%s), which media-service's media_assets does not have",
				c, strings.Join(reads[c], ", "))
			continue
		}
		if !stub[c] {
			t.Errorf("scripts/ci/sql/post-service-stubs.sql: media_assets lacks %s, read by %s",
				c, strings.Join(reads[c], ", "))
		}
	}
	for _, c := range stubCols {
		if !real[c] {
			t.Errorf("scripts/ci/sql/post-service-stubs.sql: media_assets.%s is not a media-service column", c)
		}
	}
}
