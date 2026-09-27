package postgres

import "math"

// Audience retention from coverage bitmaps (MTube, 2026-09-27).
//
// A session's coverage bitmap has one bit per second of the content, set
// when a heartbeat covered that second (markCoverage). Retention at a point
// in the video is the share of sessions that covered it. RetentionBuckets
// normalises every session's duration onto `buckets` equal slices and
// reports, per slice, the mean covered fraction across sessions — so a
// 30-second flick and a two-hour film both come back as 100 numbers in
// [0, 1].
//
// Sessions with no bitmap (backfilled from play_end, or never sent a
// heartbeat) say nothing about WHERE the viewer was and are left out of
// the curve rather than counted as "watched nothing". Sessions with no
// known duration likewise.

// RetentionBuckets computes the curve. It returns `buckets` zeros when no
// session qualifies, never nil, so the wire shape is stable.
func RetentionBuckets(sessions []SessionCoverage, buckets int) []float64 {
	if buckets <= 0 {
		buckets = 100
	}
	sum := make([]float64, buckets)
	var counted int
	for _, s := range sessions {
		if s.DurationMS <= 0 || len(s.Coverage) == 0 {
			continue
		}
		totalSeconds := (s.DurationMS + 999) / 1000
		if totalSeconds <= 0 {
			continue
		}
		counted++
		for b := 0; b < buckets; b++ {
			// [from, to) in seconds; at least one second per bucket so
			// short content still samples every bucket.
			from := int64(math.Floor(float64(b) * float64(totalSeconds) / float64(buckets)))
			to := int64(math.Floor(float64(b+1) * float64(totalSeconds) / float64(buckets)))
			if to <= from {
				to = from + 1
			}
			if to > totalSeconds {
				to = totalSeconds
			}
			if from >= totalSeconds {
				from = totalSeconds - 1
			}
			var covered int64
			for sec := from; sec < to; sec++ {
				if coverageBit(s.Coverage, sec) {
					covered++
				}
			}
			sum[b] += float64(covered) / float64(to-from)
		}
	}
	out := make([]float64, buckets)
	if counted == 0 {
		return out
	}
	for b := range out {
		out[b] = math.Round(sum[b]/float64(counted)*10000) / 10000
	}
	return out
}

func coverageBit(bitmap []byte, sec int64) bool {
	i := int(sec / 8)
	if i < 0 || i >= len(bitmap) {
		return false
	}
	return bitmap[i]&(1<<uint(sec%8)) != 0
}

// SessionAverages is the mean watched time and the mean unique coverage
// over the finalized sessions that qualify for the averages: every session
// with a known duration counts here (a session that never heartbeat still
// watched its watched_ms), which is a wider set than the retention curve.
func SessionAverages(sessions []SessionCoverage) (avgWatchedMS float64, avgPercentCovered float64) {
	var n int
	var watched, percent float64
	for _, s := range sessions {
		if s.DurationMS <= 0 {
			continue
		}
		n++
		watched += float64(s.WatchedMS)
		percent += math.Min(math.Max(s.PercentCovered, 0), 100)
	}
	if n == 0 {
		return 0, 0
	}
	return math.Round(watched/float64(n)*100) / 100, math.Round(percent/float64(n)*100) / 100
}
