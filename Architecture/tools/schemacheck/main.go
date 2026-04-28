// schemacheck — guard against schema-vs-code drift.
//
// Walks every service under Architecture/services/, parses CHECK constraints
// of the form `col IN (...)` or `col = ANY (ARRAY[...])` from the service's
// SQL files (setup.sql + migrations/*.sql), then scans the service's Go
// source for string-literal assignments to struct fields whose db-tag matches
// a constrained column. Any literal not in the allowed set is reported.
//
// Catches the bug class that hit us 4× in one session:
//   - approval_status="pending" when the constraint allowed only "draft|submitted|..."
//   - status="published" in a query against a constraint allowing "active|paused|..."
//   - payment_status="cod_pending" when the constraint had no such value
//
// Usage:  go run ./Architecture/tools/schemacheck
//
// Heuristic, not 100%. Catches the obvious literal cases. Misses values built
// from string concatenation, computed at runtime, or set via raw SQL strings.
// That tradeoff is acceptable — those are rare and easier to spot in review.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type constraintSet struct {
	source  string // file:line where defined
	table   string // best-effort table name (heuristic; may be "")
	column  string
	allowed map[string]struct{}
}

type violation struct {
	file    string
	line    int
	column  string
	value   string
	source  string
	allowed []string
}

// Matches both `col IN ('a','b')` and `col = ANY (ARRAY['a','b'])` styles.
// The column name is the first capture group; the value list is the second.
var (
	checkInRe   = regexp.MustCompile(`(?is)CHECK\s*\(\s*(\w+)\s+IN\s*\(([^)]+)\)\s*\)`)
	checkAnyRe  = regexp.MustCompile(`(?is)CHECK\s*\(\s*\(?\s*(\w+)\s*=\s*ANY\s*\(\s*ARRAY\s*\[([^\]]+)\]`)
	stringValRe = regexp.MustCompile(`'([^']+)'`)
	// Matches `CREATE TABLE [IF NOT EXISTS] foo (` plus the body up to the
	// matching closing paren. The body is captured so we can pull column names.
	createTableRe = regexp.MustCompile(`(?is)CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?["]?(\w+)["]?\s*\(`)
)

func main() {
	root, err := filepath.Abs(".")
	if err != nil {
		die(err)
	}
	// Accept either repo-root (atpost/) or Architecture-root invocation.
	candidates := []string{
		filepath.Join(root, "Architecture", "services"),
		filepath.Join(root, "services"),
	}
	servicesDir := ""
	for _, c := range candidates {
		if st, err := os.Stat(c); err == nil && st.IsDir() {
			servicesDir = c
			break
		}
	}
	if servicesDir == "" {
		die(fmt.Errorf("services dir not found relative to %s", root))
	}

	services, err := os.ReadDir(servicesDir)
	if err != nil {
		die(err)
	}

	totalViolations := 0
	for _, s := range services {
		if !s.IsDir() {
			continue
		}
		svcDir := filepath.Join(servicesDir, s.Name())
		v := checkService(svcDir, s.Name())
		totalViolations += v
	}

	if totalViolations == 0 {
		fmt.Println("✓ schemacheck: no drift detected")
		os.Exit(0)
	}
	fmt.Fprintf(os.Stderr, "\n✗ schemacheck: %d drift(s) found across services\n", totalViolations)
	os.Exit(1)
}

func checkService(svcDir, svcName string) int {
	constraints := harvestConstraints(svcDir)
	if len(constraints) == 0 {
		return 0
	}
	// A column is only worth flagging if EVERY table that has it constrains
	// it. Otherwise the same value could be valid for the unconstrained
	// table the struct actually targets — false positive territory.
	unconstrained := harvestUnconstrainedColumns(svcDir, constraints)
	violations := scanGoSources(svcDir, constraints, unconstrained)
	if len(violations) == 0 {
		return 0
	}
	fmt.Printf("\n[%s] %d drift(s):\n", svcName, len(violations))
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].file != violations[j].file {
			return violations[i].file < violations[j].file
		}
		return violations[i].line < violations[j].line
	})
	for _, v := range violations {
		fmt.Printf("  %s:%d  %s = %q  (constraint at %s allows: %s)\n",
			relPath(v.file), v.line, v.column, v.value, v.source, strings.Join(v.allowed, "|"))
	}
	return len(violations)
}

