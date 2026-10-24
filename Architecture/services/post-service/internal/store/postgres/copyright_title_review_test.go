package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// The hold and the clear inside their transactions, against a recording
// fake (the same paths run on real Postgres in
// copyright_title_review_integration_test.go). Any pgx.Tx method the fake
// does not override panics, so an unexpected statement fails loudly.

type titleTx struct {
	pgx.Tx
	reviewInserted int64 // rows the review INSERT reports (0 = one already exists)
	deleted        bool  // the post is soft-deleted
	stillHeld      bool  // another case still holds the post after the release
	effective      string
	execs          []string
	outbox         []string // event types, in write order
	eligibility    []events.PostSearchEligibilityChangedPayload
	reviewEvents   []events.PostCopyrightReviewPayload
	rev            int64

	// Owner-registered blocks and owner matches (migration 075).
	state        string   // the review's state as the event read sees it ("" = pending)
	reason       *string  // the review's reason as the event read sees it
	owner        *string  // the review's rights_owner as the event read sees it
	blockMisses  bool     // the system block's compare-and-set finds no pending row
	tracked      bool     // a copyright_owner_tracks row exists for the post
	authorID     uuid.UUID
	existing     *TitleReview // the post's review (nil = none)
	reasonCodes  []string     // restriction reason codes written, in order
	historyActs  []string     // copyright_title_review_events actions, in order
	historyNotes []string

	// Appeals, scheduled posts and track removal (migration 076).
	scheduled       bool        // the post is still scheduled (publish_at set)
	appealed        bool        // the event read sees appealed_at set
	appealMisses    bool        // the appeal's compare-and-set finds no blocked row under an active hold
	historyActors   []*uuid.UUID
	titleHoldActive bool     // an active title_review restriction exists (the flip's check)
	missingPost     bool     // the track removal's post lock finds no post
	otherTracks     bool     // another owner still tracks the post after a removal
	audits          []string // post_admin_audit actions written
	auditPrev       []string
	// insertedAnnounce is the announce_on_clear the review INSERT wrote.
	insertedAnnounce bool
}

func (t *titleTx) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	t.execs = append(t.execs, sql)
	switch {
	case strings.Contains(sql, "INSERT INTO copyright_title_reviews"):
		if t.reviewInserted == 1 {
			t.owner, _ = args[12].(*string)
			t.reason, _ = args[13].(*string)
			t.insertedAnnounce, _ = args[10].(bool)
		}
		return pgconn.NewCommandTag(fmt.Sprintf("INSERT 0 %d", t.reviewInserted)), nil
	case strings.Contains(sql, "SET state = 'blocked', decided_by = NULL"):
		if t.blockMisses {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		t.state = TitleReviewBlocked
		r := args[1].(string)
		t.reason = &r
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "SET state = 'pending', decided_by = NULL"):
		// The compare-and-set is the appeal's guard: only a blocked,
		// never-appealed review under an ACTIVE hold reopens.
		if !strings.Contains(sql, "WHERE id = $1 AND state = 'blocked' AND appealed_at IS NULL") ||
			!strings.Contains(sql, "pr.source = 'title_review' AND pr.state = 'active'") {
			return pgconn.CommandTag{}, fmt.Errorf("appeal update without its guard: %s", sql)
		}
		if t.appealMisses {
			return pgconn.NewCommandTag("UPDATE 0"), nil
		}
		t.state, t.appealed = TitleReviewPending, true
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "INSERT INTO post_admin_audit"):
		t.audits = append(t.audits, args[1].(string))
		t.auditPrev = append(t.auditPrev, args[4].(string))
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO copyright_title_review_events"):
		t.historyActs = append(t.historyActs, args[3].(string))
		actor, _ := args[4].(*uuid.UUID)
		t.historyActors = append(t.historyActors, actor)
		note := ""
		if n, ok := args[7].(*string); ok && n != nil {
			note = *n
		}
		t.historyNotes = append(t.historyNotes, note)
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "INSERT INTO copyright_owner_tracks"):
		if t.tracked {
			return pgconn.NewCommandTag("INSERT 0 0"), nil
		}
		t.tracked = true
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	case strings.Contains(sql, "SET author_note = $2, author_requested_at = now(), updated_at = now()\n\t\tWHERE id = $1"):
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "SET rights_owner = COALESCE(rights_owner"):
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "SET reason = COALESCE(reason, $2)"):
		if r, ok := args[1].(*string); ok && r != nil && t.reason == nil {
			t.reason = r
		}
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "INSERT INTO post_restrictions"),
		strings.Contains(sql, "SET active_restriction_count"),
		strings.Contains(sql, "UPDATE copyright_title_reviews SET announced_at"):
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "UPDATE post_restrictions"):
		t.reasonCodes = append(t.reasonCodes, args[2].(string))
		return pgconn.NewCommandTag("UPDATE 1"), nil
	case strings.Contains(sql, "INSERT INTO post_outbox_events"):
		typ := args[0].(string)
		t.outbox = append(t.outbox, typ)
		switch typ {
		case events.PostSearchEligibilityChanged:
			var p events.PostSearchEligibilityChangedPayload
			if err := json.Unmarshal(args[3].([]byte), &p); err != nil {
				return pgconn.CommandTag{}, err
			}
			t.eligibility = append(t.eligibility, p)
		case events.EventPostCopyrightReview:
			var p events.PostCopyrightReviewPayload
			if err := json.Unmarshal(args[3].([]byte), &p); err != nil {
				return pgconn.CommandTag{}, err
			}
			t.reviewEvents = append(t.reviewEvents, p)
		}
		return pgconn.NewCommandTag("INSERT 0 1"), nil
	}
	return pgconn.CommandTag{}, fmt.Errorf("unexpected exec: %s", sql)
}

