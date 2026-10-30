package postgres

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Owner-registered blocks and owner matches (migration 075) inside their
// transactions, against the recording titleTx fake
// (copyright_title_review_test.go). The same paths run on real Postgres in
// copyright_owner_match_integration_test.go.

func ownerHold(awaiting bool) *TitleReviewHold {
	h := testHold(awaiting)
	owner := "Sun Pictures"
	h.RightsOwner, h.AutoBlock = &owner, true
	return h
}

func count(xs []string, want string) int {
	n := 0
	for _, x := range xs {
		if x == want {
			n++
		}
	}
	return n
}

// A measured hold on an auto_block entry is BLOCKED in the publishing
// transaction by the system: the restriction says owner_registered, the
// review is blocked with no reviewer, the history row is the audit, and the
// ONE event the author and trust-safety see is "blocked" — never a
// "pending" that would wait for a reviewer.
func TestAutoBlockHold_DecidesBlockedAtOnce(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted}
	placed, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), ownerHold(false))
	if err != nil || !placed {
		t.Fatalf("placed=%v err=%v", placed, err)
	}
	if len(tx.reviewEvents) != 1 {
		t.Fatalf("review events = %+v, want exactly one", tx.reviewEvents)
	}
	ev := tx.reviewEvents[0]
	if ev.State != TitleReviewBlocked || ev.Reason != TitleReviewReasonOwnerRegistered || ev.DecidedBy != "" || ev.RightsOwner != "Sun Pictures" {
		t.Fatalf("event = %+v, want a system block for the registered owner", ev)
	}
	bi := execIndex(tx.execs, "SET state = 'blocked', decided_by = NULL")
	if bi < 0 || !strings.Contains(tx.execs[bi], "WHERE id = $1 AND state = 'pending'") {
		t.Fatalf("the system block is not a compare-and-set on the pending row: %v", tx.execs)
	}
	if len(tx.reasonCodes) != 1 || tx.reasonCodes[0] != "owner_registered" {
		t.Fatalf("restriction reasons = %v, want owner_registered", tx.reasonCodes)
	}
	if got := strings.Join(tx.historyActs, ","); got != "hold,auto_block" {
		t.Fatalf("history = %s, want hold then auto_block", got)
	}
	if tx.historyNotes[1] != TitleReviewReasonOwnerRegistered {
		t.Fatalf("auto_block history note = %q", tx.historyNotes[1])
	}
	// Still hidden: the eligibility change is restricted and no PostCreated.
	if len(tx.eligibility) != 1 || !tx.eligibility[0].Restricted || count(tx.outbox, events.PostCreated) != 0 {
		t.Fatalf("eligibility=%+v outbox=%v", tx.eligibility, tx.outbox)
	}
}

// An ordinary entry's hold still goes to a reviewer (one pending event, no
// block statement).
func TestOrdinaryHold_StillGoesToReview(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted}
	if _, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), testHold(false)); err != nil {
		t.Fatal(err)
	}
	if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewPending || execIndex(tx.execs, "SET state = 'blocked'") >= 0 {
		t.Fatalf("events=%+v execs=%v", tx.reviewEvents, tx.execs)
	}
}

// An auto_block hold still waiting for the video's length tells nobody and
// blocks nothing yet: the duration rule decides once it is measured.
func TestAutoBlockHold_AwaitingDurationWaits(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted}
	if _, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), ownerHold(true)); err != nil {
		t.Fatal(err)
	}
	if len(tx.reviewEvents) != 0 || execIndex(tx.execs, "SET state = 'blocked'") >= 0 {
		t.Fatalf("an unmeasured auto_block hold acted: events=%+v", tx.reviewEvents)
	}
}

// The system block refuses a review that is no longer pending.
func TestSystemBlock_RefusesADecidedReview(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted, blockMisses: true}
	if _, err := insertTitleReviewHoldTx(context.Background(), tx, uuid.New(), uuid.New(), ownerHold(false)); err == nil {
		t.Fatal("a block was recorded on a review that was not pending")
	}
}

