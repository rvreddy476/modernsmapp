package postgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Migration 076 inside its transactions, against the recording titleTx fake
// (copyright_title_review_test.go): the author's one appeal of a block, the
// owner match that must not re-block an appealed video, the announcement a
// scheduled post's owner hold owes, and the admin's removal of a track.

func blockedReview(decidedBy *uuid.UUID, reason *string) *TitleReview {
	at := time.Now().Add(-time.Hour)
	return &TitleReview{ID: uuid.New(), PostID: uuid.New(), AuthorID: uuid.New(), RestrictionID: uuid.New(),
		TitleID: uuid.New(), State: TitleReviewBlocked, AnnounceOnClear: true, DecidedAt: &at, DecidedBy: decidedBy,
		Reason: reason, CreatedAt: at}
}

// ── The appeal ──────────────────────────────────────────────────────────

// A blocked review — the system's for a registered owner, or a reviewer's —
// goes back to the reviewer queue on the author's request, and NOTHING about
// the hold changes: no restriction write, no recount, no eligibility event,
// no PostCreated. The video stays hidden until a reviewer clears it.
func TestAppeal_BlockedReviewGoesBackToAReviewerStillHidden(t *testing.T) {
	reviewer := uuid.New()
	ownerReason := TitleReviewReasonOwnerRegistered
	for name, r := range map[string]*TitleReview{
		"system owner_registered block": blockedReview(nil, &ownerReason),
		"reviewer block":                blockedReview(&reviewer, nil),
	} {
		t.Run(name, func(t *testing.T) {
			tx := &titleTx{existing: r, state: TitleReviewBlocked, reason: r.Reason}
			id, err := requestTitleReviewTx(context.Background(), tx, r.PostID, r.AuthorID, "I licensed this from the producer")
			if err != nil || id != r.ID {
				t.Fatalf("appeal: id=%v err=%v", id, err)
			}
			ai := execIndex(tx.execs, "SET state = 'pending', decided_by = NULL")
			if ai < 0 {
				t.Fatalf("no appeal write: %v", tx.execs)
			}
			for _, guard := range []string{
				"WHERE id = $1 AND state = 'blocked' AND appealed_at IS NULL",
				"pr.source = 'title_review' AND pr.state = 'active'",
				"appealed_at = now()",
			} {
				if !strings.Contains(tx.execs[ai], guard) {
					t.Fatalf("the appeal write lacks %q:\n%s", guard, tx.execs[ai])
				}
			}
			for _, forbidden := range []string{"UPDATE post_restrictions", "SET active_restriction_count", "SET announced_at"} {
				if execIndex(tx.execs, forbidden) >= 0 {
					t.Fatalf("the appeal touched the hold (%s): %v", forbidden, tx.execs)
				}
			}
			if count(tx.outbox, events.PostCreated) != 0 || count(tx.outbox, events.PostSearchEligibilityChanged) != 0 {
				t.Fatalf("the appeal made the video visible or announced it: %v", tx.outbox)
			}
			if len(tx.reviewEvents) != 1 || tx.reviewEvents[0].State != TitleReviewPending || !tx.reviewEvents[0].Appeal {
				t.Fatalf("events = %+v, want one pending event marked as an appeal", tx.reviewEvents)
			}
			if got := strings.Join(tx.historyActs, ","); got != "appeal" || tx.historyActors[0] == nil || *tx.historyActors[0] != r.AuthorID {
				t.Fatalf("history = %s actors=%v, want one appeal by the author", got, tx.historyActors)
			}
			if tx.historyNotes[0] != "" {
				t.Fatalf("the author's words were copied into the history: %q", tx.historyNotes[0])
			}
		})
	}
}

// One appeal per review: refused while it waits for a reviewer and after
// the reviewer answered; a cleared review is decided.
func TestAppeal_OnlyOnce(t *testing.T) {
	at := time.Now()
	waiting := blockedReview(nil, nil)
	waiting.State, waiting.DecidedAt, waiting.AppealedAt, waiting.AuthorRequestedAt = TitleReviewPending, nil, &at, &at
	answered := blockedReview(nil, nil)
	answered.AppealedAt, answered.AuthorRequestedAt = &at, &at
	cleared := blockedReview(nil, nil)
	cleared.State = TitleReviewCleared
	for name, c := range map[string]struct {
		r    *TitleReview
		want error
	}{
		"appeal waiting for a reviewer":   {waiting, ErrTitleReviewAppealed},
		"appeal answered with a re-block": {answered, ErrTitleReviewAppealed},
		"cleared":                         {cleared, ErrTitleReviewDecided},
	} {
		tx := &titleTx{existing: c.r}
		if _, err := requestTitleReviewTx(context.Background(), tx, c.r.PostID, c.r.AuthorID, "again"); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want %v", name, err, c.want)
		}
		if len(tx.execs) != 0 {
			t.Errorf("%s: a refused request wrote %v", name, tx.execs)
		}
	}
}