func (t *titleTx) QueryRow(_ context.Context, sql string, _ ...any) pgx.Row {
	return titleScan{t: t, sql: sql}
}

type titleScan struct {
	t   *titleTx
	sql string
}

func (r titleScan) Scan(dest ...any) error {
	switch {
	case strings.Contains(r.sql, "SELECT deleted_at IS NOT NULL FROM posts"):
		*dest[0].(*bool) = r.t.deleted
		return nil
	case strings.Contains(r.sql, "SELECT deleted_at IS NOT NULL, publish_at IS NOT NULL FROM posts"):
		*dest[0].(*bool) = r.t.deleted
		*dest[1].(*bool) = r.t.scheduled
		return nil
	case strings.Contains(r.sql, "SELECT active_restriction_count > 0 FROM posts"):
		*dest[0].(*bool) = r.t.stillHeld
		return nil
	case strings.Contains(r.sql, "SELECT EXISTS (SELECT 1 FROM copyright_owner_tracks"):
		*dest[0].(*bool) = r.t.tracked
		return nil
	case strings.Contains(r.sql, "SELECT EXISTS (SELECT 1 FROM post_restrictions"):
		*dest[0].(*bool) = r.t.titleHoldActive
		return nil
	case strings.Contains(r.sql, "SELECT TRUE FROM posts WHERE id = $1 FOR UPDATE"):
		if r.t.missingPost {
			return pgx.ErrNoRows
		}
		*dest[0].(*bool) = true
		return nil
	case strings.Contains(r.sql, "DELETE FROM copyright_owner_tracks"):
		if !r.t.tracked {
			return pgx.ErrNoRows
		}
		r.t.tracked = r.t.otherTracks
		owner, ref := "Sun Pictures", "fp-123"
		*dest[0].(**string) = &owner
		*dest[1].(**string) = &ref
		return nil
	case strings.Contains(r.sql, "SELECT author_id, deleted_at IS NOT NULL"):
		*dest[0].(*uuid.UUID) = r.t.authorID
		*dest[1].(*bool) = r.t.deleted
		if len(dest) > 2 {
			*dest[2].(*bool) = r.t.scheduled
		}
		return nil
	case strings.Contains(r.sql, "FROM copyright_title_reviews WHERE post_id = $1 FOR UPDATE"),
		strings.Contains(r.sql, "WHERE post_id = $1 AND author_id = $2 FOR UPDATE"):
		if r.t.existing == nil {
			return pgx.ErrNoRows
		}
		src := titleReviewDest(r.t.existing)
		for i := range dest {
			copyScan(dest[i], src[i])
		}
		return nil
	case strings.Contains(r.sql, "FROM media_assets ma JOIN post_media pm") && strings.Contains(r.sql, "/ 1000"):
		*dest[0].(*int) = 3600
		return nil
	case strings.Contains(r.sql, "SET search_rev = search_rev + 1"):
		r.t.rev++
		*dest[0].(*uuid.UUID) = uuid.New()
		*dest[1].(*string) = "public"
		*dest[2].(*string) = "approved"
		*dest[3].(*string) = "body"
		*dest[4].(*string) = "Jailer 2"
		*dest[5].(*string) = "post" // not a long video: no credit read in this fake
		*dest[6].(*time.Time) = time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
		*dest[7].(**time.Time) = nil
		*dest[8].(**time.Time) = nil
		*dest[9].(*int64) = r.t.rev
		*dest[10].(*string) = r.t.effective
		restricted := 0
		if r.t.effective == ReviewStatusRestricted {
			restricted = 1
		}
		*dest[11].(*int) = restricted
		*dest[12].(*[]string) = nil
		*dest[13].(*[]string) = nil
		*dest[14].(*int) = 0
		*dest[15].(*int) = 0
		*dest[16].(**uuid.UUID) = nil
		*dest[17].(*bool) = r.t.tracked
		return nil
	case strings.Contains(r.sql, "FROM copyright_title_reviews r JOIN posts p"):
		state := r.t.state
		if state == "" {
			state = TitleReviewPending
		}
		*dest[0].(*uuid.UUID) = uuid.New()
		*dest[1].(*uuid.UUID) = uuid.New()
		*dest[2].(*string) = state
		*dest[3].(*string) = "Jailer 2"
		*dest[4].(*string) = "Jailer 2 full movie"
		*dest[5].(*string) = "long_video"
		*dest[6].(*time.Time) = time.Now()
		*dest[7].(**time.Time) = nil
		*dest[8].(**uuid.UUID) = nil
		*dest[9].(*string) = ""
		if r.t.owner != nil {
			*dest[9].(*string) = *r.t.owner
		}
		*dest[10].(*string) = ""
		if r.t.reason != nil {
			*dest[10].(*string) = *r.t.reason
		}
		*dest[11].(*bool) = r.t.appealed
		return nil
	}
	return fmt.Errorf("unexpected query: %s", r.sql)
}

