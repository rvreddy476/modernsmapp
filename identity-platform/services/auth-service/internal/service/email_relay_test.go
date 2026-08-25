package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/identity-auth-service/internal/store"
)

// fakeEmailJobStore is an in-memory stand-in for auth.email_delivery_jobs.
type fakeEmailJobStore struct {
	jobs     map[int64]*store.EmailJob
	sent     map[int64]bool
	attempts map[int64]int
	nextDue  map[int64]time.Time
	lastErr  map[int64]string
	nextID   int64
	fetchErr error
}

func newFakeEmailJobStore() *fakeEmailJobStore {
	return &fakeEmailJobStore{
		jobs:     map[int64]*store.EmailJob{},
		sent:     map[int64]bool{},
		attempts: map[int64]int{},
		nextDue:  map[int64]time.Time{},
		lastErr:  map[int64]string{},
	}
}

func (f *fakeEmailJobStore) enqueue(userID uuid.UUID) int64 {
	f.nextID++
	f.jobs[f.nextID] = &store.EmailJob{
		ID: f.nextID, UserID: userID, Purpose: store.EmailJobPurposeVerify,
	}
	f.nextDue[f.nextID] = time.Now().Add(-time.Second)
	return f.nextID
}

func (f *fakeEmailJobStore) FetchDueEmailJobs(_ context.Context, limit int) ([]store.EmailJob, error) {
	if f.fetchErr != nil {
		return nil, f.fetchErr
	}
	var out []store.EmailJob
	for id, j := range f.jobs {
		if f.sent[id] || time.Now().Before(f.nextDue[id]) {
			continue
		}
		job := *j
		job.Attempts = f.attempts[id]
		out = append(out, job)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (f *fakeEmailJobStore) MarkEmailJobSent(_ context.Context, id int64) error {
	f.sent[id] = true
	return nil
}

func (f *fakeEmailJobStore) RescheduleEmailJob(_ context.Context, id int64, next time.Time, lastErr string) error {
	f.attempts[id]++
	f.nextDue[id] = next
	f.lastErr[id] = lastErr
	return nil
}

func (f *fakeEmailJobStore) EmailJobBacklog(_ context.Context) (int, time.Time, error) {
	count := 0
	for id := range f.jobs {
		if !f.sent[id] {
			count++
		}
	}
	return count, time.Now(), nil
}

// TestRelayRetriesAfterMailProviderFailure — the B5 acceptance criterion.
//
// Failure injection: the mail sender hard-fails, so the job must survive,
// back off, and then succeed once the provider recovers. Before this existed a
// send failure was simply lost and the account was never contacted.
func TestRelayRetriesAfterMailProviderFailure(t *testing.T) {
	fake := newFakeEmailJobStore()
	userID := uuid.New()
	jobID := fake.enqueue(userID)

	providerDown := true
	sends := 0
	send := func(_ context.Context, _ uuid.UUID) error {
		sends++
		if providerDown {
			return errors.New("smtp: connection refused")
		}
		return nil
	}

	relay := NewEmailJobRelay(fake, send, discardLogger(), time.Second)

	// Provider down: the job must NOT be marked sent, and must be rescheduled.
	relay.ProcessOnce(context.Background())
	if fake.sent[jobID] {
		t.Fatal("job marked sent while the provider was failing — the email was never delivered")
	}
	if fake.attempts[jobID] != 1 {
		t.Fatalf("attempts = %d, want 1", fake.attempts[jobID])
	}
	if fake.lastErr[jobID] == "" {
		t.Fatal("failure recorded no error; an operator cannot diagnose a stuck queue")
	}
	if !fake.nextDue[jobID].After(time.Now()) {
		t.Fatal("job is immediately due again — backoff is not being applied")
	}

	// Backoff is respected: nothing is attempted while the job is not due.
	before := sends
	relay.ProcessOnce(context.Background())
	if sends != before {
		t.Fatal("relay sent during the backoff window")
	}

	// Provider recovers and the job becomes due.
	providerDown = false
	fake.nextDue[jobID] = time.Now().Add(-time.Second)
	relay.ProcessOnce(context.Background())

	if !fake.sent[jobID] {
		t.Fatal("job not delivered after the provider recovered")
	}
}

// TestRelayLeavesNothingBehindOnSuccess — the happy path closes the job out so
// the queue drains rather than resending forever.
func TestRelayDrainsOnSuccess(t *testing.T) {
	fake := newFakeEmailJobStore()
	id := fake.enqueue(uuid.New())

	relay := NewEmailJobRelay(fake, func(context.Context, uuid.UUID) error { return nil },
		discardLogger(), time.Second)
	relay.ProcessOnce(context.Background())

	if !fake.sent[id] {
		t.Fatal("successful send did not close the job")
	}

	count, _, _ := fake.EmailJobBacklog(context.Background())
	if count != 0 {
		t.Fatalf("backlog = %d after a successful drain, want 0", count)
	}
}

// TestRelaySurvivesFetchFailure — a database blip must not kill the loop.
func TestRelaySurvivesFetchFailure(t *testing.T) {
	fake := newFakeEmailJobStore()
	fake.fetchErr = errors.New("postgres unavailable")

	relay := NewEmailJobRelay(fake, func(context.Context, uuid.UUID) error {
		t.Fatal("send attempted despite a failed fetch")
		return nil
	}, discardLogger(), time.Second)

	relay.ProcessOnce(context.Background()) // must not panic
}

// TestRelayOneFailureDoesNotBlockOthers — head-of-line blocking would mean one
// bad address stops every other registrant's mail.
func TestRelayOneFailureDoesNotBlockOthers(t *testing.T) {
	fake := newFakeEmailJobStore()
	bad := uuid.New()
	good := uuid.New()
	badID := fake.enqueue(bad)
	goodID := fake.enqueue(good)

	relay := NewEmailJobRelay(fake, func(_ context.Context, u uuid.UUID) error {
		if u == bad {
			return errors.New("permanent bounce")
		}
		return nil
	}, discardLogger(), time.Second)

	relay.ProcessOnce(context.Background())

	if fake.sent[badID] {
		t.Fatal("failing job was marked sent")
	}
	if !fake.sent[goodID] {
		t.Fatal("a healthy job was blocked behind a failing one")
	}
}

// TestBackoffIsBoundedAndGrowing — unbounded growth would park a job past any
// useful retry horizon; no growth would hammer a struggling provider.
func TestBackoffIsBoundedAndGrowing(t *testing.T) {
	first := backoffFor(0)
	second := backoffFor(1)
	if second <= first {
		t.Fatalf("backoff not growing: %s then %s", first, second)
	}
	if got := backoffFor(1000); got != emailJobMaxBackoff {
		t.Fatalf("backoff not capped: got %s, want %s", got, emailJobMaxBackoff)
	}
	if got := backoffFor(-5); got < emailJobBaseBackoff {
		t.Fatalf("negative attempts produced %s", got)
	}
}
