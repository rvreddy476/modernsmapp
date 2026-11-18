package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/subscribers"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// lv-21: every fan-out counter and gauge the alert rules read is registered
// and moves when its event happens.

func counterValue(t *testing.T, c prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	if err := c.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetCounter().GetValue()
}

func gaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	var m dto.Metric
	if err := g.Write(&m); err != nil {
		t.Fatal(err)
	}
	return m.GetGauge().GetValue()
}

func settledValue(t *testing.T, kind, outcome, reason string) float64 {
	return counterValue(t, fanoutJobsSettled.WithLabelValues(kind, outcome, reason))
}

func recipientValue(t *testing.T, kind, outcome string) float64 {
	return counterValue(t, fanoutRecipients.WithLabelValues(kind, outcome))
}

func TestFanoutMetrics_SettleCountsEachOutcome(t *testing.T) {
	f := newSubscriberFanoutWith(&fakeNotifier{}, newFakeStore(), &fakeSource{})
	ctx := context.Background()
	job := uploadJob(uuid.New(), uuid.New())

	cases := []struct {
		name            string
		err             error
		waits           int
		outcome, reason string
	}{
		{"done", nil, 0, fanoutSettledCompleted, fanoutReasonNone},
		{"skip", &fanoutSkipError{reason: "members_only"}, 0, fanoutSettledSkipped, "members_only"},
		{"skip, unknown reason", &fanoutSkipError{reason: "brand_new"}, 0, fanoutSettledSkipped, "other"},
		{"wait", &fanoutWaitError{reason: "processing"}, 0, fanoutSettledDeferred, "processing"},
		{"wait on review", &fanoutWaitError{reason: "review_pending"}, 0, fanoutSettledDeferred, "review"},
		{"wait past the ceiling", &fanoutWaitError{reason: "visibility_private"}, fanoutMaxWaits, fanoutSettledExpired, "visibility"},
		{"transient error", errors.New("graph down"), 0, fanoutSettledRetried, fanoutReasonError},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			before := settledValue(t, fanoutKindUpload, tc.outcome, tc.reason)
			j := *job
			j.Waits = tc.waits
			f.settle(ctx, &j, tc.err)
			if got := settledValue(t, fanoutKindUpload, tc.outcome, tc.reason) - before; got != 1 {
				t.Fatalf("fanout_jobs_settled_total{upload,%s,%s} moved by %v, want 1", tc.outcome, tc.reason, got)
			}
		})
	}
}

// claimOnce hands drainOnce one job, then nothing.
type claimOnce struct {
	*fakeStore
	jobs []postgres.FanoutJob
}

func (c *claimOnce) ClaimFanoutJobs(context.Context, time.Duration, int) ([]postgres.FanoutJob, error) {
	j := c.jobs
	c.jobs = nil
	return j, nil
}

func TestFanoutMetrics_AttemptsExhaustedCountsFailed(t *testing.T) {
	job := *uploadJob(uuid.New(), uuid.New())
	job.Attempts = fanoutMaxAttempts + 1
	job.NotifType = LiveNotifType
	store := &claimOnce{fakeStore: newFakeStore(), jobs: []postgres.FanoutJob{job}}
	f := newSubscriberFanoutWith(&fakeNotifier{}, store, &fakeSource{})

	before := settledValue(t, fanoutKindLive, fanoutSettledFailed, fanoutReasonAttemptsExhausted)
	if err := f.drainOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := settledValue(t, fanoutKindLive, fanoutSettledFailed, fanoutReasonAttemptsExhausted) - before; got != 1 {
		t.Fatalf("failed moved by %v, want 1", got)
	}
	if len(store.outcomes) != 1 || store.outcomes[0] != "failed" {
		t.Fatalf("store outcomes %v, want [failed]", store.outcomes)
	}
}

func TestFanoutMetrics_RecipientsByOutcome(t *testing.T) {
	author, channel := uuid.New(), uuid.New()
	told, fresh, failing, already := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	store := newFakeStore()
	store.delivered[already] = true
	notifier := &fakeNotifier{failFor: failing}
	src := &fakeSource{pages: []*subscribers.Page{{IDs: []uuid.UUID{author, told, fresh, failing, already}, NextAfter: already}}}
	f := newSubscriberFanoutWith(notifier, store, src)

	want := map[string]float64{
		fanoutRecipientNotified: 2,
		fanoutRecipientSelf:     1,
		fanoutRecipientAlready:  1,
		fanoutRecipientFailed:   1,
	}
	before := map[string]float64{}
	for o := range want {
		before[o] = recipientValue(t, fanoutKindUpload, o)
	}
	if err := f.processJob(context.Background(), uploadJob(author, channel)); err == nil {
		t.Fatal("a failed recipient must fail the page")
	}
	for o, n := range want {
		if got := recipientValue(t, fanoutKindUpload, o) - before[o]; got != n {
			t.Errorf("fanout_recipients_total{upload,%s} moved by %v, want %v", o, got, n)
		}
	}
}