// The compare-and-set refuses a review that lost its active hold (or raced
// another appeal): nothing is announced to anyone.
func TestAppeal_RefusedWithoutAnActiveHold(t *testing.T) {
	r := blockedReview(nil, nil)
	tx := &titleTx{existing: r, appealMisses: true}
	if _, err := requestTitleReviewTx(context.Background(), tx, r.PostID, r.AuthorID, "mine"); err == nil {
		t.Fatal("an appeal was recorded on a review with no active hold")
	}
	if len(tx.reviewEvents) != 0 || len(tx.historyActs) != 0 {
		t.Fatalf("a refused appeal emitted %+v / %v", tx.reviewEvents, tx.historyActs)
	}
}

func TestAppeal_DeletedPostHasNothingToAppeal(t *testing.T) {
	r := blockedReview(nil, nil)
	tx := &titleTx{existing: r, deleted: true}
	if _, err := requestTitleReviewTx(context.Background(), tx, r.PostID, r.AuthorID, "mine"); !errors.Is(err, ErrTitleReviewNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
	if execIndex(tx.execs, "SET state = 'pending'") >= 0 {
		t.Fatal("a deleted post's block was reopened")
	}
}

// A pending review's request is still the note for the reviewer: no state
// change, no event.
func TestRequestReview_PendingIsStillANote(t *testing.T) {
	r := blockedReview(nil, nil)
	r.State, r.DecidedAt = TitleReviewPending, nil
	tx := &titleTx{existing: r}
	if _, err := requestTitleReviewTx(context.Background(), tx, r.PostID, r.AuthorID, "mine"); err != nil {
		t.Fatal(err)
	}
	if execIndex(tx.execs, "SET state = 'pending', decided_by") >= 0 || len(tx.reviewEvents) != 0 ||
		strings.Join(tx.historyActs, ",") != "author_request" {
		t.Fatalf("execs=%v events=%+v history=%v", tx.execs, tx.reviewEvents, tx.historyActs)
	}
}

// A provider's (repeated) block on a video whose author appealed records
// the match and leaves the decision to the reviewer.
func TestOwnerMatch_UnderAppealIsNotReblocked(t *testing.T) {
	at := time.Now()
	r := blockedReview(nil, nil)
	r.State, r.DecidedAt, r.AppealedAt, r.AuthorRequestedAt = TitleReviewPending, nil, &at, &at
	for _, policy := range []OwnerMatchPolicy{OwnerPolicyBlock, OwnerPolicyMute, OwnerPolicyReview} {
		tx := &titleTx{existing: r, authorID: r.AuthorID}
		out, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(policy))
		if err != nil || out.Outcome != OwnerMatchUnderAppeal || *out.ReviewID != r.ID {
			t.Fatalf("%s: out=%+v err=%v", policy, out, err)
		}
		if execIndex(tx.execs, "SET state = 'blocked'") >= 0 || execIndex(tx.execs, "UPDATE post_restrictions") >= 0 ||
			execIndex(tx.execs, "SET reason = COALESCE") >= 0 || len(tx.reviewEvents) != 0 {
			t.Fatalf("%s: an appealed review was changed: execs=%v events=%+v", policy, tx.execs, tx.reviewEvents)
		}
		if strings.Join(tx.historyActs, ",") != "owner_match" {
			t.Fatalf("%s: history = %v", policy, tx.historyActs)
		}
	}
}

// ── The announcement an owner hold owes ─────────────────────────────────

// An owner match on a SCHEDULED post holds it before any PostCreated went
// out: its review owes the announcement (announce_on_clear). On a live post
// it was announced at publication, so a clear must not announce it again.
func TestOwnerHold_OwesTheAnnouncementOnlyWhileScheduled(t *testing.T) {
	for _, c := range []struct {
		scheduled bool
		policy    OwnerMatchPolicy
	}{{true, OwnerPolicyMute}, {true, OwnerPolicyBlock}, {false, OwnerPolicyMute}, {false, OwnerPolicyBlock}} {
		tx := &titleTx{reviewInserted: 1, effective: ReviewStatusRestricted, authorID: uuid.New(), scheduled: c.scheduled}
		if _, err := applyOwnerMatchTx(context.Background(), tx, ownerInput(c.policy)); err != nil {
			t.Fatal(err)
		}
		if tx.insertedAnnounce != c.scheduled {
			t.Errorf("scheduled=%v %s: announce_on_clear=%v, want %v", c.scheduled, c.policy, tx.insertedAnnounce, c.scheduled)
		}
		// A scheduled post was never fanned out: no feed cache to drop.
		if want := map[bool]int{true: 0, false: 1}[c.scheduled]; len(tx.visibility) != want {
			t.Errorf("scheduled=%v %s: %d visibility events, want %d", c.scheduled, c.policy, len(tx.visibility), want)
		}
	}
}

