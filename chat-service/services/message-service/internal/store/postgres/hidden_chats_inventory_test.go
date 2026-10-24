package postgres

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Hidden chats: every query that lists a member's conversations, or anything
// derived from them, must exclude the member's locked rows. This scan finds
// every store function that reads the viewer's own membership row
// (`chat.conversation_members m` + `m.user_id = $n`) and requires the
// filter, unless the function is classified below with a reason. A new list
// query therefore cannot forget it.

// memberScopedExempt: member-scoped reads that are NOT lists.
var memberScopedExempt = map[string]string{
	"GetInboxMeta":                   "per-row decoration of a page that was already filtered",
	"ListHiddenConversations":        "the hidden list itself (filters m.locked the other way)",
	"ViewerMayAccessLockedChatMedia": "the hidden-media check itself (needs m.locked; the caller requires an open token)",
	"UserConversations":              "account purge: must erase hidden conversations too",
	"HideMessageForMe":               "a write on one conversation (its route is behind the open-token gate)",
	"SetDatingReceiptsUntil":         "internal dating write on one match, no listing",
	"DatingReceiptsAllowed":          "per-conversation receipt check, no listing",
}

// lockFilteredWithoutMemberJoin: list/derived queries without an `m` join
// that must use notLockedFor.
var lockFilteredWithoutMemberJoin = []string{
	"GetStarredMessages",       // GET /v1/chat/starred
	"GetFolderConversations",   // folder contents
	"ListConversationsByLabel", // label lanes
	"ViewerMayAccessChatMedia", // media of a hidden chat needs the open token
}

var memberScoped = regexp.MustCompile(`m\.user_id = \$\d`)

func storeFuncBodies(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		f, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			out[fn.Name.Name] = string(src[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
		}
	}
	return out
}

func TestEveryMemberListQueryExcludesLockedConversations(t *testing.T) {
	bodies := storeFuncBodies(t)
	found := 0
	for name, body := range bodies {
		if !strings.Contains(body, "chat.conversation_members m") || !memberScoped.MatchString(body) {
			continue
		}
		found++
		if _, ok := memberScopedExempt[name]; ok {
			continue
		}
		if !strings.Contains(body, "memberNotLocked") {
			t.Errorf("%s reads the viewer's membership rows without excluding hidden (locked) conversations: add `+memberNotLocked+` or classify it in memberScopedExempt", name)
		}
	}
	if found < 8 {
		t.Fatalf("scan found only %d member-scoped queries; the scan itself is broken", found)
	}
	for name := range memberScopedExempt {
		if _, ok := bodies[name]; !ok {
			t.Errorf("stale exemption %s", name)
		}
	}
	for _, name := range lockFilteredWithoutMemberJoin {
		body, ok := bodies[name]
		if !ok {
			t.Errorf("classified list %s no longer exists", name)
			continue
		}
		if !strings.Contains(body, "notLockedFor(") && !strings.Contains(body, "memberNotLocked") {
			t.Errorf("%s must exclude the viewer's hidden conversations", name)
		}
	}
}

func TestNotLockedForNamesTheViewer(t *testing.T) {
	got := notLockedFor("x.conversation_id", "$1")
	for _, want := range []string{"lk.conversation_id = x.conversation_id", "lk.user_id = $1", "lk.locked", "NOT EXISTS"} {
		if !strings.Contains(got, want) {
			t.Fatalf("notLockedFor = %q, missing %q", got, want)
		}
	}
}

func TestLockoutLadderEscalates(t *testing.T) {
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	ladder := []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour}
	var st AttemptState
	for round, want := range []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour, 24 * time.Hour} {
		for i := 0; i < 4; i++ {
			st = NextAttemptState(st, now, 5, ladder)
			if st.LockedUntil != nil && st.LockedUntil.After(now) {
				t.Fatalf("round %d: locked after only %d failures", round, i+1)
			}
		}
		st = NextAttemptState(st, now, 5, ladder)
		if st.LockedUntil == nil || st.LockedUntil.Sub(now) != want || st.Failures != 0 {
			t.Fatalf("round %d: %+v, want lock %v", round, st, want)
		}
		now = st.LockedUntil.Add(time.Second)
	}
}

