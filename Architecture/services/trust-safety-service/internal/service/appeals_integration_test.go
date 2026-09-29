//go:build integration

// The appeal state machine against trust_safety_it_test (Copyright Match
// plan section 6.3, P-3; test matrix TM-2 a, b, d): the binding captured
// at submission, the on-read supersede check, the uphold-versus-overturn
// race, the copyright race, the in-flight replay and migration 012.
// post-service is a fake whose subject and answers each test controls.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/events"
	"github.com/atpost/trust-safety-service/database"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type overturnCall struct {
	AppealID uuid.UUID
	Reviewer uuid.UUID
	Revision int64
	Reason   string
}

// appealFakePost is the controllable post-service. subject is what
// GetSubject returns (a copy); overturnErr what OverturnAppeal returns;
// beforeOverturn runs inside OverturnAppeal (to flip the subject under a
// racing overturn); delay widens the race window.
type appealFakePost struct {
	mu             sync.Mutex
	subject        PostModerationSubject
	subjectErr     error
	overturnErr    error
	beforeOverturn func()
	delay          time.Duration
	calls          []overturnCall
}

func (f *appealFakePost) GetSubject(context.Context, uuid.UUID) (*PostModerationSubject, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.subjectErr != nil {
		return nil, f.subjectErr
	}
	s := f.subject
	return &s, nil
}

func (f *appealFakePost) OverturnAppeal(_ context.Context, appeal *postgres.ContentAppeal, reviewer uuid.UUID, revision int64, reason string) error {
	f.mu.Lock()
	f.calls = append(f.calls, overturnCall{AppealID: appeal.ID, Reviewer: reviewer, Revision: revision, Reason: reason})
	hook, delay, err := f.beforeOverturn, f.delay, f.overturnErr
	f.mu.Unlock()
	if hook != nil {
		hook()
	}
	if delay > 0 {
		time.Sleep(delay)
	}
	return err
}

func (f *appealFakePost) setSubject(s PostModerationSubject) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subject = s
}

func (f *appealFakePost) setOverturnErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overturnErr = err
}

func (f *appealFakePost) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

type appealRig struct {
	pool   *pgxpool.Pool
	svc    *Service
	extras *postgres.TrustExtrasStore
	post   *appealFakePost
	owner  uuid.UUID
	postID uuid.UUID
}

func newAppealRig(t *testing.T, subject PostModerationSubject) *appealRig {
	t.Helper()
	pool := openM7TrustDB(t)
	extras := postgres.NewExtrasStore(pool).WithEventsTopic("trust.test.v1")
	svc := New(postgres.New(pool), nil)
	svc.SetExtrasStore(extras)
	fake := &appealFakePost{subject: subject}
	svc.SetPostModerationClient(fake)
	return &appealRig{pool: pool, svc: svc, extras: extras, post: fake, owner: subject.AuthorID, postID: subject.PostID}
}

func rejectedSubject(decision *uuid.UUID, revision int64) PostModerationSubject {
	return PostModerationSubject{PostID: uuid.New(), AuthorID: uuid.New(), ReviewStatus: "rejected", ContentRevision: revision, LastDecisionID: decision, LastDecisionSource: "admin"}
}

func (rg *appealRig) submit(t *testing.T) *postgres.ContentAppeal {
	t.Helper()
	appeal, err := rg.svc.SubmitAppeal(context.Background(), rg.owner, "post", rg.postID.String(), "please look again", nil)
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	return appeal
}

