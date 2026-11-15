package reindex

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The users reindex and the account lifecycle (lifecycle.go): after a wipe
// nothing is stored to keep a hide, so the run itself must know which
// accounts are deactivated or scheduled for deletion.

const (
	activeUser      = "e1e1e1e1-0000-4000-8000-000000000011"
	deactivatedUser = "e2e2e2e2-0000-4000-8000-000000000012"
)

type fakeHidden struct {
	set   map[string]bool
	err   error
	calls int
}

func (f *fakeHidden) HiddenUserIDs(context.Context) (map[string]bool, error) {
	f.calls++
	return f.set, f.err
}

// fakeProfiles serves one page of /v1/profiles/discover.
func fakeProfiles(t *testing.T, ids ...string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []map[string]any{}
		if r.URL.Query().Get("offset") == "0" {
			for _, id := range ids {
				u := "u" + id[:4]
				items = append(items, map[string]any{"user_id": id, "username": u, "display_name": u})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestReindexUsers_WritesDeactivatedAccountsHidden(t *testing.T) {
	store, os := newFakeOS(t)
	hidden := &fakeHidden{set: map[string]bool{deactivatedUser: true}}
	res, err := ReindexUsers(context.Background(), nil, fakeProfiles(t, activeUser, deactivatedUser), "", store, nil, hidden, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if !res.LifecycleChecked || res.Hidden != 1 || res.Indexed != 2 || hidden.calls != 1 {
		t.Fatalf("result = %+v (lookups %d), want both indexed, one hidden, one lookup", res, hidden.calls)
	}
	if got := os.bulkDocs[deactivatedUser]["is_hidden"]; got != true {
		t.Fatalf("deactivated account is_hidden = %v, want true", got)
	}
	if got := os.bulkDocs[activeUser]["is_hidden"]; got != false {
		t.Fatalf("active account is_hidden = %v, want false", got)
	}
}

// A lifecycle source that cannot answer stops the run before anything is
// written: an empty set would publish every deactivated account.
func TestReindexUsers_RefusesWhenTheLifecycleIsUnknown(t *testing.T) {
	store, os := newFakeOS(t)
	_, err := ReindexUsers(context.Background(), nil, fakeProfiles(t, activeUser, deactivatedUser), "", store, nil,
		&fakeHidden{err: errors.New("identity database down")}, quietLog())
	if err == nil {
		t.Fatal("a reindex that cannot tell deactivated accounts ran")
	}
	if len(os.bulkDocs) != 0 {
		t.Fatalf("wrote %d documents blind", len(os.bulkDocs))
	}
}

// No source (a dev rig): the run goes ahead and says it did not check.
func TestReindexUsers_WithoutASourceReportsItDidNotCheck(t *testing.T) {
	store, _ := newFakeOS(t)
	res, err := ReindexUsers(context.Background(), nil, fakeProfiles(t, activeUser), "", store, nil, nil, quietLog())
	if err != nil {
		t.Fatal(err)
	}
	if res.LifecycleChecked || res.Hidden != 0 || res.Indexed != 1 {
		t.Fatalf("result = %+v", res)
	}
}

// ─── LoadHiddenAccounts over a fake pool ────────────────────────────────

type fakeRows struct {
	ids []string
	i   int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Next() bool                                   { r.i++; return r.i <= len(r.ids) }
func (r *fakeRows) Scan(dest ...any) error                       { *(dest[0].(*string)) = r.ids[r.i-1]; return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }

type fakeQuerier struct {
	sql  string
	args []any
	rows *fakeRows
	err  error
}

func (q *fakeQuerier) Query(_ context.Context, sql string, args ...any) (pgx.Rows, error) {
	q.sql, q.args = sql, args
	if q.err != nil {
		return nil, q.err
	}
	return q.rows, nil
}

func TestLoadHiddenAccounts_ReadsTheHiddenStatuses(t *testing.T) {
	q := &fakeQuerier{rows: &fakeRows{ids: []string{deactivatedUser}}}
	got, err := NewPostgresHiddenAccounts(q).HiddenUserIDs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[deactivatedUser] {
		t.Fatalf("hidden = %v", got)
	}
	if q.sql != hiddenAccountsSQL || len(q.args) != 1 {
		t.Fatalf("query = %q %v", q.sql, q.args)
	}
	statuses, _ := q.args[0].([]string)
	want := map[string]bool{"deactivated": true, "pending_deletion": true, "purged": true}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %v", statuses)
	}
	for _, s := range statuses {
		if !want[s] {
			t.Fatalf("statuses = %v", statuses)
		}
	}
	if _, err := LoadHiddenAccounts(context.Background(), &fakeQuerier{err: errors.New("down")}); err == nil {
		t.Fatal("a failed read returned a set")
	}
}