// copyScan copies *src into *dst for the scan destinations of TitleReview.
func copyScan(dst, src any) {
	switch d := dst.(type) {
	case *uuid.UUID:
		*d = *src.(*uuid.UUID)
	case *string:
		*d = *src.(*string)
	case *bool:
		*d = *src.(*bool)
	case **int:
		*d = *src.(**int)
	case **string:
		*d = *src.(**string)
	case **time.Time:
		*d = *src.(**time.Time)
	case **uuid.UUID:
		*d = *src.(**uuid.UUID)
	case *time.Time:
		*d = *src.(*time.Time)
	default:
		panic(fmt.Sprintf("copyScan: %T", dst))
	}
}

func testHold(awaiting bool) *TitleReviewHold {
	h := &TitleReviewHold{ReviewID: uuid.New(), TitleID: uuid.New(), MatchedTitle: "Jailer 2", MatchedTerm: "Jailer 2",
		MatchedField: "title", AnnounceOnClear: true}
	if awaiting {
		h.AwaitingDuration = true
	} else {
		d := 3600
		h.DurationSeconds = &d
	}
	return h
}

func execIndex(execs []string, needle string) int {
	for i, e := range execs {
		if strings.Contains(e, needle) {
			return i
		}
	}
	return -1
}

// The hold places a title_review restriction issued by post-service,
// recounts, and emits the eligibility change as RESTRICTED — the post
// leaves search the way a rejected one does — and the author's
// "in copyright review" event. No PostCreated: that is withheld.
func TestTitleReviewHold_PlacesRestrictionAndEmitsRestricted(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted}
	placed, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), testHold(false))
	if err != nil || !placed {
		t.Fatalf("placed=%v err=%v", placed, err)
	}
	ri := execIndex(tx.execs, "INSERT INTO post_restrictions")
	ci := execIndex(tx.execs, "SET active_restriction_count")
	if ri < 0 || ci < ri {
		t.Fatalf("restriction then recount, got %v", tx.execs)
	}
	if !strings.Contains(tx.execs[ri], "'active'") {
		t.Fatalf("the restriction is not placed active: %s", tx.execs[ri])
	}
	if len(tx.eligibility) != 1 || tx.eligibility[0].ReviewStatus != ReviewStatusRestricted || !tx.eligibility[0].Restricted {
		t.Fatalf("eligibility events = %+v, want one restricted", tx.eligibility)
	}
	if tx.eligibility[0].Text != "" || tx.eligibility[0].Title != "" {
		t.Fatal("a held post's body rode the eligibility event")
	}
	for _, typ := range tx.outbox {
		if typ == events.PostCreated {
			t.Fatal("a held post wrote PostCreated")
		}
	}
	if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewPending {
		t.Fatalf("review events = %+v, want one pending", tx.reviewEvents)
	}
}