func TestOwnerHoldShape(t *testing.T) {
	noOwner := ownerHold(false)
	noOwner.RightsOwner = nil
	blank := ownerHold(false)
	sp := "  "
	blank.RightsOwner = &sp
	muteBlock := ownerHold(false)
	muteBlock.Reason = TitleReviewReasonMuteRequested
	odd := testHold(false)
	odd.Reason = "because"
	for name, h := range map[string]*TitleReviewHold{
		"auto block without owner": noOwner, "auto block with blank owner": blank,
		"auto block with a hold reason": muteBlock, "unknown reason": odd,
	} {
		if err := h.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	ok := testHold(false)
	ok.MatchedField, ok.Reason = MatchedFieldOwnerMatch, TitleReviewReasonMuteRequested
	if err := ok.validate(); err != nil {
		t.Fatalf("a mute-requested owner-match hold was refused: %v", err)
	}
}

// ── ApplyOwnerMatch's transaction ───────────────────────────────────────

func ownerInput(policy OwnerMatchPolicy) OwnerMatchInput {
	owner := "Sun Pictures"
	return OwnerMatchInput{PostID: uuid.New(), TitleID: uuid.New(), MatchedTitle: "Jailer 2", RightsOwner: &owner,
		Reference: "fp-123", Policy: policy}
}

func TestOwnerMatch_BlockWithNoReview(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted, authorID: uuid.New()}
	out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyBlock))
	if err != nil || out.Outcome != OwnerMatchBlocked || out.ReviewID == nil {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewBlocked || tx.reviewEvents[0].Reason != TitleReviewReasonOwnerRegistered {
		t.Fatalf("events = %+v, want one system block", tx.reviewEvents)
	}
	if got := strings.Join(tx.historyActs, ","); got != "hold,auto_block,owner_match" || !strings.Contains(tx.historyNotes[2], "reference=fp-123") {
		t.Fatalf("history = %s %v", got, tx.historyNotes)
	}
	// 2026-10-30: the block drops the post from every viewer's feed cache.
	if len(tx.visibility) != 1 || tx.visibility[0].Reason != VisibilityReasonCopyright || tx.visibility[0].AuthorID != tx.authorID.String() {
		t.Fatalf("visibility events = %+v, want one copyright", tx.visibility)
	}
}

func TestOwnerMatch_MuteHoldsForReview(t *testing.T) {
	tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted, authorID: uuid.New()}
	out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyMute))
	if err != nil || out.Outcome != OwnerMatchHeld {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewPending || tx.reviewEvents[0].Reason != TitleReviewReasonMuteRequested {
		t.Fatalf("events = %+v, want one pending with mute_requested", tx.reviewEvents)
	}
	if execIndex(tx.execs, "SET state = 'blocked'") >= 0 {
		t.Fatal("a mute request blocked the video")
	}
	if len(tx.visibility) != 1 || tx.visibility[0].Reason != VisibilityReasonCopyright {
		t.Fatalf("a hold must drop the post from feed caches: %+v", tx.visibility)
	}
	// The kill switch's review policy: a plain hold, no reason.
	tx = &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted, authorID: uuid.New()}
	if out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyReview)); err != nil || out.Outcome != OwnerMatchHeld ||
		len(tx.reviewEvents) != 1 || tx.reviewEvents[0].Reason != "" || tx.reviewEvents[0].State != TitleReviewPending {
		t.Fatalf("review policy: out=%+v events=%+v err=%v", out, tx.reviewEvents, err)
	}
}

// Track hides nothing and tells nobody: one eligibility restatement that
// carries earnings_excluded, and nothing on a repeat.
func TestOwnerMatch_TrackExcludesEarningsOnly(t *testing.T) {
	tx := &titleTx{effective: "approved", authorID: uuid.New()}
	out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyTrack))
	if err != nil || out.Outcome != OwnerMatchTracked {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(tx.eligibility) != 1 || !tx.eligibility[0].EarningsExcluded || tx.eligibility[0].Restricted || tx.eligibility[0].ReviewStatus != "approved" {
		t.Fatalf("eligibility = %+v, want one visible, earnings-excluded restatement", tx.eligibility)
	}
	if len(tx.reviewEvents) != 0 || execIndex(tx.execs, "INSERT INTO post_restrictions") >= 0 || execIndex(tx.execs, "INSERT INTO copyright_title_reviews") >= 0 {
		t.Fatalf("a track held or notified: events=%+v execs=%v", tx.reviewEvents, tx.execs)
	}
	again, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyTrack))
	if err != nil || again.Outcome != OwnerMatchAlreadyTracked || len(tx.eligibility) != 1 {
		t.Fatalf("repeat: out=%+v eligibility=%d err=%v", again, len(tx.eligibility), err)
	}
}

