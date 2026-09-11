package model

// ClampWatched bounds a reported watch-time total to what the content's
// duration and loop count can account for:
//
//	watched_ms <= content_duration_ms x (loop_count + 1)
//
// This is the one clamp on watch time (plan M-09, M-26). It binds every
// running total a playback session takes — each heartbeat's and the
// play_end's — not only the final figure, so a looped or scrub-rewatched
// session cannot land above the ceiling through GREATEST. Rewatched
// seconds within a session are genuine watch time, but they are bounded
// the same way looping is; the client's figure is kept beside the
// clamped one in watched_ms_reported for audit.
//
// A duration that is not yet known (zero) leaves the total untouched:
// the caller applies the clamp again once the session has snapshotted
// one. A negative loop count reads as zero. The result is monotonic in
// every argument, which is what lets the session upsert keep GREATEST:
// the ceiling only ever grows, so a clamped total never goes down.
func ClampWatched(reportedMS, durationMS, loops int64) (clampedMS int64, wasClamped bool) {
	if durationMS <= 0 || reportedMS <= 0 {
		return reportedMS, false
	}
	if loops < 0 {
		loops = 0
	}
	ceiling := durationMS * (loops + 1)
	if reportedMS > ceiling {
		return ceiling, true
	}
	return reportedMS, false
}