// The failure count is written to Postgres in the same call that decides the
// lockout — not kept in process memory or only in Redis.
func TestFailureCountIsWrittenToPostgres(t *testing.T) {
	user := uuid.New()
	s, db := newFakeStore(t, func(sql string, _ []any) [][]any {
		if strings.Contains(sql, "FROM chat.hidden_chat_attempts") && strings.Contains(sql, "FOR UPDATE") {
			return [][]any{{4, 0, (*time.Time)(nil)}}
		}
		return nil
	})
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	st, err := s.RecordHiddenChatFailure(context.Background(), user, SecretKindPassword, now, 5, []time.Duration{15 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if st.LockedUntil == nil || !st.LockedUntil.Equal(now.Add(15*time.Minute)) {
		t.Fatalf("fifth failure did not lock: %+v", st)
	}
	wrote := false
	for _, c := range db.calls {
		if strings.Contains(c.sql, "UPDATE chat.hidden_chat_attempts") && strings.Contains(c.sql, "locked_until = $5") {
			wrote = true
			if c.args[0] != user || c.args[4] == nil {
				t.Fatalf("lockout not persisted for the user: %v", c.args)
			}
		}
	}
	if !wrote {
		t.Fatal("lockout state was not written to chat.hidden_chat_attempts")
	}
}

// A strict reset clears ONLY the resetting user's locked rows.
func TestStrictResetWipeIsScopedToTheUser(t *testing.T) {
	user := uuid.New()
	s, db := newFakeStore(t, nil)
	_, err := s.ResetHiddenChats(context.Background(), HiddenChatsReset{
		Settings: HiddenChatSettings{UserID: user, SecretKeyHash: "$argon2id$x", ChatPasswordHash: "$argon2id$y"},
		Wipe:     true, At: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	var wipe *dbCall
	for i, c := range db.calls {
		if strings.Contains(c.sql, "UPDATE chat.conversation_members") {
			wipe = &db.calls[i]
		}
	}
	if wipe == nil {
		t.Fatal("strict reset issued no wipe")
	}
	for _, want := range []string{"WHERE user_id = $1 AND locked AND left_at IS NULL", "hidden = TRUE", "locked = FALSE", "cleared_before"} {
		if !strings.Contains(wipe.sql, want) {
			t.Fatalf("wipe SQL missing %q:\n%s", want, wipe.sql)
		}
	}
	if wipe.args[0] != user {
		t.Fatalf("wipe not bound to the user: %v", wipe.args)
	}
	// Non-strict: no member row is touched.
	s2, db2 := newFakeStore(t, nil)
	_, _ = s2.ResetHiddenChats(context.Background(), HiddenChatsReset{
		Settings: HiddenChatSettings{UserID: user, SecretKeyHash: "$argon2id$x", ChatPasswordHash: "$argon2id$y"},
	})
	for _, c := range db2.calls {
		if strings.Contains(c.sql, "chat.conversation_members") {
			t.Fatal("a non-strict reset touched membership rows")
		}
	}
}

// The schema refuses a non-Argon2id value at the database too.
func TestSchemaRefusesClearSecrets(t *testing.T) {
	for _, path := range []string{"../../../database/setup.sql", "../../../migrations/018_hidden_chats.sql"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		s := string(raw)
		if !strings.Contains(s, "secret_key_hash LIKE '$argon2id$%' AND chat_password_hash LIKE '$argon2id$%'") {
			t.Fatalf("%s lost the hashes-only CHECK", path)
		}
		if !strings.Contains(s, "ADD COLUMN IF NOT EXISTS locked    BOOLEAN NOT NULL DEFAULT FALSE") {
			t.Fatalf("%s lost conversation_members.locked", path)
		}
	}
}