// harvestConstraints reads every .sql file under <svcDir>/database and
// parses out IN (...) / = ANY (ARRAY[...]) constraints. Returns a slice of
// every constraint found — many tables have a column named "status" with
// different allowed sets, so the caller has to consider all of them when
// checking a value.
func harvestConstraints(svcDir string) []constraintSet {
	var out []constraintSet
	dbDir := filepath.Join(svcDir, "database")
	if _, err := os.Stat(dbDir); err != nil {
		return out
	}
	_ = filepath.WalkDir(dbDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".sql") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		body := string(raw)
		for _, re := range []*regexp.Regexp{checkInRe, checkAnyRe} {
			for _, m := range re.FindAllStringSubmatchIndex(body, -1) {
				col := body[m[2]:m[3]]
				vals := body[m[4]:m[5]]
				allowed := map[string]struct{}{}
				for _, v := range stringValRe.FindAllStringSubmatch(vals, -1) {
					allowed[v[1]] = struct{}{}
				}
				if len(allowed) == 0 {
					continue
				}
				lineNum := lineOf(body, m[0])
				out = append(out, constraintSet{
					source:  fmt.Sprintf("%s:%d", relPath(path), lineNum),
					table:   nearestTable(body, m[0]),
					column:  col,
					allowed: allowed,
				})
			}
		}
		return nil
	})
	return out
}

// harvestUnconstrainedColumns returns the set of column names where at
// least one table has the column without a CHECK constraint. For those
// columns we accept any value: the struct may legitimately target the
// unconstrained table (e.g. shipments.status has no CHECK while
// orders.status does). Skipping these eliminates the false-positive
// flood that masked the real drifts the tool was built to catch.
func harvestUnconstrainedColumns(svcDir string, constraints []constraintSet) map[string]struct{} {
	// Step 1: build (table, column) -> bool of "has CHECK on this column"
	// from the constraint slice we already harvested.
	hasCheck := map[string]map[string]bool{}
	for _, c := range constraints {
		if c.table == "" {
			continue
		}
		if hasCheck[c.table] == nil {
			hasCheck[c.table] = map[string]bool{}
		}
		hasCheck[c.table][c.column] = true
	}
	// Step 2: scan SQL for every CREATE TABLE block and the columns
	// defined inside it. For each (table, column) pair without a CHECK,
	// mark the column name as unconstrained.
	out := map[string]struct{}{}
	dbDir := filepath.Join(svcDir, "database")
	_ = filepath.WalkDir(dbDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".sql") {
			return nil
		}
		raw, _ := os.ReadFile(path)
		body := string(raw)
		for _, m := range createTableRe.FindAllStringSubmatchIndex(body, -1) {
			tbl := body[m[2]:m[3]]
			// Slice from the opening paren to the matched closing paren.
			tbody := matchedBlock(body, m[1])
			for _, col := range parseColumnNames(tbody) {
				if !hasCheck[tbl][col] {
					out[col] = struct{}{}
				}
			}
		}
		return nil
	})
	return out
}

// matchedBlock returns everything inside the (...) starting at openIdx,
// respecting nested parens. openIdx points at the character AFTER the
// opening "(". Returns "" if the block is unbalanced.
func matchedBlock(body string, openIdx int) string {
	depth := 1
	for i := openIdx; i < len(body); i++ {
		switch body[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return body[openIdx:i]
			}
		}
	}
	return ""
}

