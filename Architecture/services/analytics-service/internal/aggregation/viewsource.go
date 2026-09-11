package aggregation

import "time"

// Where a view comes from, per bucket.
//
// Until server-side playback sessions existed, play_end was the sole
// carrier of a view: the aggregators counted play_end rows in
// events_raw. Sessions (analytics.playback_sessions, plan Phase 1A)
// replace that, but money already settled on the play_end numbers, so
// the switch is per bucket and never retroactive: a bucket that was
// aggregated from play_end before the cutover stays on play_end for
// ever, and a bucket after it reads sessions. Both aggregators ask the
// resolver which source a given bucket start belongs to and keep both
// SQL paths intact.
const (
	ViewSourcePlayEnd  = "play_end"
	ViewSourceSessions = "sessions"
)

// ViewSourceResolver answers ViewSourcePlayEnd or ViewSourceSessions for
// the bucket that starts at bucketStart. cmd/server wires the
// implementation (an environment-driven cutover instant); the
// aggregators are handed it through WithViewSource. A nil resolver means
// play_end everywhere, which is the pre-cutover default the service
// ships with.
type ViewSourceResolver interface {
	ViewSourceFor(bucketStart time.Time) string
}

// resolveViewSource is the one place a resolver's answer is read. Any
// answer that is not exactly ViewSourceSessions is play_end: a
// misspelled or empty source must never silently switch money onto a
// table that may not be populated.
func resolveViewSource(r ViewSourceResolver, bucketStart time.Time) string {
	if r == nil {
		return ViewSourcePlayEnd
	}
	if r.ViewSourceFor(bucketStart.UTC()) == ViewSourceSessions {
		return ViewSourceSessions
	}
	return ViewSourcePlayEnd
}
