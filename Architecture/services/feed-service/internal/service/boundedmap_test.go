package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Audit M4 / L9: the in-process TTL caches are bounded, and a full cache
// is not swept on every write.
func TestBoundTTLMap_BoundsAndAmortises(t *testing.T) {
	now := time.Now()
	exp := func(e time.Time) time.Time { return e }
	m := map[int]time.Time{}
	sweeps := 0
	for i := 0; i < 5000; i++ {
		m[i] = now.Add(time.Minute) // nothing ever expires
		before := len(m)
		boundTTLMap(m, 1000, now, exp)
		if len(m) > 1000 {
			t.Fatalf("map holds %d entries, cap 1000", len(m))
		}
		if len(m) < before {
			sweeps++
		}
	}
	// 5000 writes into a 1000-entry cap trimming to 900: about one sweep
	// per 100 writes, not one per write.
	if sweeps > 50 {
		t.Fatalf("%d sweeps for 5000 writes: a full cache is swept on every write", sweeps)
	}
}

func TestBoundTTLMap_DropsExpiredFirstAndLeavesSmallMapsAlone(t *testing.T) {
	now := time.Now()
	exp := func(e time.Time) time.Time { return e }
	m := map[int]time.Time{1: now.Add(-time.Second), 2: now.Add(time.Minute)}
	boundTTLMap(m, 10, now, exp)
	if len(m) != 2 {
		t.Fatal("a map under its cap was touched")
	}
	m = map[int]time.Time{}
	for i := 0; i < 11; i++ {
		m[i] = now.Add(-time.Second)
	}
	m[100] = now.Add(time.Minute)
	boundTTLMap(m, 10, now, exp)
	if _, ok := m[100]; !ok || len(m) != 1 {
		t.Fatalf("expired entries were not the ones dropped: %d left", len(m))
	}
}

func TestKeywordCacheIsBounded(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"keywords":[]}}`))
	}))
	defer ts.Close()
	s := &Service{trustSafetyURL: ts.URL, trustClient: ts.Client(), kwCache: map[uuid.UUID]keywordCacheEntry{}}
	live := time.Now().Add(time.Hour)
	for i := 0; i < keywordCacheMaxEntries; i++ {
		s.kwCache[uuid.New()] = keywordCacheEntry{expires: live}
	}
	if _, err := s.getFilterKeywords(context.Background(), uuid.New()); err != nil {
		t.Fatal(err)
	}
	if len(s.kwCache) > keywordCacheMaxEntries {
		t.Fatalf("keyword cache holds %d viewers, cap %d", len(s.kwCache), keywordCacheMaxEntries)
	}
}
