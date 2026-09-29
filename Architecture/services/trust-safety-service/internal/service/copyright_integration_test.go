//go:build integration

// Copyright holds against trust_safety_it_test (Copyright Match plan
// sections 6.4, 9.2, 9.5): a transition writes the case change, the
// command row and the audit row in ONE transaction (a failure inside it
// leaves none of the three); concurrent reviewers get one transition; the
// dispatcher claims with a lease, re-sends the SAME decision id after a
// transient failure, supersedes a stale command only behind a newer one
// and keeps per-case order; the sweep re-queues the recorded decision when
// post-service lacks the hold and touches nothing else.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/trust-safety-service/internal/reconcile"
	"github.com/atpost/trust-safety-service/internal/restriction"
	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const itHMACKey = "integration-post-restriction-hmac-key-0123456789"

func newCopyrightRig(t *testing.T) (*pgxpool.Pool, *Service, *postgres.CopyrightStore, *int) {
	t.Helper()
	pool := openM7TrustDB(t)
	store := postgres.NewCopyrightStore(pool)
	svc := New(postgres.New(pool), nil)
	svc.SetCopyrightStore(store)
	kicks := 0
	svc.SetRestrictionKick(func() { kicks++ })
	return pool, svc, store, &kicks
}

func commandRows(t *testing.T, pool *pgxpool.Pool, caseID uuid.UUID) []postgres.RestrictionCommand {
	t.Helper()
	rows, err := pool.Query(context.Background(), `SELECT decision_id, case_revision, action, expected_state, reason_code, status, attempts, requeues, claims_digest, park_reason
		FROM trust.restriction_commands WHERE case_id = $1 ORDER BY case_revision`, caseID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []postgres.RestrictionCommand
	for rows.Next() {
		var k postgres.RestrictionCommand
		if err := rows.Scan(&k.DecisionID, &k.CaseRevision, &k.Action, &k.ExpectedState, &k.ReasonCode, &k.Status, &k.Attempts, &k.Requeues, &k.ClaimsDigest, &k.ParkReason); err != nil {
			t.Fatal(err)
		}
		out = append(out, k)
	}
	return out
}

func caseRow(t *testing.T, pool *pgxpool.Pool, caseID uuid.UUID) (state string, revision int64, found bool) {
	t.Helper()
	err := pool.QueryRow(context.Background(), `SELECT state, case_revision FROM trust.copyright_cases WHERE id = $1`, caseID).Scan(&state, &revision)
	if err != nil {
		if strings.Contains(err.Error(), "no rows") {
			return "", 0, false
		}
		t.Fatal(err)
	}
	return state, revision, true
}

func TestCopyrightHold_TransitionsAuditAndCommandsInOneTransaction(t *testing.T) {
	pool, svc, store, kicks := newCopyrightRig(t)
	ctx := context.Background()
	admin, post, author := uuid.New(), uuid.New(), uuid.New()

	hold, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: post, SubjectAuthorID: author, ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	c := hold.Case
	if c.State != postgres.CopyrightCaseHoldActive || c.CaseRevision != 1 || c.PolicyVersion != postgres.CopyrightPolicyVersion ||
		c.SubjectPostID != post || c.SubjectAuthorID != author || c.Source != "copyright" || *kicks != 1 {
		t.Fatalf("case=%+v kicks=%d", c, *kicks)
	}
	k := hold.Enforcement
	if k == nil || k.Status != postgres.RestrictionCommandPending || k.CaseRevision != 1 || k.ExpectedState != "absent" ||
		k.Action != "place_hold" || k.ReasonCode != "removal_upheld" || k.ActorID != admin || k.Attempts != 0 || len(k.ClaimsDigest) != 32 {
		t.Fatalf("command=%+v", k)
	}
	audit := auditRows(t, postgres.New(pool), postgres.AuditTargetCopyrightCase, c.ID)
	if len(audit) != 1 || audit[0].Action != postgres.AuditCopyrightHoldPlaced || audit[0].ActorUserID == nil || *audit[0].ActorUserID != admin ||
		audit[0].PrevStatus != nil || audit[0].NewStatus == nil || *audit[0].NewStatus != "hold_active" ||
		audit[0].NewResolution == nil || *audit[0].NewResolution != "removal_upheld" || audit[0].Reason == nil || *audit[0].Reason != "removal_upheld" {
		t.Fatalf("audit=%+v", audit)
	}

	// Release before the place was acked: allowed (ordering is the
	// dispatcher's), revision 2, expected active.
	rel, err := svc.ReleaseHold(ctx, c.ID, "claim_withdrawn", adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	if rel.Case.State != postgres.CopyrightCaseHoldReleased || rel.Case.CaseRevision != 2 || rel.Enforcement.ExpectedState != "active" ||
		rel.Enforcement.CaseRevision != 2 || rel.Enforcement.DecisionID == k.DecisionID || *kicks != 2 {
		t.Fatalf("release=%+v / %+v kicks=%d", rel.Case, rel.Enforcement, *kicks)
	}
	// Twice: refused, nothing written.
	if _, err := svc.ReleaseHold(ctx, c.ID, "claim_withdrawn", adminMeta(admin, "")); !errors.Is(err, postgres.ErrInvalidTransition) {
		t.Fatalf("double release: %v", err)
	}
	// Re-place: revision 3, expected released.
	again, err := svc.PlaceHold(ctx, c.ID, "reinstated_on_review", adminMeta(admin, ""))
	if err != nil || again.Case.CaseRevision != 3 || again.Enforcement.ExpectedState != "released" || again.Case.State != postgres.CopyrightCaseHoldActive {
		t.Fatalf("re-place: %+v err=%v", again, err)
	}
	if _, err := svc.PlaceHold(ctx, c.ID, "reinstated_on_review", adminMeta(admin, "")); !errors.Is(err, postgres.ErrInvalidTransition) {
		t.Fatalf("double place: %v", err)
	}
	cmds := commandRows(t, pool, c.ID)
	if len(cmds) != 3 || cmds[0].ExpectedState != "absent" || cmds[1].ExpectedState != "active" || cmds[2].ExpectedState != "released" {
		t.Fatalf("commands=%+v", cmds)
	}
	if audit := auditRows(t, postgres.New(pool), postgres.AuditTargetCopyrightCase, c.ID); len(audit) != 3 ||
		audit[1].Action != postgres.AuditCopyrightHoldReleased || *audit[1].PrevStatus != "hold_active" || *audit[1].NewStatus != "hold_released" ||
		audit[2].Action != postgres.AuditCopyrightHoldPlaced || *audit[2].PrevStatus != "hold_released" {
		t.Fatalf("audit=%+v", audit)
	}

	// ONE transaction: a failure after the case update (the command's
	// decision id already exists) leaves the case, commands and audit
	// exactly as they were.
	_, _, err = store.TransitionHold(ctx, c.ID, postgres.NewCommand{
		DecisionID: cmds[0].DecisionID, Action: "release_hold", ReasonCode: "claim_withdrawn",
		ActorID: admin, ClaimsDigest: cmds[0].ClaimsDigest, ExpectedState: "active",
	}, adminMeta(admin, ""))
	if err == nil {
		t.Fatal("a duplicate decision id must fail the transaction")
	}
	if state, rev, _ := caseRow(t, pool, c.ID); state != "hold_active" || rev != 3 {
		t.Fatalf("case after rollback: %s %d", state, rev)
	}
	if n := len(commandRows(t, pool, c.ID)); n != 3 {
		t.Fatalf("commands after rollback: %d", n)
	}
	if n := len(auditRows(t, postgres.New(pool), postgres.AuditTargetCopyrightCase, c.ID)); n != 3 {
		t.Fatalf("audit after rollback: %d", n)
	}
	// A service actor cannot transition; nothing is written.
	if _, err := svc.ReleaseHold(ctx, c.ID, "claim_withdrawn", postgres.AuditMeta{Actor: postgres.ServiceActor("x")}); !errors.Is(err, ErrActorRequired) {
		t.Fatalf("service actor: %v", err)
	}
	// A pinned case id replayed: CASE_EXISTS, no second case, no command.
	if _, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{CaseID: c.ID, SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, "")); !errors.Is(err, postgres.ErrCopyrightCaseExists) {
		t.Fatalf("replayed case id: %v", err)
	}
	if n := len(commandRows(t, pool, c.ID)); n != 3 || *kicks != 3 {
		t.Fatalf("after refusals: commands=%d kicks=%d", n, *kicks)
	}
}

func TestCopyrightHold_ConcurrentReleasesGiveOneTransition(t *testing.T) {
	pool, svc, _, _ := newCopyrightRig(t)
	ctx := context.Background()
	admin := uuid.New()
	hold, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, refused := 0, 0
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.ReleaseHold(ctx, hold.Case.ID, "reversed_on_review", adminMeta(uuid.New(), ""))
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, postgres.ErrInvalidTransition):
				refused++
			default:
				t.Errorf("unexpected: %v", err)
			}
		}()
	}
	wg.Wait()
	if ok != 1 || refused != workers-1 {
		t.Fatalf("ok=%d refused=%d", ok, refused)
	}
	if state, rev, _ := caseRow(t, pool, hold.Case.ID); state != "hold_released" || rev != 2 {
		t.Fatalf("case: %s %d", state, rev)
	}
	if cmds := commandRows(t, pool, hold.Case.ID); len(cmds) != 2 {
		t.Fatalf("commands=%d", len(cmds))
	}
	if n := len(auditRows(t, postgres.New(pool), postgres.AuditTargetCopyrightCase, hold.Case.ID)); n != 2 {
		t.Fatalf("audit=%d", n)
	}
}

