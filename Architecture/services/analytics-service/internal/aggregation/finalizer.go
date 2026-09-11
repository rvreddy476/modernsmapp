package aggregation

import (
	"context"
	"log"
	"time"
)

// The finaliser's cadence. Sessions are closed when nothing has been
// heard from them for sessionInactivity; the tick is how often that is
// checked, so a lost play_end costs at most tick + inactivity before the
// view is counted.
const (
	finalizerTick     = 60 * time.Second
	sessionInactivity = 10 * time.Minute
	finalizerBatch    = 500
)

// SessionStore is the slice of the PostgreSQL store the finaliser needs.
type SessionStore interface {
	FinalizeInactiveSessions(ctx context.Context, before, now time.Time, batch int) (int, error)
}

// SessionFinalizer closes playback sessions that stopped reporting
// without a play_end (plan Phase 1A, audit M-08).
//
// Before server-side sessions, play_end was the only carrier of a view;
// a tab closed mid-heartbeat, an app killed by the OS, or a queue that
// flushed after the 24-hour window lost the whole view. Now every
// heartbeat advances the session row, and this worker is what turns a
// row that simply went quiet into a finalised view — computing the
// display-view flag and view score at that moment, from the GREATEST'd
// totals the heartbeats left behind.
//
// A play_end that arrives after this has closed a session is not a
// second view: it applies its updates to the same row (the primary key
// guarantees one) and the flags are recomputed there. The aggregators
// rebuild whole buckets from rows, so the correction simply replaces
// the earlier answer.
type SessionFinalizer struct {
	store      SessionStore
	tick       time.Duration
	inactivity time.Duration
	now        func() time.Time
}

func NewSessionFinalizer(store SessionStore) *SessionFinalizer {
	return &SessionFinalizer{
		store:      store,
		tick:       finalizerTick,
		inactivity: sessionInactivity,
		now:        time.Now,
	}
}

// WithClock injects the clock. Tests use it to move "now" past the
// inactivity window without waiting ten minutes.
func (f *SessionFinalizer) WithClock(now func() time.Time) *SessionFinalizer {
	if now != nil {
		f.now = now
	}
	return f
}

// Start runs the finaliser loop. Blocks until ctx is cancelled. Runs
// once on start so a restart does not leave sessions that went quiet
// during the outage waiting a further tick.
func (f *SessionFinalizer) Start(ctx context.Context) {
	ticker := time.NewTicker(f.tick)
	defer ticker.Stop()

	log.Printf("[SessionFinalizer] started (tick=%s, inactivity=%s)", f.tick, f.inactivity)

	f.run(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			f.run(ctx)
		}
	}
}

func (f *SessionFinalizer) run(ctx context.Context) {
	closed, err := f.RunOnce(ctx)
	if err != nil {
		log.Printf("[SessionFinalizer] finalize error: %v", err)
		return
	}
	if closed > 0 {
		log.Printf("[SessionFinalizer] closed %d inactive sessions", closed)
	}
}

// RunOnce closes every session whose last event is older than the
// inactivity window as of the finaliser's clock. Exported so tests and
// operators can tick it deterministically.
func (f *SessionFinalizer) RunOnce(ctx context.Context) (int, error) {
	now := f.now().UTC()
	return f.store.FinalizeInactiveSessions(ctx, now.Add(-f.inactivity), now, finalizerBatch)
}