func (rg *appealRig) row(t *testing.T, id uuid.UUID) *postgres.ContentAppeal {
	t.Helper()
	a, err := rg.extras.GetAppeal(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func appealOutbox(t *testing.T, pool *pgxpool.Pool, appealID uuid.UUID) []postgres.AppealResolvedPayload {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT event_type, payload FROM trust.enforcement_outbox
		WHERE payload->'payload'->>'appeal_id' = $1 ORDER BY id`, appealID.String())
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []postgres.AppealResolvedPayload
	for rows.Next() {
		var eventType string
		var raw json.RawMessage
		if err := rows.Scan(&eventType, &raw); err != nil {
			t.Fatal(err)
		}
		var env events.EventEnvelope
		if err := json.Unmarshal(raw, &env); err != nil || env.EventType != postgres.AppealResolvedEvent || eventType != postgres.AppealResolvedEvent || env.EventID == "" {
			t.Fatalf("envelope %s: %v", raw, err)
		}
		var p postgres.AppealResolvedPayload
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		out = append(out, p)
	}
	return out
}

// ── Submission ─────────────────────────────────────────────────────────────

func TestAppealSubmit_BindsDecisionAndRevision(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	stored := rg.row(t, appeal.ID)
	if stored.Status != postgres.AppealOpen || stored.ActionTaken != "rejected" ||
		stored.AppealedDecisionID == nil || *stored.AppealedDecisionID != d1 ||
		stored.AppealedRevision == nil || *stored.AppealedRevision != 7 ||
		stored.OverturnDecisionID != nil || stored.OverturnStartedAt != nil || stored.OverturnAppliedAt != nil {
		t.Fatalf("stored=%+v", stored)
	}
	// One active appeal per post: the second is refused whatever id it names.
	if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, "post", rg.postID.String(), "again", &d1); !errors.Is(err, postgres.ErrActiveAppealExists) {
		t.Fatalf("second submit: %v", err)
	}

	// The refusals, each on its own post so the dedup index stays out of it.
	refusals := []struct {
		name    string
		subject func() PostModerationSubject
		named   *uuid.UUID
		want    error
	}{
		{"copyright decision", func() PostModerationSubject {
			s := rejectedSubject(&d1, 3)
			s.LastDecisionSource = "copyright"
			return s
		}, nil, ErrAppealCopyrightCase},
		{"named decision stale", func() PostModerationSubject { return rejectedSubject(&d1, 3) }, ptrUUID(uuid.New()), ErrAppealDecisionStale},
		{"approved base", func() PostModerationSubject { s := rejectedSubject(&d1, 3); s.ReviewStatus = "approved"; return s }, nil, ErrAppealNotEligible},
		{"deleted", func() PostModerationSubject { s := rejectedSubject(&d1, 3); s.Deleted = true; return s }, nil, ErrAppealNotEligible},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.subject()
			rg.post.setSubject(s)
			if _, err := rg.svc.SubmitAppeal(ctx, s.AuthorID, "post", s.PostID.String(), "please", tc.named); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
	// Request shape: 422s, before post-service is asked.
	rg.post.mu.Lock()
	rg.post.subjectErr = errors.New("must not be called")
	rg.post.mu.Unlock()
	for _, tc := range []struct{ ctype, cid, reason string }{
		{"comment", uuid.NewString(), "r"}, {"post", "nope", "r"}, {"post", uuid.NewString(), "   "}, {"post", uuid.NewString(), strings.Repeat("x", 2001)},
	} {
		if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, tc.ctype, tc.cid, tc.reason, nil); !errors.Is(err, ErrAppealInvalid) {
			t.Fatalf("%+v: got %v, want ErrAppealInvalid", tc, err)
		}
	}
	// post-service down: unavailable, not "not appealable".
	if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, "post", uuid.NewString(), "please", nil); !errors.Is(err, ErrAppealsUnavailable) {
		t.Fatalf("outage: %v", err)
	}
	rg.post.mu.Lock()
	rg.post.subjectErr = ErrPostSubjectNotFound
	rg.post.mu.Unlock()
	if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, "post", uuid.NewString(), "please", nil); !errors.Is(err, ErrAppealNotEligible) {
		t.Fatalf("unknown post: %v", err)
	}
}

// ── Adjudication: overturn happy path, replay and the outbox ──────────────

func TestAppealOverturn_SignsCapturedRevisionAndRecordsOutcomeOnce(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	reviewer := uuid.New()
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "under_review", "", adminMeta(reviewer, "")); err != nil {
		t.Fatal(err)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "  fair use  ", adminMeta(reviewer, "fair use")); err != nil {
		t.Fatal(err)
	}
	if n := rg.post.callCount(); n != 1 {
		t.Fatalf("canonical calls=%d", n)
	}
	call := rg.post.calls[0]
	if call.AppealID != appeal.ID || call.Reviewer != reviewer || call.Revision != 7 || call.Reason != "Appeal overturned: fair use" {
		t.Fatalf("call=%+v", call)
	}
	stored := rg.row(t, appeal.ID)
	if stored.Status != postgres.AppealOverturned || stored.OverturnDecisionID == nil || *stored.OverturnDecisionID != appeal.ID ||
		stored.OverturnStartedAt == nil || stored.OverturnAppliedAt == nil || stored.ResolvedAt == nil ||
		stored.ReviewedBy == nil || *stored.ReviewedBy != reviewer || stored.ResolutionNote == nil || *stored.ResolutionNote != "fair use" {
		t.Fatalf("stored=%+v", stored)
	}
	// Replays: a repeated overturn is the same decision (no second call),
	// and an uphold after the fact is refused.
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "again", adminMeta(uuid.New(), "")); err != nil {
		t.Fatalf("replay: %v", err)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealTransition) {
		t.Fatalf("uphold after overturn: %v", err)
	}
	if n := rg.post.callCount(); n != 1 {
		t.Fatalf("canonical calls after replays=%d", n)
	}
	// Audit: under_review, overturning, overturned; outbox: one resolved event.
	var actions []string
	for _, row := range auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID) {
		actions = append(actions, row.Action)
	}
	if strings.Join(actions, ",") != "appeal.under_review,appeal.overturning,appeal.overturned" {
		t.Fatalf("audit actions=%v", actions)
	}
	ob := appealOutbox(t, rg.pool, appeal.ID)
	if len(ob) != 1 || ob[0].Outcome != postgres.AppealOverturned || ob[0].UserID != rg.owner.String() || ob[0].ContentID != rg.postID.String() ||
		ob[0].AppealedDecisionID == nil || *ob[0].AppealedDecisionID != d1.String() || ob[0].OverturnDecisionID == nil || *ob[0].OverturnDecisionID != appeal.ID.String() ||
		ob[0].ReviewedBy == nil || *ob[0].ReviewedBy != reviewer.String() || ob[0].ResolvedAt.IsZero() {
		t.Fatalf("outbox=%+v", ob)
	}
	if rows := outboxRowsFor(t, rg.pool, rg.owner.String()); len(rows) != 1 || rows[0].Topic != "trust.test.v1" || rows[0].Published {
		t.Fatalf("outbox rows=%+v", rows)
	}
}

func TestAppealUphold_ResolvedOutboxedAndFinal(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	reviewer := uuid.New()
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "policy stands", adminMeta(reviewer, "policy stands")); err != nil {
		t.Fatal(err)
	}
	stored := rg.row(t, appeal.ID)
	if stored.Status != postgres.AppealUpheld || stored.ResolvedAt == nil || stored.ReviewedBy == nil || *stored.ReviewedBy != reviewer || stored.OverturnDecisionID != nil {
		t.Fatalf("stored=%+v", stored)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealTransition) {
		t.Fatalf("overturn after uphold: %v", err)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "under_review", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealTransition) {
		t.Fatalf("reopen after uphold: %v", err)
	}
	if rg.post.callCount() != 0 {
		t.Fatal("an uphold must never reach post-service")
	}
	if ob := appealOutbox(t, rg.pool, appeal.ID); len(ob) != 1 || ob[0].Outcome != postgres.AppealUpheld {
		t.Fatalf("outbox=%+v", ob)
	}
	if err := rg.svc.ReviewAppeal(ctx, uuid.New(), "upheld", "", adminMeta(reviewer, "")); !errors.Is(err, ErrAppealNotFound) {
		t.Fatalf("unknown appeal: %v", err)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "expired", "", adminMeta(reviewer, "")); !errors.Is(err, ErrAppealInvalid) {
		t.Fatalf("bad status: %v", err)
	}
}

// ── The uphold-versus-overturn race (TM-2 b) ───────────────────────────────

// Adjudicators race on one appeal: exactly one terminal state, and the post
// is approved (one canonical call) if and only if the appeal is overturned.
func TestAppealRace_UpholdVersusOverturn_OneWinnerAndThePostMatches(t *testing.T) {
	for round := 0; round < 6; round++ {
		d1 := uuid.New()
		rg := newAppealRig(t, rejectedSubject(&d1, 7))
		rg.post.delay = 15 * time.Millisecond
		ctx := context.Background()
		appeal := rg.submit(t)
		const workers = 16
		var wg sync.WaitGroup
		var mu sync.Mutex
		outcomes := map[string]int{}
		for i := 0; i < workers; i++ {
			status := "upheld"
			if i%2 == 1 {
				status = "overturned"
			}
			wg.Add(1)
			go func(status string) {
				defer wg.Done()
				err := rg.svc.ReviewAppeal(ctx, appeal.ID, status, "race", adminMeta(uuid.New(), "race"))
				mu.Lock()
				defer mu.Unlock()
				switch {
				case err == nil:
					outcomes[status+":ok"]++
				case errors.Is(err, ErrAppealTransition):
					outcomes[status+":refused"]++
				case errors.Is(err, ErrAppealOverturnInFlight):
					// A second overturner inside the grace: the first one's
					// approve is running, so this one makes no call.
					outcomes[status+":in_flight"]++
				default:
					outcomes["unexpected:"+err.Error()]++
				}
			}(status)
		}
		wg.Wait()
		for k := range outcomes {
			if strings.HasPrefix(k, "unexpected:") {
				t.Fatalf("round %d: %v", round, outcomes)
			}
		}
		stored := rg.row(t, appeal.ID)
		calls := rg.post.callCount()
		switch stored.Status {
		case postgres.AppealUpheld:
			if calls != 0 || outcomes["overturned:ok"] != 0 {
				t.Fatalf("round %d: upheld but post approved (calls=%d outcomes=%v)", round, calls, outcomes)
			}
		case postgres.AppealOverturned:
			if calls != 1 || outcomes["upheld:ok"] != 0 {
				t.Fatalf("round %d: overturned with calls=%d outcomes=%v", round, calls, outcomes)
			}
		default:
			t.Fatalf("round %d: status=%s outcomes=%v", round, stored.Status, outcomes)
		}
		// Every uphold that returned nil is either THE uphold or a replay of
		// it; every overturn likewise. Never one of each.
		if outcomes["upheld:ok"] > 0 && outcomes["overturned:ok"] > 0 {
			t.Fatalf("round %d: both outcomes reported success: %v", round, outcomes)
		}
		if ob := appealOutbox(t, rg.pool, appeal.ID); len(ob) != 1 || ob[0].Outcome != stored.Status {
			t.Fatalf("round %d: outbox=%+v status=%s", round, ob, stored.Status)
		}
		var terminalAudits int
		for _, row := range auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID) {
			if row.NewStatus != nil && postgres.AppealTerminal(*row.NewStatus) {
				terminalAudits++
			}
		}
		if terminalAudits != 1 {
			t.Fatalf("round %d: terminal audit rows=%d", round, terminalAudits)
		}
	}
}

// ── A pre-existing appeal racing a copyright-style decision (TM-2 a) ──────

func TestAppealRace_LaterDecisionSupersedes_NeverOverturns(t *testing.T) {
	ctx := context.Background()
	t.Run("copyright decision lands before adjudication", func(t *testing.T) {
		d1, d2 := uuid.New(), uuid.New()
		rg := newAppealRig(t, rejectedSubject(&d1, 7))
		appeal := rg.submit(t)
		later := rg.post.subject
		later.LastDecisionID, later.LastDecisionSource, later.ContentRevision = &d2, "copyright", 8
		rg.post.setSubject(later)
		err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "looks fine", adminMeta(uuid.New(), ""))
		if !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("overturn: %v", err)
		}
		stored := rg.row(t, appeal.ID)
		if stored.Status != postgres.AppealSuperseded || stored.ResolvedAt == nil || stored.OverturnDecisionID != nil || rg.post.callCount() != 0 {
			t.Fatalf("stored=%+v calls=%d", stored, rg.post.callCount())
		}
		if ob := appealOutbox(t, rg.pool, appeal.ID); len(ob) != 1 || ob[0].Outcome != postgres.AppealSuperseded {
			t.Fatalf("outbox=%+v", ob)
		}
		// Closed for good: neither outcome can be written afterwards, and a
		// second attempt says superseded rather than a bare transition error.
		if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("overturn after supersede: %v", err)
		}
		if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("uphold after supersede: %v", err)
		}
		// The author may appeal the new decision: the old one no longer
		// blocks the dedup index.
		if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, "post", rg.postID.String(), "new appeal", &d2); !errors.Is(err, ErrAppealCopyrightCase) {
			t.Fatalf("appeal against the copyright decision: %v", err)
		}
	})

	t.Run("uphold also observes the later decision", func(t *testing.T) {
		d1, d2 := uuid.New(), uuid.New()
		rg := newAppealRig(t, rejectedSubject(&d1, 7))
		appeal := rg.submit(t)
		later := rg.post.subject
		later.LastDecisionID = &d2
		rg.post.setSubject(later)
		if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("uphold: %v", err)
		}
		if rg.row(t, appeal.ID).Status != postgres.AppealSuperseded {
			t.Fatal("uphold did not close the appeal as superseded")
		}
	})

	t.Run("no ids exposed: the base status is the decision", func(t *testing.T) {
		rg := newAppealRig(t, rejectedSubject(nil, 7))
		appeal := rg.submit(t)
		if a := rg.row(t, appeal.ID); a.AppealedDecisionID != nil || a.AppealedRevision == nil || *a.AppealedRevision != 7 {
			t.Fatalf("binding without ids=%+v", a)
		}
		approved := rg.post.subject
		approved.ReviewStatus, approved.ContentRevision = "approved", 8
		rg.post.setSubject(approved)
		if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("overturn: %v", err)
		}
		if rg.row(t, appeal.ID).Status != postgres.AppealSuperseded || rg.post.callCount() != 0 {
			t.Fatal("appeal must close superseded without a canonical call")
		}
	})

	t.Run("decision lands inside the race window: post-service's fence wins", func(t *testing.T) {
		d1 := uuid.New()
		rg := newAppealRig(t, rejectedSubject(&d1, 7))
		appeal := rg.submit(t)
		// The pre-check passes (subject unchanged); post-service then refuses
		// the captured revision because a copyright decision beat the approve.
		rg.post.setOverturnErr(ErrPostDecisionSuperseded)
		if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
			t.Fatalf("overturn: %v", err)
		}
		stored := rg.row(t, appeal.ID)
		if stored.Status != postgres.AppealSuperseded || stored.OverturnAppliedAt != nil || stored.OverturnDecisionID == nil || rg.post.callCount() != 1 {
			t.Fatalf("stored=%+v calls=%d", stored, rg.post.callCount())
		}
		var actions []string
		for _, row := range auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID) {
			actions = append(actions, row.Action)
		}
		if strings.Join(actions, ",") != "appeal.overturning,appeal.superseded" {
			t.Fatalf("audit=%v", actions)
		}
	})
}

// ── Revision moved under the same decision: refused, stays open ───────────

func TestAppealOverturn_RevisionMovedUnderSameDecision_RefusedStaysOpen(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	edited := rg.post.subject
	edited.ContentRevision = 9
	rg.post.setSubject(edited)
	reviewer := uuid.New()
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "ok", adminMeta(reviewer, "ok")); !errors.Is(err, ErrAppealSubjectChanged) {
		t.Fatalf("overturn: %v", err)
	}
	stored := rg.row(t, appeal.ID)
	if stored.Status != postgres.AppealOpen || stored.OverturnDecisionID != nil || stored.ReviewedBy != nil || rg.post.callCount() != 0 {
		t.Fatalf("stored=%+v calls=%d", stored, rg.post.callCount())
	}
	rows := auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID)
	if len(rows) != 1 || rows[0].Action != postgres.AuditAppealOverturnRefused || rows[0].NewResolution == nil ||
		!strings.Contains(*rows[0].NewResolution, "revision 7 to 9") || rows[0].ActorUserID == nil || *rows[0].ActorUserID != reviewer {
		t.Fatalf("audit=%+v", rows)
	}
	if len(appealOutbox(t, rg.pool, appeal.ID)) != 0 {
		t.Fatal("a refusal is not an outcome")
	}
	// The appeal is still live: it can be upheld (the rejection stands on
	// the edited post too).
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "", adminMeta(reviewer, "")); err != nil {
		t.Fatal(err)
	}
}

// ── Transient failure: in flight, then replayed with the same claims ──────

func TestAppealOverturn_TransientFailureStaysInFlight_SweeperReplaysSameClaims(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	reviewer := uuid.New()
	rg.post.setOverturnErr(errors.New("dial tcp: connection refused"))
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "restore", adminMeta(reviewer, "restore")); !errors.Is(err, ErrAppealOverturnPending) {
		t.Fatalf("overturn: %v", err)
	}
	inflight := rg.row(t, appeal.ID)
	if inflight.Status != postgres.AppealOverturning || inflight.ReviewedBy == nil || *inflight.ReviewedBy != reviewer ||
		inflight.OverturnDecisionID == nil || *inflight.OverturnDecisionID != appeal.ID || inflight.OverturnStartedAt == nil || inflight.OverturnAppliedAt != nil {
		t.Fatalf("in flight=%+v", inflight)
	}
	// In flight: it cannot be upheld, and a second author appeal is blocked.
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "upheld", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealTransition) {
		t.Fatalf("uphold while overturning: %v", err)
	}
	if _, err := rg.svc.SubmitAppeal(ctx, rg.owner, "post", rg.postID.String(), "again", nil); !errors.Is(err, postgres.ErrActiveAppealExists) {
		t.Fatalf("second appeal while overturning: %v", err)
	}
	// The sweeper leaves a young in-flight overturn to the request that
	// started it.
	if n, err := rg.svc.ResumeOverturningAppeals(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("young sweep: n=%d err=%v", n, err)
	}
	// Still failing: the sweep logs and moves on.
	if n, err := rg.svc.ResumeOverturningAppeals(ctx, 0); err != nil || n != 0 || rg.row(t, appeal.ID).Status != postgres.AppealOverturning {
		t.Fatalf("failing sweep: n=%d err=%v", n, err)
	}
	// post-service is back: the sweep replays the SAME claims (the original
	// reviewer, note and captured revision), never the sweeper's identity.
	rg.post.setOverturnErr(nil)
	if n, err := rg.svc.ResumeOverturningAppeals(ctx, 0); err != nil || n != 1 {
		t.Fatalf("sweep: n=%d err=%v", n, err)
	}
	calls := rg.post.calls
	if len(calls) != 3 {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, c := range calls {
		if c.AppealID != appeal.ID || c.Reviewer != reviewer || c.Revision != 7 || c.Reason != "Appeal overturned: restore" {
			t.Fatalf("call %d=%+v", i, c)
		}
	}
	done := rg.row(t, appeal.ID)
	if done.Status != postgres.AppealOverturned || done.OverturnAppliedAt == nil || done.ReviewedBy == nil || *done.ReviewedBy != reviewer {
		t.Fatalf("done=%+v", done)
	}
	rows := auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID)
	last := rows[len(rows)-1]
	if last.Action != "appeal.overturned" || last.ActorType != "service" || last.ActorService == nil || *last.ActorService != SweeperActor {
		t.Fatalf("finish audit=%+v", last)
	}
	if ob := appealOutbox(t, rg.pool, appeal.ID); len(ob) != 1 || ob[0].Outcome != postgres.AppealOverturned {
		t.Fatalf("outbox=%+v", ob)
	}
	// Nothing left to sweep, and the admin's own retry is a no-op.
	if n, _ := rg.svc.ResumeOverturningAppeals(ctx, 0); n != 0 {
		t.Fatalf("second sweep n=%d", n)
	}
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "", adminMeta(reviewer, "")); err != nil || len(rg.post.calls) != 3 {
		t.Fatalf("retry after completion: err=%v calls=%d", err, len(rg.post.calls))
	}
}

func TestAppealOverturn_AdminRetryReplaysInFlightApprove(t *testing.T) {
	d1 := uuid.New()
	rg := newAppealRig(t, rejectedSubject(&d1, 7))
	ctx := context.Background()
	appeal := rg.submit(t)
	reviewer := uuid.New()
	rg.post.setOverturnErr(errors.New("timeout"))
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "first note", adminMeta(reviewer, "")); !errors.Is(err, ErrAppealOverturnPending) {
		t.Fatalf("overturn: %v", err)
	}
	// Inside the replay grace the first approve is presumed running: a
	// second adjudicator is refused and makes no call.
	rg.post.setOverturnErr(nil)
	other := uuid.New()
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "second note", adminMeta(other, "")); !errors.Is(err, ErrAppealOverturnInFlight) {
		t.Fatalf("retry inside the grace: %v", err)
	}
	if rg.post.callCount() != 1 || rg.row(t, appeal.ID).Status != postgres.AppealOverturning {
		t.Fatal("a retry inside the grace must not call post-service")
	}
	// After the grace a different admin retries with a different note: the
	// claims sent are still the first reviewer's and the first note
	// (post-service compares them under the decision id), and the note on
	// the row stays the one the claims were built from; the retrying
	// admin's note is in the audit row.
	rg.svc.SetOverturnReplayGrace(0)
	if err := rg.svc.ReviewAppeal(ctx, appeal.ID, "overturned", "second note", adminMeta(other, "second note")); err != nil {
		t.Fatal(err)
	}
	c := rg.post.calls[len(rg.post.calls)-1]
	if c.Reviewer != reviewer || c.Reason != "Appeal overturned: first note" || c.Revision != 7 {
		t.Fatalf("replayed call=%+v", c)
	}
	done := rg.row(t, appeal.ID)
	if done.Status != postgres.AppealOverturned || *done.ReviewedBy != reviewer || *done.ResolutionNote != "first note" {
		t.Fatalf("done=%+v", done)
	}
	rows := auditRows(t, postgres.New(rg.pool), postgres.AuditTargetAppeal, appeal.ID)
	if last := rows[len(rows)-1]; last.Action != "appeal.overturned" || last.ActorUserID == nil || *last.ActorUserID != other || last.Reason == nil || *last.Reason != "second note" {
		t.Fatalf("finish audit=%+v", last)
	}
	// DECISION_CONFLICT is not transient and not superseded: the appeal
	// stays in flight for a person to look at.
	rg2 := newAppealRig(t, rejectedSubject(&d1, 7))
	appeal2 := rg2.submit(t)
	rg2.post.setOverturnErr(ErrPostDecisionConflict)
	if err := rg2.svc.ReviewAppeal(ctx, appeal2.ID, "overturned", "", adminMeta(reviewer, "")); !errors.Is(err, ErrPostDecisionConflict) {
		t.Fatalf("conflict: %v", err)
	}
	if rg2.row(t, appeal2.ID).Status != postgres.AppealOverturning {
		t.Fatal("a decision conflict must leave the appeal in flight")
	}
}

// ── Appeals that pre-date the binding (migration 012 rows) ────────────────

func TestAppealLegacyRow_BoundAtFirstAdjudication(t *testing.T) {
	rg := newAppealRig(t, rejectedSubject(nil, 5))
	ctx := context.Background()
	insertLegacy := func(t *testing.T, owner, post uuid.UUID) uuid.UUID {
		t.Helper()
		id := uuid.New()
		if _, err := rg.pool.Exec(ctx, `INSERT INTO trust.content_appeals (id, user_id, content_type, content_id, action_taken, appeal_reason, status)
			VALUES ($1, $2, 'post', $3, 'rejected', 'legacy', 'open')`, id, owner, post); err != nil {
			t.Fatal(err)
		}
		return id
	}
	// Base status still the one appealed: bound now to the revision read
	// under the lock, then overturned with it.
	id := insertLegacy(t, rg.owner, rg.postID)
	if a := rg.row(t, id); a.AppealedDecisionID != nil || a.AppealedRevision != nil {
		t.Fatalf("legacy row=%+v", a)
	}
	if err := rg.svc.ReviewAppeal(ctx, id, "overturned", "", adminMeta(uuid.New(), "")); err != nil {
		t.Fatal(err)
	}
	if a := rg.row(t, id); a.Status != postgres.AppealOverturned || a.AppealedRevision == nil || *a.AppealedRevision != 5 || rg.post.calls[0].Revision != 5 {
		t.Fatalf("bound legacy row=%+v calls=%+v", a, rg.post.calls)
	}
	// Base status moved on since: superseded, never overturned.
	approved := rg.post.subject
	approved.ReviewStatus = "approved"
	rg.post.setSubject(approved)
	id2 := insertLegacy(t, rg.owner, uuid.New())
	if err := rg.svc.ReviewAppeal(ctx, id2, "overturned", "", adminMeta(uuid.New(), "")); !errors.Is(err, ErrAppealSuperseded) {
		t.Fatalf("legacy, status moved: %v", err)
	}
	if rg.row(t, id2).Status != postgres.AppealSuperseded || rg.post.callCount() != 1 {
		t.Fatal("legacy appeal on a re-decided post must close superseded without a canonical call")
	}
}

// ── Migration 012 ───────────────────────────────────────────────────────────

func TestAppealsMigration012_IdempotentAndEnforced(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	raw, err := database.Migrations.ReadFile("migrations/012_appeal_decision_binding.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The opener already applied it once; twice more must be a no-op, and
	// with the active index gone (a fresh database, or 008's index dropped
	// by hand) the file must rebuild it covering 'overturning'.
	for i := 0; i < 2; i++ {
		runLockedDDL(t, pool, string(raw))
	}
	runLockedDDL(t, pool, `DROP INDEX IF EXISTS trust.uq_appeals_one_active_per_user_content`, string(raw))
	var indexdef string
	if err := pool.QueryRow(ctx, `SELECT indexdef FROM pg_indexes WHERE schemaname='trust' AND indexname='uq_appeals_one_active_per_user_content'`).Scan(&indexdef); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(indexdef, "overturning") {
		t.Fatalf("active index does not cover overturning: %s", indexdef)
	}
	owner, post := uuid.New(), uuid.New()
	insert := func(status string, extra string) error {
		_, err := pool.Exec(ctx, `INSERT INTO trust.content_appeals (id, user_id, content_type, content_id, action_taken, appeal_reason, status, reviewed_by, overturn_decision_id, overturn_started_at, overturn_applied_at)
			VALUES (gen_random_uuid(), $1, 'post', $2, 'rejected', 'r', $3, `+extra+`)`, owner, post, status)
		return err
	}
	if err := insert("bogus", "NULL, NULL, NULL, NULL"); err == nil {
		t.Fatal("status CHECK accepted an unknown status")
	}
	if err := insert("overturning", "NULL, NULL, NULL, NULL"); err == nil {
		t.Fatal("overturning without a decision id, start time and reviewer was accepted")
	}
	if err := insert("overturned", "gen_random_uuid(), gen_random_uuid(), NOW(), NULL"); err == nil {
		t.Fatal("overturned without an acknowledgement time was accepted")
	}
	if err := insert("overturning", "gen_random_uuid(), gen_random_uuid(), NOW(), NULL"); err != nil {
		t.Fatalf("well-formed overturning row: %v", err)
	}
	// One active appeal per (user, post) while one is in flight.
	if err := insert("open", "NULL, NULL, NULL, NULL"); err == nil {
		t.Fatal("a second active appeal was accepted beside an in-flight overturn")
	}
	if err := insert("superseded", "NULL, NULL, NULL, NULL"); err != nil {
		t.Fatalf("superseded row: %v", err)
	}
	// Stats count the in-flight appeal as open.
	stats, err := postgres.New(pool).AdminStats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var open int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM trust.content_appeals WHERE status IN ('open','under_review','overturning')`).Scan(&open); err != nil {
		t.Fatal(err)
	}
	if stats.OpenAppeals != open {
		t.Fatalf("stats open appeals=%d, want %d", stats.OpenAppeals, open)
	}
}
