package purge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/atpost/analytics-service/internal/model"
	"github.com/google/uuid"
)

// reels-8: the analytics erase runs Scylla, then PostgreSQL, then Redis;
// any failure stops it before the ack; a retry finishes the job.

type fakeAnalytics struct {
	log      []string
	keys     map[uuid.UUID][]model.WatchSessionKey // PG-held session keys per user
	pgRows   map[uuid.UUID]int
	scylla   map[model.WatchSessionKey]bool
	history  map[uuid.UUID]bool
	signals  map[uuid.UUID]bool
	failOnce map[string]error
}

func newFakeAnalytics() *fakeAnalytics {
	return &fakeAnalytics{
		keys: map[uuid.UUID][]model.WatchSessionKey{}, pgRows: map[uuid.UUID]int{},
		scylla: map[model.WatchSessionKey]bool{}, history: map[uuid.UUID]bool{},
		signals: map[uuid.UUID]bool{}, failOnce: map[string]error{},
	}
}

func (f *fakeAnalytics) fail(step string) error {
	if err, ok := f.failOnce[step]; ok {
		delete(f.failOnce, step)
		return err
	}
	return nil
}

func (f *fakeAnalytics) seed(user uuid.UUID, sessions int) {
	for i := 0; i < sessions; i++ {
		k := model.WatchSessionKey{ContentID: uuid.New(), SessionID: fmt.Sprintf("s-%05d", i)}
		f.keys[user] = append(f.keys[user], k)
		f.scylla[k] = true
	}
	f.pgRows[user] = 3 + sessions
	f.history[user] = true
	f.signals[user] = true
}

func (f *fakeAnalytics) WatchSessionKeys(_ context.Context, user uuid.UUID, after model.WatchSessionKey, limit int) ([]model.WatchSessionKey, error) {
	if err := f.fail("keys"); err != nil {
		return nil, err
	}
	all := f.keys[user]
	start := 0
	if after.SessionID != "" {
		for i, k := range all {
			if k == after {
				start = i + 1
			}
		}
	}
	end := start + limit
	if end > len(all) {
		end = len(all)
	}
	f.log = append(f.log, "pg-keys")
	return append([]model.WatchSessionKey(nil), all[start:end]...), nil
}

func (f *fakeAnalytics) PurgeUser(_ context.Context, user uuid.UUID) error {
	if err := f.fail("pg"); err != nil {
		return err
	}
	delete(f.pgRows, user)
	delete(f.keys, user)
	f.log = append(f.log, "pg-purge")
	return nil
}

func (f *fakeAnalytics) DeleteWatchSessions(_ context.Context, keys []model.WatchSessionKey) error {
	if err := f.fail("scylla"); err != nil {
		return err
	}
	for _, k := range keys {
		delete(f.scylla, k)
	}
	f.log = append(f.log, fmt.Sprintf("scylla-sessions:%d", len(keys)))
	return nil
}

func (f *fakeAnalytics) DeleteViewerHistory(_ context.Context, user uuid.UUID) error {
	delete(f.history, user)
	f.log = append(f.log, "scylla-history")
	return nil
}

func (f *fakeAnalytics) DeleteViewerSignals(_ context.Context, user uuid.UUID) error {
	if err := f.fail("redis"); err != nil {
		return err
	}
	delete(f.signals, user)
	f.log = append(f.log, "redis")
	return nil
}

type recordingAcks struct{ acks []Ack }

func (r *recordingAcks) PublishPurgeAck(_ context.Context, a Ack) error {
	r.acks = append(r.acks, a)
	return nil
}

func purgePayload(user uuid.UUID) json.RawMessage {
	return json.RawMessage(`{"user_id":"` + user.String() + `"}`)
}

