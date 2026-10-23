package actionrecheck

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/actionlinks"
	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/service/actionfake"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

type scripted struct {
	reports []service.ActionRecheckReport
	err     error
	calls   int
}

func (s *scripted) RecheckActionLinks(context.Context, int) (service.ActionRecheckReport, error) {
	s.calls++
	if s.err != nil {
		return service.ActionRecheckReport{}, s.err
	}
	if s.calls > len(s.reports) {
		return service.ActionRecheckReport{}, nil
	}
	return s.reports[s.calls-1], nil
}

// One tick drains full batches, stops at a short one, at an all-failed one
// and at MaxBatches, and stops on a claim error.
func TestTickBounds(t *testing.T) {
	full := service.ActionRecheckReport{Checked: 10}
	for _, tc := range []struct {
		name  string
		s     *scripted
		calls int
	}{
		{"short batch ends the tick", &scripted{reports: []service.ActionRecheckReport{full, {Checked: 3}}}, 2},
		{"checker down ends the tick", &scripted{reports: []service.ActionRecheckReport{{Unavailable: 10}}}, 1},
		{"at most MaxBatches", &scripted{reports: []service.ActionRecheckReport{full, full, full, full, full}}, 3},
		{"claim error ends the tick", &scripted{err: errors.New("db")}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := NewWorker(tc.s, Config{Interval: time.Hour, Batch: 10, MaxBatches: 3}, nil)
			w.Tick(context.Background())
			if tc.s.calls != tc.calls {
				t.Fatalf("batches = %d, want %d", tc.s.calls, tc.calls)
			}
		})
	}
}

// End to end over the service and the fake store: a link that turned unsafe
// is blocked by the tick; a fresh verdict is left alone.
func TestTickBlocksALinkThatTurnedUnsafe(t *testing.T) {
	now := time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)
	store := actionfake.New()
	store.Now = func() time.Time { return now }
	old, fresh := now.Add(-48*time.Hour), now.Add(-time.Hour)
	post := uuid.New()
	bad := store.Insert(postgres.ActionLink{PostID: &post, AuthorID: uuid.New(), URL: "https://testsafebrowsing.appspot.com/s/malware.html",
		Domain: "testsafebrowsing.appspot.com", Label: "download", SafetyStatus: "safe", SafetyCheckedAt: &old})
	post2 := uuid.New()
	recent := store.Insert(postgres.ActionLink{PostID: &post2, AuthorID: uuid.New(), URL: "https://testsafebrowsing.appspot.com/s/phishing.html",
		Domain: "testsafebrowsing.appspot.com", Label: "download", SafetyStatus: "safe", SafetyCheckedAt: &fresh})
	svc := service.NewForHandlerTests(service.HandlerTestDeps{Now: func() time.Time { return now }})
	svc.SetActionLinks(service.ActionLinkConfig{Checker: &actionlinks.Mock{}, Store: store})

	rep := NewWorker(svc, Config{Batch: 50}, nil).Tick(context.Background())
	if rep.Checked != 1 || rep.Blocked != 1 {
		t.Fatalf("report = %+v", rep)
	}
	if l := store.Get(bad.ID); l.Status != postgres.ActionStatusBlocked || l.StatusReason == nil || *l.StatusReason != postgres.ActionReasonUnsafe {
		t.Fatalf("turned-unsafe link = %+v", l)
	}
	if l := store.Get(recent.ID); l.Status != postgres.ActionStatusActive {
		t.Fatalf("a fresh verdict was rechecked: %+v", l)
	}
}
