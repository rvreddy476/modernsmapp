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

// broadcast_channels.updated_at is the row's version (2026-11-08; see
// BroadcastChannel). search-service drops a snapshot whose updated_at is not
// newer than the one it holds, and NOW() is the transaction's start time: a
// writer that waited on another's row lock could commit the newer state under
// the older stamp. These tests hold every statement in the module that sets
// the column to the strictly increasing stamp; the behaviour runs in
// updated_at_stamp_integration_test.go (CI).

const channelStampTail = `GREATEST(clock_timestamp(), %s.updated_at + interval '1 microsecond')`

var (
	updateChannelsRe = regexp.MustCompile(`(?i)\bUPDATE\s+broadcast_channels\b(?:\s+(?:AS\s+)?(\w+))?`)
	// A SET target: updated_at not qualified by a table (a SET column never
	// is), followed by '='. A comparison in WHERE would match too, which is
	// the point: nothing else may write or compare it here without review.
	updatedAtAssignRe = regexp.MustCompile(`(?i)(?:^|[^.\w])updated_at\s*=\s*`)
)

// sqlLiteral is one string literal holding an UPDATE of broadcast_channels.
type sqlLiteral struct {
	pos  string
	text string
}

// channelUpdateLiterals walks every non-test Go file in the module.
func channelUpdateLiterals(t *testing.T) []sqlLiteral {
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
				if uerr != nil || !updateChannelsRe.MatchString(s) {
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
	qual := "broadcast_channels"
	if m := updateChannelsRe.FindStringSubmatch(sql); m != nil && m[1] != "" && !strings.EqualFold(m[1], "SET") {
		qual = m[1]
	}
	want := strings.Replace(channelStampTail, "%s", qual, 1)
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

func TestEveryChannelRowWriteStampsAStrictlyNewerUpdatedAt(t *testing.T) {
	lits := channelUpdateLiterals(t)
	stamped := 0
	for _, l := range lits {
		ok, problem := checkStamp(l.text)
		if problem != "" {
			t.Errorf("%s: broadcast_channels.updated_at must be strictly newer than the version it replaces: %s", l.pos, problem)
		}
		if ok {
			stamped++
		}
	}
	// UpdateChannel, DeleteChannel, IncrementUpdateCount, ArchiveChannel,
	// SetChannelStatus, AdminSetChannelSuspended, the scheduler's count bump.
	// Fewer means the walk lost statements, not that they are fine.
	if stamped < 7 {
		t.Fatalf("found %d stamped broadcast_channels UPDATEs, want at least 7 (did the walk miss files?)", stamped)
	}
}

// The checker itself: the old stamp and a lookalike are refused, an alias is
// followed, and a statement that does not touch the column passes.
func TestChannelStampCheckerRefusesTheOldStamp(t *testing.T) {
	for _, bad := range []string{
		`UPDATE broadcast_channels SET status = $2, updated_at = NOW() WHERE id = $1`,
		`UPDATE broadcast_channels SET updated_at=now() WHERE id = $1`,
		`UPDATE broadcast_channels SET updated_at = clock_timestamp() WHERE id = $1`,
		`UPDATE broadcast_channels SET updated_at = GREATEST(NOW(), broadcast_channels.updated_at + interval '1 microsecond')`,
		`UPDATE broadcast_channels b SET updated_at = GREATEST(clock_timestamp(), broadcast_channels.updated_at + interval '1 microsecond')`,
	} {
		if _, problem := checkStamp(bad); problem == "" {
			t.Errorf("accepted: %s", bad)
		}
	}
	for _, good := range []string{
		`UPDATE broadcast_channels SET status = $2, updated_at = GREATEST(clock_timestamp(), broadcast_channels.updated_at + interval '1 microsecond') WHERE id = $1`,
		`UPDATE broadcast_channels b SET n = 1, updated_at = GREATEST(clock_timestamp(), b.updated_at + interval '1 microsecond') FROM x WHERE b.id = x.id`,
	} {
		if ok, problem := checkStamp(good); problem != "" || !ok {
			t.Errorf("refused or not counted (%q): %s", problem, good)
		}
	}
	if ok, problem := checkStamp(recountSubscribersSQL); ok || problem != "" {
		t.Errorf("the recount does not touch updated_at and must pass unstamped: ok=%v %s", ok, problem)
	}
}

// The channel.updated snapshot after an edit is built from ch, which the
// service read before the write. The columns UpdateChannel does not write
// must come back from the committed row, or a suspension committed between
// the read and the write would be announced as 'active' under the newer
// updated_at, and search would re-list the channel.
func TestUpdateChannelReturnsTheCommittedRow(t *testing.T) {
	fn := funcBody(t, "channel.go", "UpdateChannel")
	for _, must := range []string{
		"RETURNING owner_id, is_verified, subscriber_count, update_count, status,",
		"created_at, updated_at, deleted_at`",
		".Scan(&ch.OwnerID, &ch.IsVerified, &ch.SubscriberCount, &ch.UpdateCount, &ch.Status,",
		"&ch.CreatedAt, &ch.UpdatedAt, &ch.DeletedAt)",
	} {
		if !strings.Contains(fn, must) {
			t.Errorf("UpdateChannel lost %q: the snapshot would mix a stale read with the new version", must)
		}
	}
}
