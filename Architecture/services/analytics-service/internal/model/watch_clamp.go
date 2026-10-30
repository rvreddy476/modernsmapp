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

// ServerDurationToleranceMaxMS caps the slack a watch total is allowed
// over the server-measured duration, per pass, before it is clamped
// (audit M6). post-service's duration can be whole seconds
// (duration_seconds) where the player measures milliseconds, so an honest
// full watch may read a fraction of a second longer than the server's
// figure. The slack is 5% of the duration up to this cap, so a 2s clip
// gets 100ms, not a 50% bonus. It only ever affects watch time:
// percent_viewed is computed against the exact server duration and capped
// at 100, and the session row is clamped to the exact duration again on
// upsert (postgres.applySessionUpdate).
const ServerDurationToleranceMaxMS = 1000

// ServerDurationTolerance is the per-pass slack for a server duration:
// min(5% of it, ServerDurationToleranceMaxMS); 0 for an unknown duration.
func ServerDurationTolerance(serverDurationMS int64) int64 {
	if serverDurationMS <= 0 {
		return 0
	}
	tol := serverDurationMS / 20
	if tol > ServerDurationToleranceMaxMS {
		tol = ServerDurationToleranceMaxMS
	}
	return tol
}

// ClampToServerDuration bounds a reported watch total by the server-known
// duration plus its tolerance, per pass:
//
//	watched_ms <= (server_duration_ms + tolerance) x (loop_count + 1)
//
// An unknown server duration (<= 0) leaves the total untouched; the
// caller must then not qualify the view (see IngestService).
func ClampToServerDuration(reportedMS, serverDurationMS, loops int64) (clampedMS int64, wasClamped bool) {
	if serverDurationMS <= 0 {
		return reportedMS, false
	}
	return ClampWatched(reportedMS, serverDurationMS+ServerDurationTolerance(serverDurationMS), loops)
}
