// Package viewsource says where the aggregators read views from during
// the cutover from play_end rows to server-side playback sessions
// (plan Phase 1, deploy sequence).
//
// The switch is an environment variable, ANALYTICS_VIEW_SOURCE, read
// once at startup by cmd/server and installed here. It defaults to
// play_end so that shipping the sessions table changes nothing the
// money reads until the backfill has run and the two aggregations have
// reconciled to zero per hour bucket. Only then is it flipped to
// sessions and the service restarted.
//
// The aggregation package consults Current() (or is handed a *Resolver,
// which satisfies its ViewSourceResolver interface) and asks per bucket.
// Today the answer is the same for every bucket; the per-bucket shape
// exists so a later cutover can be dated rather than global without
// changing the callers.
package viewsource

import (
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// PlayEnd: views_display, view_score_total and watch time come from
	// play_end rows in events_raw, as they always have.
	PlayEnd = "play_end"
	// Sessions: the same figures come from finalised rows in
	// analytics.playback_sessions.
	Sessions = "sessions"

	EnvVar = "ANALYTICS_VIEW_SOURCE"
)

// Resolver answers which source applies to a bucket.
type Resolver struct {
	source string
}

// New validates the source name. Empty means the default, play_end.
func New(source string) (*Resolver, error) {
	source = strings.ToLower(strings.TrimSpace(source))
	switch source {
	case "":
		source = PlayEnd
	case PlayEnd, Sessions:
	default:
		return nil, fmt.Errorf("%s=%q: must be %q or %q", EnvVar, source, PlayEnd, Sessions)
	}
	return &Resolver{source: source}, nil
}

// FromEnv reads ANALYTICS_VIEW_SOURCE.
func FromEnv() (*Resolver, error) {
	return New(os.Getenv(EnvVar))
}

// ViewSourceFor is the per-bucket answer the aggregators ask for.
func (r *Resolver) ViewSourceFor(bucketStart time.Time) string {
	_ = bucketStart
	return r.source
}

// Source is the configured value, for the startup log line.
func (r *Resolver) Source() string {
	return r.source
}

var current atomic.Pointer[Resolver]

// Install makes r the process-wide resolver returned by Current.
func Install(r *Resolver) {
	if r != nil {
		current.Store(r)
	}
}

// Current returns the installed resolver, or the play_end default when
// nothing has been installed (tests, tools). Never nil.
func Current() *Resolver {
	if r := current.Load(); r != nil {
		return r
	}
	return &Resolver{source: PlayEnd}
}
