package postgres

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// feed-30: toggleReactionTx over a scripted transaction, so the order of
// the statements and what a toggle reports are checked without a database
// (the concurrency itself is reactions_toggle_integration_test.go, CI). Any
// pgx.Tx method the fake does not override panics: the embedded interface
// is nil, so an unexpected call fails the test loudly.

// reactionTx is one viewer's reaction on one target, as the reactions table
// holds it, plus a log of what the transaction did.
type reactionTx struct {
	pgx.Tx
	stored string // the viewer's reaction_type, "" for none

	// beforeWrite runs between the toggle's read and its write: another
	// writer that does not take the lock.
	beforeWrite func(tx *reactionTx)

	log     []string
	lockKey string
	outbox  []string
}

type reactionRow struct {
	value string
	found bool
}

func (r reactionRow) Scan(dest ...any) error {
	if !r.found {
		return pgx.ErrNoRows
	}
	*(dest[0].(*string)) = r.value
	return nil
}

func tag(verb string, rows int) pgconn.CommandTag {
	if verb == "INSERT" {
		return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", rows))
	}
	return pgconn.NewCommandTag(fmt.Sprintf("%s %d", verb, rows))
}

func (t *reactionTx) write() {
	if t.beforeWrite != nil {
		hook := t.beforeWrite
		t.beforeWrite = nil
		hook(t)
	}
}

