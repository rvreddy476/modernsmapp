package service

import "time"

// boundTTLMap keeps an in-process TTL cache map from growing without end
// (audit M4: kwCache was never evicted, one entry per viewer since start)
// and from paying an O(n) sweep on every write once full (audit L9: the
// privacy cache swept all 50k entries under its global lock on each write
// while none had expired yet).
//
// Nothing happens until the map holds more than max entries. Then expired
// entries are dropped, and if the map is still above 90% of max, arbitrary
// entries (Go's map order) are dropped down to that mark. The next sweep is
// therefore at least max/10 writes away, so the cost per write is O(10)
// amortised. An evicted entry is only a cache miss: the next read asks the
// source again. The caller holds the map's lock.
func boundTTLMap[K comparable, E any](m map[K]E, max int, now time.Time, expires func(E) time.Time) {
	if len(m) <= max {
		return
	}
	for k, e := range m {
		if now.After(expires(e)) {
			delete(m, k)
		}
	}
	target := max - max/10
	for k := range m {
		if len(m) <= target {
			break
		}
		delete(m, k)
	}
}