// A hold still waiting for the video's length tells nobody yet.
func TestTitleReviewHold_AwaitingDurationIsSilent(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted}
	if _, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), testHold(true)); err != nil {
		t.Fatal(err)
	}
	if len(tx.reviewEvents) != 0 {
		t.Fatalf("an unmeasured hold notified the author: %+v", tx.reviewEvents)
	}
	if len(tx.eligibility) != 1 {
		t.Fatal("an unmeasured hold still leaves search")
	}
}

// One review per post: a second hold attempt writes nothing else.
func TestTitleReviewHold_OnePerPost(t *testing.T) {
	tx := &titleTx{reviewInserted: 0}
	placed, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), testHold(false))
	if err != nil || placed {
		t.Fatalf("placed=%v err=%v", placed, err)
	}
	if len(tx.execs) != 1 || len(tx.outbox) != 0 {
		t.Fatalf("a duplicate hold wrote more: %v", tx.execs)
	}
}

func TestTitleReviewHold_ShapeIsChecked(t *testing.T) {
	bad := testHold(false)
	bad.AwaitingDuration = true // with a duration: contradictory
	if _, err := insertTitleReviewHoldTx(context.Background(), &titleTx{}, uuid.New(), uuid.New(), bad); err == nil {
		t.Fatal("a contradictory hold was written")
	}
	field := testHold(false)
	field.MatchedField = "comments"
	if _, err := insertTitleReviewHoldTx(context.Background(), &titleTx{}, uuid.New(), uuid.New(), field); err == nil {
		t.Fatal("an unknown matched field was written")
	}
}

