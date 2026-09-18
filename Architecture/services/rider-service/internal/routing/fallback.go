package routing

import (
	"context"
	"log/slog"
	"sync"
)

// Fallback answers from Primary (Google) when it is configured and from
// Secondary (Haversine) otherwise or whenever Primary fails: any error,
// timeout, non-2xx or empty route. It never passes a Primary failure to the
// caller. Each failure class is logged once per process and every failure is
// counted (Failures).
type Fallback struct {
	primary   Router
	secondary Router
	logger    *slog.Logger

	mu     sync.Mutex
	logged map[string]bool
	counts map[string]int64
}

// NewFallback builds the wrapper. A nil primary means haversine only (no
// GOOGLE_MAPS_SERVER_KEY); a nil logger uses slog.Default().
func NewFallback(primary Router, secondary Haversine, logger *slog.Logger) *Fallback {
	if logger == nil {
		logger = slog.Default()
	}
	return &Fallback{
		primary: primary, secondary: secondary, logger: logger,
		logged: map[string]bool{}, counts: map[string]int64{},
	}
}

// Route implements Router. The only error it returns is the secondary's,
// which Haversine gives only for out-of-range coordinates.
func (f *Fallback) Route(ctx context.Context, from, to LatLng) (Route, error) {
	if f.primary != nil {
		r, err := f.primary.Route(ctx, from, to)
		if err == nil {
			return r, nil
		}
		f.record(err)
	}
	return f.secondary.Route(ctx, from, to)
}

func (f *Fallback) record(err error) {
	class := ClassOf(err)
	f.mu.Lock()
	f.counts[class]++
	first := !f.logged[class]
	f.logged[class] = true
	f.mu.Unlock()
	if first {
		f.logger.Warn("rider-service: google routes failed; quotes use the haversine estimate "+
			"(logged once per failure class)",
			"class", class, "error", err.Error())
	}
}

// Failures returns a copy of the per-class failure counts.
func (f *Fallback) Failures() map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]int64, len(f.counts))
	for k, v := range f.counts {
		out[k] = v
	}
	return out
}