// parseColumnNames pulls out the leading identifier on each line of a
// CREATE TABLE body — best-effort; misses commas inside CHECK clauses,
// which is fine since we only care about column existence not types.
func parseColumnNames(body string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		s := strings.TrimSpace(line)
		if s == "" {
			continue
		}
		// Skip lines that obviously aren't column defs.
		upper := strings.ToUpper(s)
		if strings.HasPrefix(upper, "CONSTRAINT") ||
			strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") ||
			strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CHECK") ||
			strings.HasPrefix(upper, "INDEX") ||
			strings.HasPrefix(upper, "--") {
			continue
		}
		// First token is the column name.
		end := strings.IndexAny(s, " \t,")
		if end <= 0 {
			continue
		}
		name := strings.Trim(s[:end], `"`)
		// Sanity-check it looks like an identifier.
		if !regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`).MatchString(name) {
			continue
		}
		out = append(out, name)
	}
	return out
}

// nearestTable scans backward from the constraint position to find the most
// recent CREATE TABLE or ALTER TABLE statement, returning the table name.
// Best-effort; returns "" if nothing matches.
var tableContextRe = regexp.MustCompile(`(?i)(?:CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?|ALTER\s+TABLE\s+(?:ONLY\s+)?)["]?(\w+)["]?`)

func nearestTable(body string, off int) string {
	prefix := body[:off]
	matches := tableContextRe.FindAllStringSubmatchIndex(prefix, -1)
	if len(matches) == 0 {
		return ""
	}
	last := matches[len(matches)-1]
	return prefix[last[2]:last[3]]
}

// scanGoSources walks every .go file in the service and looks for struct
// composite-literals. For each `Field: "value"` we check whether that field
// has a `db:"col"` tag matching a constrained column, and whether the value
// is in *some* allowed set for that column. Many tables have a column named
// "status" with different allowed values; we accept the value if any of those
// constraints permits it. That misses cross-table mismatches (value valid
// for orders.status but used in code targeting products.status) but kills
// the false-positive flood that made the strict mode unusable.
func scanGoSources(svcDir string, constraints []constraintSet, unconstrained map[string]struct{}) []violation {
	if len(constraints) == 0 {
		return nil
	}
	// Bucket by column name for fast lookup.
	byColumn := map[string][]constraintSet{}
	for _, c := range constraints {
		byColumn[c.column] = append(byColumn[c.column], c)
	}
	fieldToCol := buildFieldColumnMap(svcDir)
	if len(fieldToCol) == 0 {
		return nil
	}

	var violations []violation
	fset := token.NewFileSet()

	_ = filepath.WalkDir(svcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		if strings.Contains(path, string(os.PathSeparator)+"vendor"+string(os.PathSeparator)) ||
			strings.Contains(path, string(os.PathSeparator)+".gocache"+string(os.PathSeparator)) {
			return nil
		}

		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			structName := compositeLitStructName(cl.Type)
			if structName == "" {
				// Anonymous structs / map literals / slice literals — skip.
				return true
			}
			for _, elt := range cl.Elts {
				kv, ok := elt.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				key, ok := kv.Key.(*ast.Ident)
				if !ok {
					continue
				}
				col, mapped := fieldToCol[[2]string{structName, key.Name}]
				if !mapped {
					continue
				}
				if _, isAmbiguous := unconstrained[col]; isAmbiguous {
					// Some table has this column without a CHECK; we can't be
					// sure the struct doesn't target it. Skip rather than FP.
					continue
				}
				csList := byColumn[col]
				if len(csList) == 0 {
					continue
				}
				lit, ok := kv.Value.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				val := strings.Trim(lit.Value, `"`)
				if val == "" {
					continue
				}
				// Pass if any constraint with this column name allows the value.
				accepted := false
				for _, cs := range csList {
					if _, ok := cs.allowed[val]; ok {
						accepted = true
						break
					}
				}
				if accepted {
					continue
				}
				// Build the union of all allowed sets for the report so the
				// developer sees what's possible across every table that
				// has this column.
				union := map[string]struct{}{}
				for _, cs := range csList {
					for v := range cs.allowed {
						union[v] = struct{}{}
					}
				}
				allowedList := make([]string, 0, len(union))
				for a := range union {
					allowedList = append(allowedList, a)
				}
				sort.Strings(allowedList)
				pos := fset.Position(lit.Pos())
				violations = append(violations, violation{
					file:    pos.Filename,
					line:    pos.Line,
					column:  col,
					value:   val,
					source:  joinSources(csList),
					allowed: allowedList,
				})
			}
			return true
		})
		return nil
	})
	return violations
}

// compositeLitStructName extracts the bare struct name from a composite
// literal's Type expression. Handles:
//   - Foo{...}              -> "Foo"
//   - pkg.Foo{...}          -> "Foo"
//   - &Foo{...}             -> "Foo" (wrapped in *ast.UnaryExpr handled by caller)
//   - []Foo / map[K]Foo / etc — returns "" to skip non-struct literals.
func compositeLitStructName(t ast.Expr) string {
	switch v := t.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	}
	return ""
}

func joinSources(cs []constraintSet) string {
	tables := map[string]struct{}{}
	for _, c := range cs {
		if c.table != "" {
			tables[c.table] = struct{}{}
		}
	}
	if len(tables) == 0 {
		return cs[0].source
	}
	out := make([]string, 0, len(tables))
	for t := range tables {
		out = append(out, t)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// buildFieldColumnMap walks Go sources and builds a map keyed on
// (StructName, FieldName) → column. The struct-aware key prevents false
// positives where a different struct uses the same Go field name without
// a db tag (e.g. courier.ShipmentRequest.PaymentMethod is a courier API
// parameter, not a DB column).
func buildFieldColumnMap(svcDir string) map[[2]string]string {
	out := map[[2]string]string{}
	fset := token.NewFileSet()
	tagRe := regexp.MustCompile(`db:"([^",]+)`)
	_ = filepath.WalkDir(svcDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return nil
		}
		// Walk top-level type decls; each StructType has a name we can
		// pair with its field names.
		for _, decl := range f.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				structName := ts.Name.Name
				for _, field := range st.Fields.List {
					if field.Tag == nil {
						continue
					}
					m := tagRe.FindStringSubmatch(field.Tag.Value)
					if len(m) < 2 {
						continue
					}
					col := m[1]
					for _, fname := range field.Names {
						out[[2]string{structName, fname.Name}] = col
					}
				}
			}
		}
		return nil
	})
	return out
}

func lineOf(body string, off int) int {
	return strings.Count(body[:off], "\n") + 1
}

func relPath(p string) string {
	wd, err := os.Getwd()
	if err != nil {
		return p
	}
	rel, err := filepath.Rel(wd, p)
	if err != nil {
		return p
	}
	return filepath.ToSlash(rel)
}

func die(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
