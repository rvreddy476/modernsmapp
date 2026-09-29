// The dispatcher without a database (plan section 9.2): each disposition
// lands on the right store call, a retry re-sends the SAME decision id and
// digest, a stale command is superseded only when a newer one exists, the
// backoff stays within its bounds.
package restriction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// fakeStore is an in-memory trust.restriction_commands.
type fakeStore struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]*postgres.RestrictionCommand
	order   []uuid.UUID
	retries map[uuid.UUID][]time.Duration
	failOn  string
}

func newFakeStore() *fakeStore {
	return &fakeStore{rows: map[uuid.UUID]*postgres.RestrictionCommand{}, retries: map[uuid.UUID][]time.Duration{}}
}

func (f *fakeStore) add(caseID uuid.UUID, rev int64, action string) *postgres.RestrictionCommand {
	f.mu.Lock()
	defer f.mu.Unlock()
	k := &postgres.RestrictionCommand{
		DecisionID: uuid.New(), CaseID: caseID, CaseRevision: rev, Action: action, Source: "copyright",
		SubjectPostID: uuid.New(), SubjectAuthorID: uuid.New(), ExpectedState: "absent", ReasonCode: "removal_upheld",
		PolicyVersion: "copyright-v1", ActorID: uuid.New(), Status: postgres.RestrictionCommandPending, CreatedAt: time.Now(),
	}
	f.rows[k.DecisionID] = k
	f.order = append(f.order, k.DecisionID)
	return k
}

func (f *fakeStore) ClaimPendingCommands(_ context.Context, limit int, lease time.Duration) ([]postgres.RestrictionCommand, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failOn == "claim" {
		return nil, errors.New("claim failed")
	}
	var out []postgres.RestrictionCommand
	now := time.Now()
	for _, id := range f.order {
		k := f.rows[id]
		if k.Status != postgres.RestrictionCommandPending || k.NextAttemptAt.After(now) {
			continue
		}
		blocked := false
		for _, other := range f.rows {
			if other.CaseID == k.CaseID && other.Status == postgres.RestrictionCommandPending && other.CaseRevision < k.CaseRevision {
				blocked = true
			}
		}
		if blocked {
			continue
		}
		k.Attempts++
		k.NextAttemptAt = now.Add(lease)
		out = append(out, *k)
		if len(out) == limit {
			break
		}
	}
	return out, nil
}

func (f *fakeStore) pending(id uuid.UUID) (*postgres.RestrictionCommand, error) {
	k, ok := f.rows[id]
	if !ok || k.Status != postgres.RestrictionCommandPending {
		return nil, postgres.ErrRestrictionCommandNotPending
	}
	return k, nil
}

func (f *fakeStore) MarkCommandAcked(_ context.Context, id uuid.UUID, status int, replayed bool, result json.RawMessage) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := f.pending(id)
	if err != nil {
		return err
	}
	k.Status, k.Replayed, k.Result, k.LastStatusCode = postgres.RestrictionCommandAcked, &replayed, result, &status
	return nil
}

func (f *fakeStore) RecordCommandRetry(_ context.Context, id uuid.UUID, status int, code, cause string, retryAfter time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := f.pending(id)
	if err != nil {
		return err
	}
	k.NextAttemptAt = time.Now().Add(retryAfter)
	k.LastStatusCode, k.LastErrorCode, k.LastError = &status, &code, &cause
	f.retries[id] = append(f.retries[id], retryAfter)
	return nil
}

func (f *fakeStore) MarkCommandSuperseded(_ context.Context, id uuid.UUID, status int, code, cause string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := f.pending(id)
	if err != nil {
		return false, err
	}
	for _, other := range f.rows {
		if other.CaseID == k.CaseID && other.CaseRevision > k.CaseRevision {
			k.Status = postgres.RestrictionCommandSuperseded
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) ParkCommand(_ context.Context, id uuid.UUID, reason string, status int, code, cause string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	k, err := f.pending(id)
	if err != nil {
		return err
	}
	k.Status, k.ParkReason, k.LastStatusCode, k.LastErrorCode = postgres.RestrictionCommandParked, &reason, &status, &code
	return nil
}

func (f *fakeStore) OldestPendingCommandAge(context.Context) (time.Duration, error) { return 0, nil }
func (f *fakeStore) CountParkedCommands(context.Context) (int, error)               { return 0, nil }

func (f *fakeStore) status(id uuid.UUID) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[id].Status
}

// scriptedSender answers per decision id in order and records every send.
type scriptedSender struct {
	mu      sync.Mutex
	script  map[uuid.UUID][]Outcome
	sent    []Command
	digests [][]byte
}

func (s *scriptedSender) Send(_ context.Context, cmd Command) Outcome {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, cmd)
	s.digests = append(s.digests, Digest(cmd))
	q := s.script[cmd.DecisionID]
	if len(q) == 0 {
		return Outcome{Disposition: DispositionAcked, StatusCode: 200, Body: json.RawMessage(`{"replayed":false}`)}
	}
	s.script[cmd.DecisionID] = q[1:]
	return q[0]
}

