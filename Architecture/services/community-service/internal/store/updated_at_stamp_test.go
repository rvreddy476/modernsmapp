package store

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// communities.updated_at is the row's version (2026-11-08; see Community).
// search-service drops a snapshot whose updated_at is not newer than the one
// it holds, and NOW() is the transaction's start time: a writer that waited
// on another's row lock could commit the newer state under the older stamp.
// These tests hold every statement in the module that sets the column to the
// strictly increasing stamp; the behaviour runs in
// updated_at_stamp_integration_test.go (CI).

const communityStampTail = `GREATEST(clock_timestamp(), %s.updated_at + interval '1 microsecond')`

var (
	updateCommunitiesRe = regexp.MustCompile(`(?i)\bUPDATE\s+communities\b(?:\s+(?:AS\s+)?(\w+))?`)
	// A SET target: updated_at not qualified by a table (a SET column never
	// is), followed by '='. A comparison in WHERE would match too, which is
	// the point: nothing else may write or compare it here without review.
	updatedAtAssignRe = regexp.MustCompile(`(?i)(?:^|[^.\w])updated_at\s*=\s*`)
)

type sqlLiteral struct {
	pos  string
	text string
}

// communityUpdateLiterals walks every non-test Go file in the module.
func communityUpdateLiterals(t *testing.T) []sqlLiteral {
	t.Helper()
	var out []sqlLiteral
	for _, root := range []string{"..", "../../cmd"} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if perr != nil {
				return perr
			}
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, uerr := strconv.Unquote(lit.Value)
				if uerr != nil || !updateCommunitiesRe.MatchString(s) {
					return true
				}
				out = append(out, sqlLiteral{pos: fset.Position(lit.Pos()).String(), text: s})
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	return out
}

// checkStamp reports what is wrong with one statement's updated_at writes, or
// "" when it has none or every one is the stamp. stamped says whether it set
// the column at all.
func checkStamp(sql string) (stamped bool, problem string) {
	qual := "communities"
	if m := updateCommunitiesRe.FindStringSubmatch(sql); m != nil && m[1] != "" && !strings.EqualFold(m[1], "SET") {
		qual = m[1]
	}
	want := strings.Replace(communityStampTail, "%s", qual, 1)
	for _, loc := range updatedAtAssignRe.FindAllStringIndex(sql, -1) {
		if !strings.HasPrefix(sql[loc[1]:], want) {
			end := loc[1] + 40
			if end > len(sql) {
				end = len(sql)
			}
			return stamped, "updated_at = " + sql[loc[1]:end] + "... (want " + want + ")"
		}
		stamped = true
	}
	return stamped, ""
}

func TestEveryCommunityRowWriteStampsAStrictlyNewerUpdatedAt(t *testing.T) {
	stamped := 0
	for _, l := range communityUpdateLiterals(t) {
		ok, problem := checkStamp(l.text)
		if problem != "" {
			t.Errorf("%s: communities.updated_at must be strictly newer than the version it replaces: %s", l.pos, problem)
		}
		if ok {
			stamped++
		}
	}
	// UpdateCommunity, DeleteCommunity, IncrementMemberCount, SetMemberCount,
	// IncrementSpaceCount, ArchiveCommunity, the member-count reconciler.
	// Fewer means the walk lost statements, not that they are fine.
	if stamped < 7 {
		t.Fatalf("found %d stamped communities UPDATEs, want at least 7 (did the walk miss files?)", stamped)
	}
}

// The checker itself: the old stamp and lookalikes are refused, an alias is
// followed, and a statement that does not touch the column passes.
func TestCommunityStampCheckerRefusesTheOldStamp(t *testing.T) {
	for _, bad := range []string{
		`UPDATE communities SET status = 'archived', updated_at = NOW() WHERE id = $1`,
		`UPDATE communities SET updated_at=now() WHERE id = $1`,
		`UPDATE communities SET updated_at = clock_timestamp() WHERE id = $1`,
		`UPDATE communities SET updated_at = GREATEST(NOW(), communities.updated_at + interval '1 microsecond')`,
		"UPDATE communities c\n SET member_count = 1,\n updated_at = NOW() FROM x WHERE c.id = x.id",
	} {
		if _, problem := checkStamp(bad); problem == "" {
			t.Errorf("accepted: %s", bad)
		}
	}
	for _, good := range []string{
		`UPDATE communities SET status = 'archived', updated_at = GREATEST(clock_timestamp(), communities.updated_at + interval '1 microsecond') WHERE id = $1`,
		"UPDATE communities c\n SET member_count = 1,\n updated_at = GREATEST(clock_timestamp(), c.updated_at + interval '1 microsecond') FROM x WHERE c.id = x.id",
	} {
		if ok, problem := checkStamp(good); problem != "" || !ok {
			t.Errorf("refused or not counted (%q): %s", problem, good)
		}
	}
	if ok, problem := checkStamp(`UPDATE communities SET post_count = post_count + 1 WHERE id = $1`); ok || problem != "" {
		t.Errorf("a statement that does not touch updated_at must pass unstamped: ok=%v %s", ok, problem)
	}
}

// The community.updated snapshot after an edit is built from c, which the
// service read before the write. The columns UpdateCommunity does not write
// must come back from the committed row, or an archive committed between the
// read and the write would be announced as 'active' under the newer
// updated_at, and search would re-list the community.
func TestUpdateCommunityReturnsTheCommittedRow(t *testing.T) {
	fn := storeFunc(t, "community.go", "UpdateCommunity")
	for _, must := range []string{
		"RETURNING owner_id, handle, member_count, space_count, is_verified, status,",
		"created_at, updated_at, deleted_at`",
		".Scan(&c.OwnerID, &c.Handle, &c.MemberCount, &c.SpaceCount, &c.IsVerified, &c.Status,",
		"&c.CreatedAt, &c.UpdatedAt, &c.DeletedAt)",
	} {
		if !strings.Contains(fn, must) {
			t.Errorf("UpdateCommunity lost %q: the snapshot would mix a stale read with the new version", must)
		}
	}
}
