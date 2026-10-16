package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/google/uuid"
)

// followers_delta on creator/me: null means "Not tracked yet" to the web
// and Android Creator Hub, so null and 0 must never be confused.

type emptyCreatorStore struct{}

func (emptyCreatorStore) GetCreatorAggStats(context.Context, uuid.UUID, time.Time) (*postgres.CreatorAggStats, error) {
	return &postgres.CreatorAggStats{}, nil
}
func (emptyCreatorStore) GetCreatorDailyTrend(context.Context, uuid.UUID, time.Time) ([]postgres.DailySummaryRow, error) {
	return nil, nil
}
func (emptyCreatorStore) GetContentList(context.Context, uuid.UUID, int, time.Time, string) ([]postgres.ContentSummary, error) {
	return nil, nil
}
func (emptyCreatorStore) GetCreatorPeriodTotals(context.Context, uuid.UUID, time.Time, time.Time) (*postgres.CreatorPeriodTotals, error) {
	return &postgres.CreatorPeriodTotals{}, nil
}
func (emptyCreatorStore) GetCreatorTopContent(context.Context, uuid.UUID, time.Time, time.Time, int) ([]postgres.ContentPeriodStat, error) {
	return nil, nil
}
func (emptyCreatorStore) GetCreatorHourlyViews(_ context.Context, _ uuid.UUID, _ time.Time, hours int) ([]int64, error) {
	return make([]int64, hours), nil
}

type fakeFollowerReader struct {
	delta      int64
	tracked    bool
	err        error
	since, now time.Time
	creator    uuid.UUID
}

func (f *fakeFollowerReader) CreatorFollowersDelta(_ context.Context, creatorID uuid.UUID, since, now time.Time) (int64, bool, error) {
	f.creator, f.since, f.now = creatorID, since, now
	return f.delta, f.tracked, f.err
}

var followersNow = time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)

func followersDeltaJSON(t *testing.T, s *CreatorService, period string) string {
	t.Helper()
	stats, err := s.GetStats(context.Background(), uuid.New(), period)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stats)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	v, ok := m["followers_delta"]
	if !ok {
		t.Fatalf("followers_delta key missing: %s", raw)
	}
	return strings.TrimSpace(string(v))
}

func newFollowersService(r FollowerDeltaReader) *CreatorService {
	s := NewCreatorServiceFrom(emptyCreatorStore{}).WithClock(func() time.Time { return followersNow })
	if r != nil {
		s = s.WithFollowerDeltas(r)
	}
	return s
}

func TestFollowersDeltaNullWhenNotWired(t *testing.T) {
	if got := followersDeltaJSON(t, newFollowersService(nil), "28d"); got != "null" {
		t.Fatalf("followers_delta = %s, want null without a follower reader", got)
	}
}

func TestFollowersDeltaNullWhenTrackingHasNotStarted(t *testing.T) {
	r := &fakeFollowerReader{delta: 5, tracked: false}
	if got := followersDeltaJSON(t, newFollowersService(r), "28d"); got != "null" {
		t.Fatalf("followers_delta = %s, want null before tracking starts", got)
	}
}

func TestFollowersDeltaNullWhenTheReadFails(t *testing.T) {
	r := &fakeFollowerReader{tracked: true, err: errors.New("relation does not exist")}
	if got := followersDeltaJSON(t, newFollowersService(r), "28d"); got != "null" {
		t.Fatalf("followers_delta = %s, want null when unavailable", got)
	}
}

func TestFollowersDeltaZeroOnceTracked(t *testing.T) {
	r := &fakeFollowerReader{delta: 0, tracked: true}
	if got := followersDeltaJSON(t, newFollowersService(r), "28d"); got != "0" {
		t.Fatalf("followers_delta = %s, want 0 for a tracked creator with no activity", got)
	}
}

func TestFollowersDeltaNetValueAndWindow(t *testing.T) {
	for period, days := range map[string]int{"7d": 7, "28d": 28, "90d": 90} {
		r := &fakeFollowerReader{delta: -3, tracked: true}
		if got := followersDeltaJSON(t, newFollowersService(r), period); got != "-3" {
			t.Fatalf("%s: followers_delta = %s, want -3 (net loss is reported, not floored)", period, got)
		}
		if !r.now.Equal(followersNow) || !r.since.Equal(followersNow.AddDate(0, 0, -days)) {
			t.Fatalf("%s: window = [%v, %v], want the period's [%v, %v]", period, r.since, r.now, followersNow.AddDate(0, 0, -days), followersNow)
		}
	}
}