// A clear releases ONLY this review's restriction, recounts, emits the
// eligibility change and — once — the withheld PostCreated, marking the
// review announced in the same transaction.
func TestTitleReviewRelease_AnnouncesOnce(t *testing.T) {
	calls := 0
	announce := func(rev int64) (string, interface{}, error) {
		calls++
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	}
	r := &TitleReview{ID: uuid.New(), PostID: uuid.New(), RestrictionID: uuid.New(), State: TitleReviewPending, AnnounceOnClear: true}
	tx := &titleTx{effective: "approved"}
	announced, err := releaseTitleReviewTx(context.Background(), tx, r, titleReviewReasonCleared, announce)
	if err != nil || !announced || calls != 1 {
		t.Fatalf("announced=%v calls=%d err=%v", announced, calls, err)
	}
	ui := execIndex(tx.execs, "UPDATE post_restrictions")
	if ui < 0 || !strings.Contains(tx.execs[ui], "WHERE restriction_id = $1 AND source = $4 AND state = 'active'") {
		t.Fatalf("the release is not scoped to this review's active row: %v", tx.execs)
	}
	if execIndex(tx.execs, "SET active_restriction_count") < ui {
		t.Fatal("no recount after the release")
	}
	if got := strings.Join(tx.outbox, ","); got != events.PostSearchEligibilityChanged+","+events.PostCreated {
		t.Fatalf("outbox = %s, want the eligibility change then PostCreated", got)
	}
	if execIndex(tx.execs, "SET announced_at") < 0 {
		t.Fatal("the announcement is not recorded, so a second clear could fan out again")
	}

	// Already announced: the event is not written again.
	again := *r
	now := time.Now()
	again.AnnouncedAt = &now
	tx2 := &titleTx{effective: "approved"}
	if announced, err := releaseTitleReviewTx(context.Background(), tx2, &again, titleReviewReasonCleared, announce); err != nil || announced || calls != 1 {
		t.Fatalf("second announce: announced=%v calls=%d err=%v", announced, calls, err)
	}
	// A deleted post, or one whose publication was never withheld, is not announced.
	tx3 := &titleTx{effective: "approved", deleted: true}
	if announced, _ := releaseTitleReviewTx(context.Background(), tx3, r, titleReviewReasonCleared, announce); announced || calls != 1 {
		t.Fatal("a deleted post was announced")
	}
	// Still held by a copyright case: not visible, so not announced.
	if announced, _ := releaseTitleReviewTx(context.Background(), &titleTx{effective: ReviewStatusRestricted, stillHeld: true}, r, titleReviewReasonCleared, announce); announced || calls != 1 {
		t.Fatal("a post still held by another case was announced")
	}
	quiet := *r
	quiet.AnnounceOnClear = false
	if announced, _ := releaseTitleReviewTx(context.Background(), &titleTx{effective: "approved"}, &quiet, titleReviewReasonCleared, announce); announced || calls != 1 {
		t.Fatal("a post that was never withheld was announced")
	}
}

// No other writer can lift the hold: trust-safety's signed restriction
// command refuses the source before any statement runs.
func TestRestrictionCommandRefusesTheTitleReviewSource(t *testing.T) {
	cmd := RestrictionCommand{
		Action: RestrictionActionReleaseHold, Source: RestrictionSourceTitleReview, CaseID: uuid.New(), CaseRevision: 2,
		PostID: uuid.New(), SubjectAuthorID: uuid.New(), ExpectedState: RestrictionStateActive, DecisionID: uuid.New(),
		PolicyVersion: "copyright-v1", ReasonCode: "claim_withdrawn", ActorID: uuid.New(), ClaimsDigest: []byte{1},
	}
	if err := cmd.validate(); err == nil {
		t.Fatal("a trust-safety command may release a title_review hold")
	}
	cmd.Source = RestrictionSourceCopyright
	if err := cmd.validate(); err != nil {
		t.Fatalf("the copyright source is refused too: %v", err)
	}
}

func TestEditResultTier(t *testing.T) {
	tier, other := uuid.New(), uuid.New()
	gated := &Post{TierRequiredID: &tier}
	if got := editResultTier(gated, PostEditPatch{}); got == nil || *got != tier {
		t.Fatal("an untouched tier is not kept")
	}
	if editResultTier(gated, PostEditPatch{ClearTierRequired: true}) != nil {
		t.Fatal("clearing leaves a tier")
	}
	if got := editResultTier(&Post{}, PostEditPatch{TierRequiredID: &other}); got == nil || *got != other {
		t.Fatal("a new tier is not the result")
	}
}