// itPostStub verifies the capability like post-service and answers the
// script; it records every decision id and digest it saw.
type itPostStub struct {
	t       *testing.T
	v       *moderationcap.RestrictionVerifier
	mu      sync.Mutex
	seen    []moderationcap.RestrictionClaims
	answers map[string][]stubReply // by decision id; default 200
	rows    []restriction.Row
}

type stubReply struct {
	status int
	body   string
}

func (s *itPostStub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(restriction.HeaderInternalServiceKey) != "k" {
		w.WriteHeader(401)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodGet {
		s.mu.Lock()
		body, _ := json.Marshal(map[string]any{"data": map[string]any{"items": s.rows}})
		s.mu.Unlock()
		_, _ = w.Write(body)
		return
	}
	var req struct {
		Claims     moderationcap.RestrictionClaims `json:"claims"`
		Capability string                          `json:"capability"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || s.v.Verify(req.Claims, req.Capability) != nil {
		s.t.Errorf("stub: bad command: %v", err)
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":{"code":"INVALID_CAPABILITY"}}`))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, req.Claims)
	q := s.answers[req.Claims.DecisionID]
	if len(q) == 0 {
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"data":{"state":"active","changed":true,"replayed":false}}`))
		return
	}
	s.answers[req.Claims.DecisionID] = q[1:]
	w.WriteHeader(q[0].status)
	_, _ = w.Write([]byte(q[0].body))
}

func newITStub(t *testing.T) (*itPostStub, *restriction.Client, string) {
	t.Helper()
	v, err := moderationcap.NewRestrictionVerifier([]byte(itHMACKey), nil, moderationcap.MaxRestrictionTTL)
	if err != nil {
		t.Fatal(err)
	}
	stub := &itPostStub{t: t, v: v, answers: map[string][]stubReply{}}
	srv := httptest.NewServer(stub)
	t.Cleanup(srv.Close)
	signer, err := moderationcap.NewRestrictionSigner([]byte(itHMACKey), restriction.CapabilityTTL)
	if err != nil {
		t.Fatal(err)
	}
	return stub, restriction.New(restriction.Config{BaseURL: srv.URL, InternalKey: "k", Signer: signer}), srv.URL
}

// sends counts how often the stub saw decision id.
func (s *itPostStub) sends(id uuid.UUID) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, cl := range s.seen {
		if cl.DecisionID == id.String() {
			n++
		}
	}
	return n
}

func quietLog() *slog.Logger { return slog.New(slog.NewTextHandler(&strings.Builder{}, nil)) }

func TestCopyrightHold_DispatcherLeaseRetryAndOrdering(t *testing.T) {
	pool, svc, store, _ := newCopyrightRig(t)
	ctx := context.Background()
	stub, client, _ := newITStub(t)
	d := restriction.NewDispatcher(store, client, quietLog(), nil)
	d.Lease = 2 * time.Second
	admin := uuid.New()

	// Case A: the place fails twice (503, then a transport-shaped 502),
	// then lands.
	a, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	placeA := a.Enforcement.DecisionID
	stub.answers[placeA.String()] = []stubReply{{503, `{"error":{"code":"UNAVAILABLE"}}`}, {502, ``}}

	// Sweep 1: claimed (attempts 1), 503 → still pending, held back. (The
	// scratch database is shared: other tests leave rows in the queue, so
	// every assertion counts THIS decision.)
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	cmds := commandRows(t, pool, a.Case.ID)
	if cmds[0].Status != "pending" || cmds[0].Attempts != 1 {
		t.Fatalf("after 503: %+v", cmds[0])
	}
	// Held back by the backoff: a second sweep sends nothing.
	if _, err := d.Sweep(ctx); err != nil || stub.sends(placeA) != 1 {
		t.Fatalf("held back: err=%v sends=%d", err, stub.sends(placeA))
	}
	// Simulate the backoff / a lapsed lease after a crash mid-send: the
	// row becomes due and the SAME decision goes out again.
	for i := 0; i < 2; i++ {
		if _, err := pool.Exec(ctx, `UPDATE trust.restriction_commands SET next_attempt_at = clock_timestamp() WHERE decision_id = $1`, placeA); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Sweep(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if stub.sends(placeA) != 3 {
		t.Fatalf("sends=%d, want 3", stub.sends(placeA))
	}
	for i, cl := range stub.seen {
		if cl.DecisionID == placeA.String() && string(cl.Digest()) != string(cmds[0].ClaimsDigest) {
			t.Fatalf("send %d changed the digest", i)
		}
	}
	if cmds = commandRows(t, pool, a.Case.ID); cmds[0].Status != "acked" || cmds[0].Attempts != 3 {
		t.Fatalf("after ack: %+v", cmds[0])
	}
	var acked bool
	if err := pool.QueryRow(ctx, `SELECT acked_at IS NOT NULL AND result->>'state' = 'active' AND replayed = false FROM trust.restriction_commands WHERE decision_id = $1`, placeA).Scan(&acked); err != nil || !acked {
		t.Fatalf("ack columns: %v %v", acked, err)
	}
	// Terminal: a re-run sends nothing more.
	if _, err := pool.Exec(ctx, `UPDATE trust.restriction_commands SET next_attempt_at = clock_timestamp() WHERE decision_id = $1`, placeA); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Sweep(ctx); err != nil || stub.sends(placeA) != 3 {
		t.Fatalf("acked row re-sent: sends=%d err=%v", stub.sends(placeA), err)
	}

	// Case B: place then release queued before either is sent. Post-service
	// answers the place with STALE_CASE_REVISION: superseded (a newer
	// command exists), and only THEN is the release sent, and acked.
	b, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ReleaseHold(ctx, b.Case.ID, "claim_withdrawn", adminMeta(admin, "")); err != nil {
		t.Fatal(err)
	}
	stub.answers[b.Enforcement.DecisionID.String()] = []stubReply{{409, `{"error":{"code":"STALE_CASE_REVISION"}}`}}
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	cmds = commandRows(t, pool, b.Case.ID)
	if stub.sends(cmds[0].DecisionID) != 1 || stub.sends(cmds[1].DecisionID) != 0 || cmds[0].Status != "superseded" || cmds[1].Status != "pending" {
		t.Fatalf("ordering: sends=%d/%d statuses=%s/%s", stub.sends(cmds[0].DecisionID), stub.sends(cmds[1].DecisionID), cmds[0].Status, cmds[1].Status)
	}
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if cmds = commandRows(t, pool, b.Case.ID); cmds[1].Status != "acked" || stub.sends(cmds[1].DecisionID) != 1 {
		t.Fatalf("release: %+v sends=%d", cmds[1], stub.sends(cmds[1].DecisionID))
	}

	// Case C: a refusal parks with its reason and is never re-sent.
	cc, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	stub.answers[cc.Enforcement.DecisionID.String()] = []stubReply{{404, `{"error":{"code":"SUBJECT_NOT_FOUND"}}`}}
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	cmds = commandRows(t, pool, cc.Case.ID)
	if cmds[0].Status != "parked" || cmds[0].ParkReason == nil || *cmds[0].ParkReason != restriction.ParkReasonRefused {
		t.Fatalf("parked: %+v", cmds[0])
	}
	if _, err := pool.Exec(ctx, `UPDATE trust.restriction_commands SET next_attempt_at = clock_timestamp() WHERE decision_id = $1`, cc.Enforcement.DecisionID); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Sweep(ctx); err != nil || stub.sends(cc.Enforcement.DecisionID) != 1 {
		t.Fatalf("parked row re-sent: sends=%d", stub.sends(cc.Enforcement.DecisionID))
	}
	// A STALE answer with no newer command parks as stale_no_successor.
	dd, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err != nil {
		t.Fatal(err)
	}
	stub.answers[dd.Enforcement.DecisionID.String()] = []stubReply{{409, `{"error":{"code":"STALE_CASE_REVISION"}}`}}
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if cmds = commandRows(t, pool, dd.Case.ID); cmds[0].Status != "parked" || *cmds[0].ParkReason != restriction.ParkReasonStaleNoSuccessor {
		t.Fatalf("stale without successor: %+v", cmds[0])
	}
	// Queue health reads.
	if n, err := store.CountParkedCommands(ctx); err != nil || n < 2 {
		t.Fatalf("parked count=%d err=%v", n, err)
	}
	if _, err := store.OldestPendingCommandAge(ctx); err != nil {
		t.Fatal(err)
	}
}

// Two dispatchers never send one row at once: a claimed row is invisible
// to a second claim until its lease lapses.
func TestCopyrightHold_ClaimIsExclusive(t *testing.T) {
	_, svc, store, _ := newCopyrightRig(t)
	ctx := context.Background()
	hold, err := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(uuid.New(), ""))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.ClaimPendingCommands(ctx, 500, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var mine bool
	for _, k := range first {
		mine = mine || k.DecisionID == hold.Enforcement.DecisionID
	}
	if !mine {
		t.Fatalf("first claim did not include the new command")
	}
	second, err := store.ClaimPendingCommands(ctx, 500, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range second {
		if k.DecisionID == hold.Enforcement.DecisionID {
			t.Fatalf("a leased row was claimed twice")
		}
	}
	// Only a pending row records an outcome.
	if err := store.MarkCommandAcked(ctx, hold.Enforcement.DecisionID, 200, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkCommandAcked(ctx, hold.Enforcement.DecisionID, 200, false, nil); !errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
		t.Fatalf("second ack: %v", err)
	}
	if err := store.ParkCommand(ctx, hold.Enforcement.DecisionID, "x", 0, "", ""); !errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
		t.Fatalf("park after ack: %v", err)
	}
	if err := store.RecordCommandRetry(ctx, hold.Enforcement.DecisionID, 500, "", "", time.Second); !errors.Is(err, postgres.ErrRestrictionCommandNotPending) {
		t.Fatalf("retry after ack: %v", err)
	}
	if err := store.ParkCommand(ctx, uuid.New(), "", 0, "", ""); !errors.Is(err, postgres.ErrCopyrightCaseInvalidInput) {
		t.Fatalf("park without reason: %v", err)
	}
}

func TestCopyrightHold_SweepRequeuesOnlyTheRecordedAckedDecision(t *testing.T) {
	pool, svc, store, _ := newCopyrightRig(t)
	ctx := context.Background()
	stub, client, stubURL := newITStub(t)
	d := restriction.NewDispatcher(store, client, quietLog(), nil)
	admin := uuid.New()

	// Three cases: acked-and-present, acked-and-missing, pending, parked.
	present, _ := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	missing, _ := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	pending, _ := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	parked, _ := svc.CreateCopyrightHold(ctx, CreateCopyrightHoldInput{SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ReasonCode: "removal_upheld"}, adminMeta(admin, ""))
	if err := store.ParkCommand(ctx, parked.Enforcement.DecisionID, "refused", 404, "SUBJECT_NOT_FOUND", ""); err != nil {
		t.Fatal(err)
	}
	// post-service knows the present case's hold only.
	stub.rows = []restriction.Row{{CaseID: present.Case.ID, PostID: present.Case.SubjectPostID, State: "active", CaseRevision: 1}}

	// The sweep needs a read token: give the stub client one it accepts
	// (the stub does not verify the read token; the unit test does).
	_, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := servicetoken.NewSignerFromBase64(restriction.Issuer, "t1", priv)
	if err != nil {
		t.Fatal(err)
	}
	reader := restriction.New(restriction.Config{BaseURL: stubURL, InternalKey: "k", Tokens: tokens})
	kicks := 0
	rec := reconcile.NewHoldReconciler(store, reader, nil, func() { kicks++ }, quietLog())
	rep, err := rec.Reconcile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Findings[reconcile.DriftMissingHold] < 1 || rep.Findings[reconcile.DriftParked] < 1 || rep.Requeued < 1 || kicks < 1 {
		t.Fatalf("report=%+v kicks=%d", rep, kicks)
	}
	get := func(id uuid.UUID) postgres.RestrictionCommand { return commandRows(t, pool, id)[0] }
	if k := get(missing.Case.ID); k.Status != "pending" || k.Requeues != 1 {
		t.Fatalf("missing hold must be re-queued: %+v", k)
	}
	if k := get(present.Case.ID); k.Status != "acked" || k.Requeues != 0 {
		t.Fatalf("present hold must be untouched: %+v", k)
	}
	if k := get(pending.Case.ID); k.Status != "pending" || k.Requeues != 0 {
		t.Fatalf("pending command must be untouched: %+v", k)
	}
	if k := get(parked.Case.ID); k.Status != "parked" || k.Requeues != 0 {
		t.Fatalf("parked command must be untouched: %+v", k)
	}
	// The re-queued decision goes out again with the same id.
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	if stub.sends(missing.Enforcement.DecisionID) != 2 || get(missing.Case.ID).Status != "acked" {
		t.Fatalf("re-queued decision must be re-sent and acked")
	}
	// A released case (acked release) whose row is still active re-queues
	// the recorded RELEASE; an active case never gets a release sent.
	if _, err := svc.ReleaseHold(ctx, present.Case.ID, "claim_withdrawn", adminMeta(admin, "")); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Sweep(ctx); err != nil {
		t.Fatal(err)
	}
	rel := commandRows(t, pool, present.Case.ID)[1]
	if rel.Status != "acked" {
		t.Fatalf("release not acked: %+v", rel)
	}
	stub.rows = []restriction.Row{{CaseID: present.Case.ID, PostID: present.Case.SubjectPostID, State: "active", CaseRevision: 1}}
	rep, err = rec.Reconcile(ctx)
	if err != nil || rep.Findings[reconcile.DriftUnexpectedActive] < 1 {
		t.Fatalf("unexpected active: %+v err=%v", rep, err)
	}
	if k := commandRows(t, pool, present.Case.ID)[1]; k.Status != "pending" || k.Requeues != 1 || k.Action != "release_hold" || k.DecisionID != rel.DecisionID {
		t.Fatalf("recorded release must be re-queued: %+v", k)
	}
}