func newTestDispatcher(store Store, sender Sender) *Dispatcher {
	d := NewDispatcher(store, sender, slog.New(slog.NewTextHandler(&strings.Builder{}, nil)), nil)
	d.rand = func() float64 { return 0.5 } // no jitter
	return d
}

func TestDispatcher_RetryResendsTheSameDecision(t *testing.T) {
	store := newFakeStore()
	sender := &scriptedSender{script: map[uuid.UUID][]Outcome{}}
	k := store.add(uuid.New(), 1, "place_hold")
	sender.script[k.DecisionID] = []Outcome{
		{Disposition: DispositionRetry, StatusCode: 503, Err: errors.New("503")},
		{Disposition: DispositionRetry, StatusCode: 0, Err: errors.New("dial")},
		{Disposition: DispositionAcked, StatusCode: 200, Replayed: true, Body: json.RawMessage(`{"replayed":true}`)},
	}
	d := newTestDispatcher(store, sender)
	ctx := context.Background()

	// First sweep: transient failure → pending with backoff.
	if n, err := d.Sweep(ctx); err != nil || n != 0 {
		t.Fatalf("sweep 1: n=%d err=%v", n, err)
	}
	if store.status(k.DecisionID) != postgres.RestrictionCommandPending || len(store.retries[k.DecisionID]) != 1 {
		t.Fatalf("after a 503 the row stays pending with one retry recorded: %s %v", store.status(k.DecisionID), store.retries)
	}
	// While held back, the row is not claimed again.
	if n, err := d.Sweep(ctx); err != nil || n != 0 || len(sender.sent) != 1 {
		t.Fatalf("held back: n=%d err=%v sent=%d", n, err, len(sender.sent))
	}
	// The lease / backoff lapses (a crash mid-send looks the same): the
	// SAME decision goes out again.
	store.rows[k.DecisionID].NextAttemptAt = time.Now().Add(-time.Second)
	_, _ = d.Sweep(ctx)
	store.rows[k.DecisionID].NextAttemptAt = time.Now().Add(-time.Second)
	if n, err := d.Sweep(ctx); err != nil || n != 1 {
		t.Fatalf("sweep 3: n=%d err=%v", n, err)
	}
	if len(sender.sent) != 3 {
		t.Fatalf("sent %d, want 3", len(sender.sent))
	}
	for i, c := range sender.sent {
		if c.DecisionID != k.DecisionID || !bytes.Equal(sender.digests[i], sender.digests[0]) || c.CaseRevision != 1 || c.ExpectedState != "absent" {
			t.Fatalf("send %d changed the decision: %+v", i, c)
		}
	}
	row := store.rows[k.DecisionID]
	if row.Status != postgres.RestrictionCommandAcked || row.Replayed == nil || !*row.Replayed || row.Attempts != 3 {
		t.Fatalf("acked row: %+v", row)
	}
	// Backoff grew: 30 s then 60 s (no jitter).
	if r := store.retries[k.DecisionID]; len(r) != 2 || r[0] != 30*time.Second || r[1] != 60*time.Second {
		t.Fatalf("retries=%v", r)
	}
}