// A clear of a post that is still scheduled announces nothing (the flip
// will, once); the same clear on the live post announces it once.
func TestTitleReviewRelease_ScheduledPostIsLeftToTheFlip(t *testing.T) {
	calls := 0
	announce := func(rev int64) (string, interface{}, error) {
		calls++
		return events.PostCreated, events.PostCreatedPayload{SearchRev: rev}, nil
	}
	r := &TitleReview{ID: uuid.New(), PostID: uuid.New(), RestrictionID: uuid.New(), State: TitleReviewPending, AnnounceOnClear: true}
	tx := &titleTx{effective: "approved", scheduled: true}
	announced, err := releaseTitleReviewTx(context.Background(), tx, r, titleReviewReasonCleared, announce)
	if err != nil || announced || calls != 0 || count(tx.outbox, events.PostCreated) != 0 || execIndex(tx.execs, "SET announced_at") >= 0 {
		t.Fatalf("scheduled: announced=%v calls=%d outbox=%v err=%v", announced, calls, tx.outbox, err)
	}
	tx = &titleTx{effective: "approved"}
	if announced, err := releaseTitleReviewTx(context.Background(), tx, r, titleReviewReasonCleared, announce); err != nil || !announced || calls != 1 {
		t.Fatalf("live: announced=%v calls=%d err=%v", announced, calls, err)
	}
}

// The flip withholds the PostCreated of a post an owner match holds (an
// active title_review restriction) even when the title rule did not match
// at the flip; with no such hold it publishes normally.
func TestScheduledFlip_WithholdsAnOwnerHeldPost(t *testing.T) {
	var rev int64 = 7
	held, err := scheduledFlipHeldTx(context.Background(), &titleTx{titleHoldActive: true}, uuid.New(), uuid.New(), nil, &rev)
	if err != nil || !held {
		t.Fatalf("owner-held scheduled post: held=%v err=%v", held, err)
	}
	held, err = scheduledFlipHeldTx(context.Background(), &titleTx{}, uuid.New(), uuid.New(), nil, &rev)
	if err != nil || held || rev != 7 {
		t.Fatalf("no hold: held=%v rev=%d err=%v", held, rev, err)
	}
	// A review already exists when the title rule matches at the flip: it decides.
	held, err = scheduledFlipHeldTx(context.Background(), &titleTx{reviewInserted: 0, stillHeld: true}, uuid.New(), uuid.New(), testHold(false), &rev)
	if err != nil || !held {
		t.Fatalf("existing review: held=%v err=%v", held, err)
	}
}

// ── Removing a track ────────────────────────────────────────────────────

func TestRemoveOwnerTrack_RestatesEligibilityAndAudits(t *testing.T) {
	post, title, actor := uuid.New(), uuid.New(), uuid.New()
	tx := &titleTx{effective: "approved", tracked: true}
	out, err := removeOwnerTrackTx(context.Background(), tx, post, title, actor, "licence shown")
	if err != nil || out.StillTracked || out.PostID != post || out.TitleID != title {
		t.Fatalf("out=%+v err=%v", out, err)
	}
	if len(tx.eligibility) != 1 || tx.eligibility[0].EarningsExcluded {
		t.Fatalf("eligibility = %+v, want one restatement with earnings no longer excluded", tx.eligibility)
	}
	if len(tx.audits) != 1 || tx.audits[0] != "copyright_track.remove" || !strings.Contains(tx.auditPrev[0], "title_id="+title.String()) ||
		!strings.Contains(tx.auditPrev[0], `reference="fp-123"`) {
		t.Fatalf("audit = %v %v", tx.audits, tx.auditPrev)
	}
	// Another owner still tracks it: the restatement keeps the exclusion.
	tx = &titleTx{effective: "approved", tracked: true, otherTracks: true}
	if out, err := removeOwnerTrackTx(context.Background(), tx, post, title, actor, ""); err != nil || !out.StillTracked ||
		len(tx.eligibility) != 1 || !tx.eligibility[0].EarningsExcluded {
		t.Fatalf("still tracked: out=%+v eligibility=%+v err=%v", out, tx.eligibility, err)
	}
}

func TestRemoveOwnerTrack_Refusals(t *testing.T) {
	for name, c := range map[string]struct {
		tx    *titleTx
		actor uuid.UUID
		want  error
	}{
		"no such track": {&titleTx{effective: "approved"}, uuid.New(), ErrOwnerTrackNotFound},
		"no such post":  {&titleTx{missingPost: true, tracked: true}, uuid.New(), ErrOwnerTrackNotFound},
		"no actor":      {&titleTx{tracked: true}, uuid.Nil, ErrAdminAuditActor},
	} {
		if _, err := removeOwnerTrackTx(context.Background(), c.tx, uuid.New(), uuid.New(), c.actor, ""); !errors.Is(err, c.want) {
			t.Errorf("%s: err=%v, want %v", name, err, c.want)
		}
		if len(c.tx.eligibility) != 0 || len(c.tx.audits) != 0 {
			t.Errorf("%s: a refused removal wrote eligibility=%v audits=%v", name, c.tx.eligibility, c.tx.audits)
		}
	}
}