func TestAnalyticsEraseCoversEveryStoreInOrder(t *testing.T) {
	f := newFakeAnalytics()
	gone, kept := uuid.New(), uuid.New()
	f.seed(gone, sessionKeyPage+3) // two pages of keys
	f.seed(kept, 2)
	acks := &recordingAcks{}
	h := NewHandler("analytics", NewEraser(f, f, f), acks, nil, nil)

	if err := h.Handle(context.Background(), EventUserPurgeRequested, purgePayload(gone)); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf("pg-keys,scylla-sessions:%d,pg-keys,scylla-sessions:3,scylla-history,pg-purge,redis", sessionKeyPage)
	if got := strings.Join(f.log, ","); got != want {
		t.Fatalf("order:\n got %s\nwant %s", got, want)
	}
	for k := range f.scylla {
		found := false
		for _, kk := range f.keys[kept] {
			if kk == k {
				found = true
			}
		}
		if !found {
			t.Fatalf("a watch session of the purged user survived: %v", k)
		}
	}
	if _, ok := f.pgRows[gone]; ok || f.history[gone] || f.signals[gone] {
		t.Fatalf("purged user left data: pg=%v history=%v signals=%v", f.pgRows[gone], f.history[gone], f.signals[gone])
	}
	if f.pgRows[kept] == 0 || !f.history[kept] || !f.signals[kept] || len(f.scylla) != 2 {
		t.Fatalf("another user's data was touched")
	}
	if len(acks.acks) != 1 || acks.acks[0].Service != "analytics" || acks.acks[0].UserID != gone.String() {
		t.Fatalf("acks = %+v, want one as analytics for %s", acks.acks, gone)
	}

	// A redelivery finds nothing and acks again.
	f.log = nil
	if err := h.Handle(context.Background(), EventUserPurgeRequested, purgePayload(gone)); err != nil {
		t.Fatal(err)
	}
	if len(acks.acks) != 2 {
		t.Fatalf("redelivery did not re-ack: %+v", acks.acks)
	}
}

func TestAnalyticsEraseFailureSendsNoAckAndRetryFinishes(t *testing.T) {
	for _, step := range []string{"keys", "scylla", "pg", "redis"} {
		t.Run(step, func(t *testing.T) {
			f := newFakeAnalytics()
			gone := uuid.New()
			f.seed(gone, 4)
			f.failOnce[step] = errors.New(step + " down")
			acks := &recordingAcks{}
			h := NewHandler("analytics", NewEraser(f, f, f), acks, nil, nil)

			if err := h.Handle(context.Background(), EventUserPurgeRequested, purgePayload(gone)); err == nil {
				t.Fatal("erase failure returned nil")
			}
			if len(acks.acks) != 0 {
				t.Fatalf("acked after a failed erase: %+v", acks.acks)
			}
			if err := h.Handle(context.Background(), EventUserPurgeRequested, purgePayload(gone)); err != nil {
				t.Fatal(err)
			}
			if len(f.scylla) != 0 || f.history[gone] || f.signals[gone] || f.pgRows[gone] != 0 {
				t.Fatalf("retry left data: scylla=%d history=%v signals=%v pg=%d", len(f.scylla), f.history[gone], f.signals[gone], f.pgRows[gone])
			}
			if len(acks.acks) != 1 {
				t.Fatalf("acks after retry = %d", len(acks.acks))
			}
		})
	}
}

// Without Scylla or Redis the PostgreSQL slice is still erased and acked.
func TestAnalyticsEraseWithoutOptionalStores(t *testing.T) {
	f := newFakeAnalytics()
	gone := uuid.New()
	f.seed(gone, 2)
	acks := &recordingAcks{}
	h := NewHandler("analytics", NewEraser(f, nil, nil), acks, nil, nil)
	if err := h.Handle(context.Background(), EventUserPurgeRequested, purgePayload(gone)); err != nil {
		t.Fatal(err)
	}
	if strings.Join(f.log, ",") != "pg-purge" || len(acks.acks) != 1 {
		t.Fatalf("log=%v acks=%d", f.log, len(acks.acks))
	}
}

// Hide events are not erasure: nothing is deleted and nothing acked.
func TestAnalyticsIgnoresHideEvents(t *testing.T) {
	f := newFakeAnalytics()
	gone := uuid.New()
	f.seed(gone, 1)
	acks := &recordingAcks{}
	h := NewHandler("analytics", NewEraser(f, f, f), acks, nil, nil)
	for _, ev := range []string{EventUserDeletionScheduled, EventUserDeactivated, "user.deletion_requested"} {
		if err := h.Handle(context.Background(), ev, purgePayload(gone)); err != nil {
			t.Fatal(err)
		}
	}
	if len(f.log) != 0 || len(acks.acks) != 0 {
		t.Fatalf("non-purge events erased or acked: log=%v acks=%d", f.log, len(acks.acks))
	}
}