func (t *reactionTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	switch {
	case sql == reactionLockSQL:
		t.log = append(t.log, "lock")
		t.lockKey, _ = args[0].(string)
		return pgconn.NewCommandTag("SELECT 1"), nil
	case sql == reactionDeleteSQL:
		t.write()
		t.log = append(t.log, "delete")
		if t.stored == "" {
			return tag("DELETE", 0), nil
		}
		t.stored = ""
		return tag("DELETE", 1), nil
	case sql == reactionUpdateSQL:
		t.write()
		t.log = append(t.log, "update")
		if t.stored == "" {
			return tag("UPDATE", 0), nil
		}
		t.stored = args[3].(string)
		return tag("UPDATE", 1), nil
	case sql == reactionInsertSQL:
		t.write()
		t.log = append(t.log, "insert")
		if t.stored != "" { // ON CONFLICT DO NOTHING
			return tag("INSERT", 0), nil
		}
		t.stored = args[4].(string)
		return tag("INSERT", 1), nil
	case strings.Contains(sql, "INSERT INTO post_outbox_events"):
		t.log = append(t.log, "outbox")
		t.outbox = append(t.outbox, args[0].(string))
		return tag("INSERT", 1), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", sql)
}

func (t *reactionTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	if sql != reactionReadSQL {
		panic("unexpected query: " + sql)
	}
	t.log = append(t.log, "read")
	return reactionRow{value: t.stored, found: t.stored != ""}
}

// postEvent is the rule service.reactionEvent applies.
func postEvent(t ReactionToggle) (string, interface{}, bool) {
	switch {
	case t.IsSet && t.PrevType == "":
		return events.PostReacted, map[string]string{}, true
	case !t.IsSet && t.PrevType != "":
		return events.PostUnreacted, map[string]string{}, true
	}
	return "", nil, false
}

type mirrorLog struct {
	tx   *reactionTx
	seen []ReactionToggle
	err  error
}

func (m *mirrorLog) mirror(_ context.Context, t ReactionToggle) error {
	m.tx.log = append(m.tx.log, "mirror")
	m.seen = append(m.seen, t)
	return m.err
}

func runToggle(t *testing.T, tx *reactionTx, target string, m *mirrorLog) (ReactionToggle, error) {
	t.Helper()
	m.tx = tx
	return toggleReactionTx(context.Background(), tx, target, uuid.New(), uuid.New(), "like", postEvent, m.mirror)
}

// The lock comes first: before the read it protects.
func TestToggleReactionTx_LocksBeforeItReads(t *testing.T) {
	post, user := uuid.New(), uuid.New()
	tx := &reactionTx{}
	m := &mirrorLog{tx: tx}
	if _, err := toggleReactionTx(context.Background(), tx, "post", post, user, "like", postEvent, m.mirror); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(tx.log); got != "[lock read insert outbox mirror]" {
		t.Fatalf("statements = %s, want the lock, then the read, the write, the event and the mirror", got)
	}
	if tx.lockKey != reactionLockKey("post", post, user) {
		t.Fatalf("lock key = %q, want this viewer's reaction on this post", tx.lockKey)
	}
}

func TestToggleReactionTx_AddsAndRemoves(t *testing.T) {
	tx := &reactionTx{}
	m := &mirrorLog{}

	added, err := runToggle(t, tx, "post", m)
	if err != nil {
		t.Fatal(err)
	}
	if added != (ReactionToggle{NewType: "like", IsSet: true}) || tx.stored != "like" {
		t.Fatalf("first tap = %+v, stored %q", added, tx.stored)
	}

	removed, err := runToggle(t, tx, "post", m)
	if err != nil {
		t.Fatal(err)
	}
	if removed != (ReactionToggle{PrevType: "like"}) || tx.stored != "" {
		t.Fatalf("second tap = %+v, stored %q", removed, tx.stored)
	}
	if fmt.Sprint(tx.outbox) != fmt.Sprint([]string{events.PostReacted, events.PostUnreacted}) {
		t.Fatalf("outbox = %v, want one PostReacted then one PostUnreacted", tx.outbox)
	}
	if len(m.seen) != 2 || m.seen[0] != added || m.seen[1] != removed {
		t.Fatalf("mirror saw %+v, want each toggle's outcome", m.seen)
	}
}

// Another writer put the like there between the read and the insert. The
// insert reports no row: the toggle settles on the stored like, announces
// nothing, and its mirror is the idempotent copy of what is stored.
func TestToggleReactionTx_InsertThatTouchedNoRowSettles(t *testing.T) {
	tx := &reactionTx{beforeWrite: func(tx *reactionTx) { tx.stored = "like" }}
	m := &mirrorLog{}

	got, err := runToggle(t, tx, "post", m)
	if err != nil {
		t.Fatalf("toggle: %v (it used to fail on the unique index)", err)
	}
	if got != (ReactionToggle{NewType: "like", PrevType: "like", IsSet: true}) {
		t.Fatalf("toggle = %+v, want it settled on the stored like", got)
	}
	if len(tx.outbox) != 0 {
		t.Fatalf("a toggle that added nothing announced %v", tx.outbox)
	}
	if fmt.Sprint(tx.log) != "[lock read insert read mirror]" {
		t.Fatalf("statements = %v, want a second read after the insert that touched no row", tx.log)
	}
	if len(m.seen) != 1 || m.seen[0] != got {
		t.Fatalf("mirror saw %+v", m.seen)
	}
}

// The like was removed between the read and the delete: this toggle
// removed nothing, so there is no PostUnreacted. Fifty requests that all
// read "liked" used to write fifty.
func TestToggleReactionTx_DeleteThatTouchedNoRowAnnouncesNothing(t *testing.T) {
	tx := &reactionTx{stored: "like", beforeWrite: func(tx *reactionTx) { tx.stored = "" }}
	m := &mirrorLog{}

	got, err := runToggle(t, tx, "post", m)
	if err != nil {
		t.Fatal(err)
	}
	if got != (ReactionToggle{}) {
		t.Fatalf("toggle = %+v, want unset with nothing before", got)
	}
	if len(tx.outbox) != 0 {
		t.Fatalf("a delete that removed nothing announced %v", tx.outbox)
	}
}

// A switch of type whose row vanished settles the same way.
func TestToggleReactionTx_UpdateThatTouchedNoRowSettles(t *testing.T) {
	tx := &reactionTx{stored: "love", beforeWrite: func(tx *reactionTx) { tx.stored = "" }}
	m := &mirrorLog{}

	got, err := runToggle(t, tx, "story", m)
	if err != nil {
		t.Fatal(err)
	}
	if got != (ReactionToggle{}) || len(tx.outbox) != 0 {
		t.Fatalf("toggle = %+v, outbox %v", got, tx.outbox)
	}
}

// A mirror that fails fails the toggle; the caller rolls back.
func TestToggleReactionTx_MirrorErrorIsReturned(t *testing.T) {
	boom := errors.New("scylla timeout")
	tx := &reactionTx{}
	m := &mirrorLog{err: boom}

	if _, err := runToggle(t, tx, "post", m); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the mirror error", err)
	}
}

// No hooks: the row is still written under the lock.
func TestToggleReactionTx_NoHooks(t *testing.T) {
	tx := &reactionTx{}
	got, err := toggleReactionTx(context.Background(), tx, "post", uuid.New(), uuid.New(), "like", nil, nil)
	if err != nil || !got.IsSet {
		t.Fatalf("toggle = %+v, %v", got, err)
	}
	if fmt.Sprint(tx.log) != "[lock read insert]" {
		t.Fatalf("statements = %v", tx.log)
	}
}