func TestDispatcher_DispositionTable(t *testing.T) {
	type want struct {
		status string
		reason string
	}
	cases := []struct {
		name string
		out  Outcome
		want want
	}{
		{"acked", Outcome{Disposition: DispositionAcked, StatusCode: 200}, want{postgres.RestrictionCommandAcked, ""}},
		{"retry", Outcome{Disposition: DispositionRetry, StatusCode: 500, Err: errors.New("x")}, want{postgres.RestrictionCommandPending, ""}},
		{"stale without successor", Outcome{Disposition: DispositionSuperseded, StatusCode: 409, ErrorCode: CodeStaleCaseRevision, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonStaleNoSuccessor}},
		{"decision conflict", Outcome{Disposition: DispositionParked, StatusCode: 409, ErrorCode: CodeDecisionConflict, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonDecisionConflict}},
		{"subject mismatch", Outcome{Disposition: DispositionParked, StatusCode: 409, ErrorCode: CodeSubjectMismatch, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonSubjectMismatch}},
		{"state mismatch", Outcome{Disposition: DispositionParked, StatusCode: 409, ErrorCode: CodeStateMismatch, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonStateMismatch}},
		{"403", Outcome{Disposition: DispositionParked, StatusCode: 403, ErrorCode: CodeInvalidCapability, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonRefused}},
		{"404", Outcome{Disposition: DispositionParked, StatusCode: 404, ErrorCode: CodeSubjectNotFound, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonRefused}},
		{"422", Outcome{Disposition: DispositionParked, StatusCode: 422, ErrorCode: CodeInvalidClaims, Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonInvalidClaims}},
		{"signer refused", Outcome{Disposition: DispositionParked, ErrorCode: "SIGNER_REFUSED", Err: errors.New("x")}, want{postgres.RestrictionCommandParked, ParkReasonInvalidClaims}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeStore()
			sender := &scriptedSender{script: map[uuid.UUID][]Outcome{}}
			k := store.add(uuid.New(), 1, "place_hold")
			sender.script[k.DecisionID] = []Outcome{tc.out}
			d := newTestDispatcher(store, sender)
			if _, err := d.Sweep(context.Background()); err != nil {
				t.Fatal(err)
			}
			row := store.rows[k.DecisionID]
			if row.Status != tc.want.status {
				t.Fatalf("status=%s, want %s", row.Status, tc.want.status)
			}
			if tc.want.reason != "" && (row.ParkReason == nil || *row.ParkReason != tc.want.reason) {
				t.Fatalf("park_reason=%v, want %s", row.ParkReason, tc.want.reason)
			}
			// A parked row is never sent again.
			store.rows[k.DecisionID].NextAttemptAt = time.Now().Add(-time.Second)
			before := len(sender.sent)
			_, _ = d.Sweep(context.Background())
			if tc.want.status == postgres.RestrictionCommandParked && len(sender.sent) != before {
				t.Fatalf("a parked command was re-sent")
			}
		})
	}
}

func TestDispatcher_StaleIsSupersededWhenANewerCommandExistsAndOrderingHolds(t *testing.T) {
	store := newFakeStore()
	sender := &scriptedSender{script: map[uuid.UUID][]Outcome{}}
	caseID := uuid.New()
	place := store.add(caseID, 1, "place_hold")
	release := store.add(caseID, 2, "release_hold")
	sender.script[place.DecisionID] = []Outcome{{Disposition: DispositionSuperseded, StatusCode: 409, ErrorCode: CodeStaleCaseRevision, Err: errors.New("stale")}}
	d := newTestDispatcher(store, sender)

	// Sweep 1: only the place is claimable (the release waits behind it).
	if _, err := d.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(sender.sent) != 1 || sender.sent[0].DecisionID != place.DecisionID {
		t.Fatalf("sent=%+v, want the place alone", sender.sent)
	}
	if store.status(place.DecisionID) != postgres.RestrictionCommandSuperseded {
		t.Fatalf("place=%s, want superseded", store.status(place.DecisionID))
	}
	// Sweep 2: the release is free to go and acks.
	if n, err := d.Sweep(context.Background()); err != nil || n != 1 {
		t.Fatalf("sweep 2: n=%d err=%v", n, err)
	}
	if store.status(release.DecisionID) != postgres.RestrictionCommandAcked || len(sender.sent) != 2 || sender.sent[1].DecisionID != release.DecisionID {
		t.Fatalf("release=%s sent=%d", store.status(release.DecisionID), len(sender.sent))
	}
}

func TestDispatcher_BackoffBounds(t *testing.T) {
	d := newTestDispatcher(newFakeStore(), &scriptedSender{})
	prev := time.Duration(0)
	for attempts := 1; attempts <= 30; attempts++ {
		w := d.backoff(attempts)
		if w < d.MinBackoff/2 || w > d.MaxBackoff {
			t.Fatalf("attempt %d: %s outside [%s, %s]", attempts, w, d.MinBackoff/2, d.MaxBackoff)
		}
		if w < prev {
			t.Fatalf("attempt %d: %s < previous %s", attempts, w, prev)
		}
		prev = w
	}
	if d.backoff(1) != 30*time.Second || d.backoff(2) != time.Minute || d.backoff(7) != 30*time.Minute || d.backoff(100) != 30*time.Minute {
		t.Fatalf("schedule: %s %s %s %s", d.backoff(1), d.backoff(2), d.backoff(7), d.backoff(100))
	}
	// With jitter the delay stays within ±20 %.
	for _, r := range []float64{0, 1} {
		d.rand = func() float64 { return r }
		if w := d.backoff(1); w < 24*time.Second || w > 36*time.Second {
			t.Fatalf("jitter r=%v: %s", r, w)
		}
	}
}

func TestDispatcher_StoreFailureIsReturnedAndKickNeverBlocks(t *testing.T) {
	store := newFakeStore()
	store.failOn = "claim"
	d := newTestDispatcher(store, &scriptedSender{})
	if _, err := d.Sweep(context.Background()); err == nil {
		t.Fatal("a claim failure must surface")
	}
	d.Kick()
	d.Kick()
	d.Kick()
}
