package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PUT /v1/posts/:postId/membership is a search-eligibility change
// (2026-10-23): the gate write, the search_rev bump and
// PostSearchEligibilityChanged carrying tier_required_id share one
// transaction. The transaction here is a recording fake; the same path runs
// against real Postgres in membership_gate_integration_test.go.

// gateTx records what the membership write does inside its transaction. Any
// pgx.Tx method it does not override panics (the embedded interface is nil),
// so an unexpected statement fails the test loudly.
type gateTx struct {
	pgx.Tx
	updated  int64 // rows the UPDATE matches
	missing  bool  // the post does not exist (answers the existence probe)
	held     bool  // the post is under an active restriction (migration 073 guard)
	row      gateRow
	execs    []string
	queries  []string
	payloads []events.PostSearchEligibilityChangedPayload
	types    []string
	// visibility is every post.visibility_changed written (2026-10-30).
	visibility []PostVisibilityChangedEvent
}

type gateRow struct {
	author      uuid.UUID
	visibility  string
	review      string
	text, title string
	contentType string
	rev         int64
	tier        *uuid.UUID
}

func (t *gateTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, sql)
	switch {
	case strings.Contains(sql, "UPDATE posts SET tier_required_id"):
		// The hold guard is the statement's own: a gate on a held post
		// matches no row (what Postgres does with the WHERE clause).
		if tier, _ := args[1].(*uuid.UUID); tier != nil && t.held &&
			strings.Contains(sql, "$2::uuid IS NULL OR active_restriction_count = 0") {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		if tier, _ := args[1].(*uuid.UUID); tier != nil {
			v := *tier
			t.row.tier = &v
		} else {
			t.row.tier = nil
		}
		return pgconn.NewCommandTag(fmt.Sprintf("UPDATE %d", t.updated)), nil
	case strings.Contains(sql, "INSERT INTO post_outbox_events"):
		t.types = append(t.types, args[0].(string))
		if args[0].(string) == events.EventPostVisibilityChanged {
			var v PostVisibilityChangedEvent
			if err := json.Unmarshal(args[3].([]byte), &v); err != nil {
				return pgconn.CommandTag{}, err
			}
			t.visibility = append(t.visibility, v)
			return pgconn.NewCommandTag("INSERT 0 1"), nil
		}
		var p events.PostSearchEligibilityChangedPayload
		if err := json.Unmarshal(args[3].([]byte), &p); err != nil {
			return pgconn.CommandTag{}, err
		}
		t.payloads = append(t.payloads, p)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.HasPrefix(sql, "SAVEPOINT "), strings.HasPrefix(sql, "ROLLBACK TO SAVEPOINT "),
		strings.HasPrefix(sql, "RELEASE SAVEPOINT "):
		// channelIDForAuthorTx's guard around the channel read.
		return pgconn.NewCommandTag(""), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", sql)
}

func (t *gateTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	t.queries = append(t.queries, sql)
	return gateScan{t: t, sql: sql}
}

type gateScan struct {
	t   *gateTx
	sql string
}

// Scan fills BumpSearchRevAndEmitTxRev's RETURNING list in order.
func (r gateScan) Scan(dest ...any) error {
	if strings.Contains(r.sql, "SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1)") && len(dest) == 1 {
		*dest[0].(*bool) = !r.t.missing
		return nil
	}
	if strings.Contains(r.sql, "SELECT tier_required_id FROM posts WHERE id = $1 FOR UPDATE") {
		// The gate before the write, under the row lock.
		if r.t.missing {
			return pgx.ErrNoRows
		}
		*dest[0].(**uuid.UUID) = r.t.row.tier
		return nil
	}
	if strings.Contains(r.sql, "SELECT author_id, content_type, visibility, created_at, deleted_at IS NOT NULL, publish_at") {
		// emitVisibilityNarrowedTx's read (visibility_events.go).
		*dest[0].(*uuid.UUID) = r.t.row.author
		*dest[1].(*string) = r.t.row.contentType
		*dest[2].(*string) = r.t.row.visibility
		*dest[3].(*time.Time) = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		*dest[4].(*bool) = false
		*dest[5].(**time.Time) = nil
		return nil
	}
	if strings.Contains(r.sql, "SELECT id FROM channels WHERE user_id = $1") {
		return pgx.ErrNoRows // the author has no channel
	}
	if !strings.Contains(r.sql, "SET search_rev = search_rev + 1") {
		return fmt.Errorf("unexpected query: %s", r.sql)
	}
	if len(dest) != 18 {
		return fmt.Errorf("RETURNING scans %d columns; this fake knows 18", len(dest))
	}
	row := r.t.row
	row.rev++
	r.t.row.rev = row.rev
	*dest[0].(*uuid.UUID) = row.author
	*dest[1].(*string) = row.visibility
	*dest[2].(*string) = row.review
	*dest[3].(*string) = row.text
	*dest[4].(*string) = row.title
	*dest[5].(*string) = row.contentType
	*dest[6].(*time.Time) = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	*dest[7].(**time.Time) = nil
	*dest[8].(**time.Time) = nil
	*dest[9].(*int64) = row.rev
	*dest[10].(*string) = row.review
	*dest[11].(*int) = 0
	*dest[12].(*[]string) = []string{uuid.NewString()}
	*dest[13].(*[]string) = []string{"video"}
	*dest[14].(*int) = 61000
	*dest[15].(*int) = 1080
	*dest[16].(**uuid.UUID) = row.tier
	*dest[17].(*bool) = false // earnings_excluded: no owner tracks the post
	return nil
}

func newGateTx() *gateTx {
	return &gateTx{updated: 1, row: gateRow{
		author: uuid.New(), visibility: "public", review: "approved",
		text: "the paid body", title: "Episode 1", contentType: "post", rev: 4,
	}}
}

// Setting a gate bumps search_rev and emits the eligibility event with
// tier_required_id, and the paid body stays off the bus.
func TestMembershipGateWriteEmitsEligibilityWithTier(t *testing.T) {
	tx := newGateTx()
	post, tier := uuid.New(), uuid.New()
	changed, err := setPostMembershipGateTx(context.Background(), tx, post, &tier)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if n := countContaining(tx.queries, "SET search_rev = search_rev + 1"); n != 1 {
		t.Fatalf("search_rev bumped %d times; want once", n)
	}
	if len(tx.payloads) != 1 || tx.types[0] != events.PostSearchEligibilityChanged {
		t.Fatalf("outbox: types %v payloads %d; want one %s", tx.types, len(tx.payloads), events.PostSearchEligibilityChanged)
	}
	// 2026-10-30: a gate added drops the post from every viewer's feed
	// cache (post.visibility_changed, reason membership_gate).
	if len(tx.visibility) != 1 || tx.visibility[0].Reason != VisibilityReasonMembershipGate ||
		tx.visibility[0].PostID != post.String() || tx.visibility[0].NewVisibility != "public" {
		t.Fatalf("visibility events %+v; want one membership_gate", tx.visibility)
	}
	p := tx.payloads[0]
	if p.PostID != post.String() || p.TierRequiredID != tier.String() || p.SearchRev != 5 {
		t.Fatalf("payload post=%s tier=%q rev=%d; want %s %s 5", p.PostID, p.TierRequiredID, p.SearchRev, post, tier)
	}
	if p.Text != "" || p.Title != "" || len(p.Media) != 0 || p.DurationMs != 0 {
		t.Fatalf("a gated post's body rode the eligibility event: %+v", p)
	}
	if !strings.Contains(tx.execs[0], "UPDATE posts SET tier_required_id") {
		t.Fatalf("the gate write did not come first: %v", tx.execs)
	}
}

// Clearing the gate bumps again; the event says no tier (omitted on the
// wire) and carries the body again, so search re-admits the post.
func TestMembershipGateClearEmitsEligibilityWithoutTier(t *testing.T) {
	tx := newGateTx()
	post := uuid.New()
	if _, err := setPostMembershipGateTx(context.Background(), tx, post, nil); err != nil {
		t.Fatal(err)
	}
	p := tx.payloads[0]
	if p.TierRequiredID != "" || p.Text != "the paid body" || p.Title != "Episode 1" || p.SearchRev != 5 {
		t.Fatalf("payload %+v", p)
	}
	raw, _ := json.Marshal(p)
	if strings.Contains(string(raw), "tier_required_id") {
		t.Fatalf("an open post's event carries tier_required_id: %s", raw)
	}
	if len(tx.visibility) != 0 {
		t.Fatalf("clearing a gate widens; it emitted %+v", tx.visibility)
	}
	// Changing the gate to another tier narrows again; the same tier again
	// changes nothing the feed holds.
	t1, t2 := uuid.New(), uuid.New()
	change := newGateTx()
	change.row.tier = &t1
	if _, err := setPostMembershipGateTx(context.Background(), change, post, &t2); err != nil || len(change.visibility) != 1 {
		t.Fatalf("changed gate: events %+v err %v", change.visibility, err)
	}
	same := newGateTx()
	same.row.tier = &t1
	if _, err := setPostMembershipGateTx(context.Background(), same, post, &t1); err != nil || len(same.visibility) != 0 {
		t.Fatalf("same gate: events %+v err %v", same.visibility, err)
	}
}

func countContaining(xs []string, sub string) int {
	n := 0
	for _, x := range xs {
		if strings.Contains(x, sub) {
			n++
		}
	}
	return n
}

// No such post: nothing bumped, nothing emitted.
func TestMembershipGateWriteOnAMissingPostEmitsNothing(t *testing.T) {
	tx := newGateTx()
	tx.updated = 0
	tx.missing = true
	tier := uuid.New()
	changed, err := setPostMembershipGateTx(context.Background(), tx, uuid.New(), &tier)
	if err != nil || changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if len(tx.payloads) != 0 || len(tx.visibility) != 0 {
		t.Fatalf("a missing post emitted: payloads %v visibility %v", tx.payloads, tx.visibility)
	}
	for _, q := range tx.queries {
		if strings.Contains(q, "search_rev") {
			t.Fatalf("a missing post bumped search_rev: %v", tx.queries)
		}
	}
}

// Copyright quick wins (migration 073): a post under an active hold may
// not be gated (409 COPYRIGHT_REVIEW upstream); nothing is bumped or
// emitted. Clearing a gate on a held post is always allowed.
func TestMembershipGateRefusedOnAHeldPost(t *testing.T) {
	tx := newGateTx()
	tx.held = true
	tier := uuid.New()
	changed, err := setPostMembershipGateTx(context.Background(), tx, uuid.New(), &tier)
	if !errors.Is(err, ErrPostCopyrightHeld) || changed {
		t.Fatalf("changed=%v err=%v, want ErrPostCopyrightHeld", changed, err)
	}
	if len(tx.payloads) != 0 || tx.row.tier != nil || len(tx.visibility) != 0 {
		t.Fatalf("a held post was gated: tier %v payloads %v visibility %v", tx.row.tier, tx.payloads, tx.visibility)
	}

	clear := newGateTx()
	clear.held = true
	if changed, err := setPostMembershipGateTx(context.Background(), clear, uuid.New(), nil); err != nil || !changed {
		t.Fatalf("clearing the gate on a held post: changed=%v err=%v", changed, err)
	}
}

// Every eligibility event carries the gate, whatever path emitted it: an
// already gated post approved by moderation stays out of search and keeps
// its body off the bus.
func TestEveryEligibilityEventCarriesTheGate(t *testing.T) {
	tx := newGateTx()
	tier := uuid.New()
	tx.row.tier = &tier
	if err := BumpSearchRevAndEmitTx(context.Background(), tx, uuid.New()); err != nil {
		t.Fatal(err)
	}
	p := tx.payloads[0]
	if p.TierRequiredID != tier.String() || p.Text != "" {
		t.Fatalf("payload %+v", p)
	}
}