// A pending review is blocked by an owner's block; a decided one is left
// as it is; a deleted post is not touched.
func TestOwnerMatch_ExistingReviews(t *testing.T) {
	pending := &TitleReview{ID: uuid.New(), PostID: uuid.New(), RestrictionID: uuid.New(), State: TitleReviewPending, CreatedAt: time.Now()}
	tx := &titleTx{effective: ReviewStatusRestricted, authorID: uuid.New(), existing: pending}
	out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyBlock))
	if err != nil || out.Outcome != OwnerMatchBlocked || *out.ReviewID != pending.ID {
		t.Fatalf("pending + block: out=%+v err=%v", out, err)
	}
	if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewBlocked || len(tx.reasonCodes) != 1 || tx.reasonCodes[0] != "owner_registered" {
		t.Fatalf("pending + block: events=%+v reasons=%v", tx.reviewEvents, tx.reasonCodes)
	}

	tx = &titleTx{effective: ReviewStatusRestricted, authorID: uuid.New(), existing: pending}
	if out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyMute)); err != nil || out.Outcome != OwnerMatchHeld ||
		len(tx.reviewEvents) != 0 || tx.reason == nil || *tx.reason != TitleReviewReasonMuteRequested {
		t.Fatalf("pending + mute: out=%+v events=%+v reason=%v err=%v", out, tx.reviewEvents, tx.reason, err)
	}

	for _, state := range []string{TitleReviewCleared, TitleReviewBlocked} {
		decided := *pending
		decided.State = state
		tx := &titleTx{authorID: uuid.New(), existing: &decided}
		out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyBlock))
		want := OwnerMatchReviewerCleared
		if state == TitleReviewBlocked {
			want = OwnerMatchAlreadyBlocked
		}
		if err != nil || out.Outcome != want || len(tx.execs) != 0 {
			t.Fatalf("%s + block: out=%+v execs=%v err=%v", state, out, tx.execs, err)
		}
	}

	tx = &titleTx{authorID: uuid.New(), deleted: true}
	if out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(OwnerPolicyBlock)); err != nil || out.Outcome != OwnerMatchPostDeleted || len(tx.execs) != 0 {
		t.Fatalf("deleted post: out=%+v execs=%v err=%v", out, tx.execs, err)
	}
}

func TestOwnerMatchInputValidation(t *testing.T) {
	noOwner := ownerInput(OwnerPolicyBlock)
	noOwner.RightsOwner = nil
	long := ownerInput(OwnerPolicyTrack)
	long.Reference = strings.Repeat("x", MaxOwnerMatchReference+1)
	bad := ownerInput("delete")
	for name, in := range map[string]OwnerMatchInput{"block without owner": noOwner, "long reference": long, "unknown policy": bad} {
		if err := in.validate(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	trackNoOwner := ownerInput(OwnerPolicyTrack)
	trackNoOwner.RightsOwner = nil
	if err := trackNoOwner.validate(); err != nil {
		t.Fatalf("a track without an owner name was refused: %v", err)
	}
}

// A cleared post that a rights owner tracks restates the exclusion AFTER
// its withheld PostCreated, so the ownership row that PostCreated creates
// never starts earning.
func TestTitleReviewRelease_TrackedPostRestatesExclusion(t *testing.T) {
	announce := func(rev int64) (string, interface{}, error) {
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	}
	r := &TitleReview{ID: uuid.New(), PostID: uuid.New(), RestrictionID: uuid.New(), State: TitleReviewPending, AnnounceOnClear: true}
	tx := &titleTx{effective: "approved", tracked: true}
	if announced, err := releaseTitleReviewTx(context.Background(), tx, r, titleReviewReasonCleared, announce); err != nil || !announced {
		t.Fatalf("announced=%v err=%v", announced, err)
	}
	want := events.PostSearchEligibilityChanged + "," + events.PostCreated + "," + events.PostSearchEligibilityChanged
	if got := strings.Join(tx.outbox, ","); got != want {
		t.Fatalf("outbox = %s, want %s", got, want)
	}
	last := tx.eligibility[len(tx.eligibility)-1]
	if !last.EarningsExcluded || last.SearchRev <= tx.eligibility[0].SearchRev {
		t.Fatalf("restatement = %+v, want a later revision with earnings excluded", last)
	}
	// Untracked: no restatement.
	tx = &titleTx{effective: "approved"}
	if _, err := releaseTitleReviewTx(context.Background(), tx, r, titleReviewReasonCleared, announce); err != nil || count(tx.outbox, events.PostSearchEligibilityChanged) != 1 {
		t.Fatalf("untracked: outbox=%v err=%v", tx.outbox, err)
	}
}