// replayStore answers the replay sweep.
type replayStore struct {
	*fakeStore
	n   int64
	err error
}

func (r *replayStore) RequeueFailedFanoutJobs(context.Context, int, time.Duration, time.Duration) (int64, error) {
	return r.n, r.err
}

func TestFanoutMetrics_ReplaysAndEnqueues(t *testing.T) {
	ctx := context.Background()
	rs := &replayStore{fakeStore: newFakeStore(), n: 3}
	f := newSubscriberFanoutWith(&fakeNotifier{}, rs, &fakeSource{})

	jobs, errs := counterValue(t, fanoutReplayedJobs), counterValue(t, fanoutReplayErrors)
	f.replayFailed(ctx)
	rs.err = errors.New("db down")
	f.replayFailed(ctx)
	if got := counterValue(t, fanoutReplayedJobs) - jobs; got != 3 {
		t.Fatalf("replayed jobs moved by %v, want 3", got)
	}
	if got := counterValue(t, fanoutReplayErrors) - errs; got != 1 {
		t.Fatalf("replay errors moved by %v, want 1", got)
	}

	ok := counterValue(t, fanoutJobsEnqueued.WithLabelValues(fanoutKindUpload, "ok"))
	if err := f.Enqueue(ctx, EnqueueParams{PostID: uuid.New(), NotifType: "creator_uploaded_video"}); err != nil {
		t.Fatal(err)
	}
	if got := counterValue(t, fanoutJobsEnqueued.WithLabelValues(fanoutKindUpload, "ok")) - ok; got != 1 {
		t.Fatalf("enqueued{upload,ok} moved by %v, want 1", got)
	}
}

// statsStore answers the queue gauge read.
type statsStore struct {
	*fakeStore
	st  postgres.FanoutQueueStats
	err error
}

func (s *statsStore) FanoutQueueStats(context.Context) (postgres.FanoutQueueStats, error) {
	return s.st, s.err
}

func TestFanoutMetrics_QueueGauges(t *testing.T) {
	ss := &statsStore{fakeStore: newFakeStore(), st: postgres.FanoutQueueStats{
		Due: 4, OldestDue: 20 * time.Minute, Waiting: 2, OldestWaiting: 3 * time.Hour,
		Backoff: 1, Running: 5, OldestRunning: 90 * time.Second,
	}}
	f := newSubscriberFanoutWith(&fakeNotifier{}, ss, &fakeSource{})
	f.sampleFanoutQueue(context.Background())

	for state, want := range map[string]float64{fanoutQueueDue: 1200, fanoutQueueWaiting: 10800, fanoutQueueRunning: 90} {
		if got := gaugeValue(t, fanoutQueueOldest.WithLabelValues(state)); got != want {
			t.Errorf("fanout_queue_oldest_seconds{%s} = %v, want %v", state, got, want)
		}
	}
	for state, want := range map[string]float64{fanoutQueueDue: 4, fanoutQueueWaiting: 2, fanoutQueueBackoff: 1, fanoutQueueRunning: 5} {
		if got := gaugeValue(t, fanoutQueueJobs.WithLabelValues(state)); got != want {
			t.Errorf("fanout_queue_jobs{%s} = %v, want %v", state, got, want)
		}
	}

	// A failed read keeps the last values and is counted.
	errs := counterValue(t, fanoutQueueReadErrors)
	ss.err = errors.New("db down")
	f.sampleFanoutQueue(context.Background())
	if got := counterValue(t, fanoutQueueReadErrors) - errs; got != 1 {
		t.Fatalf("read errors moved by %v, want 1", got)
	}
	if got := gaugeValue(t, fanoutQueueOldest.WithLabelValues(fanoutQueueDue)); got != 1200 {
		t.Fatalf("a failed read changed the gauge to %v", got)
	}
}

// The alerted series exist at zero from the start, so increase() sees the
// first failure after a restart.
func TestFanoutMetrics_PrecreatesAlertedSeries(t *testing.T) {
	precreateFanoutMetrics()
	fams, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]map[string]string{
		"atpost_notification_service_fanout_jobs_settled_total":  {"kind": "upload", "outcome": "failed", "reason": "attempts_exhausted"},
		"atpost_notification_service_fanout_queue_oldest_seconds": {"state": "due"},
	}
	for name, labels := range want {
		found := false
		for _, mf := range fams {
			if mf.GetName() != name {
				continue
			}
			for _, m := range mf.GetMetric() {
				match := true
				for _, lp := range m.GetLabel() {
					if v, ok := labels[lp.GetName()]; ok && v != lp.GetValue() {
						match = false
					}
				}
				if match {
					found = true
				}
			}
		}
		if !found {
			t.Errorf("%s%v is not registered at startup", name, labels)
		}
	}
}
