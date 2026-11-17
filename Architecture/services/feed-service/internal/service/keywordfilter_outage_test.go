package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// A trust-safety outage used to fail every page on every surface (502
// HYDRATION_FAILED), for every viewer, although almost nobody has a filter
// keyword. These pin what a page does now: it is served, filtered with the
// viewer's last known keywords; only a viewer with none known is served
// without the keyword filter.

// trustStub is trust-safety's keyword endpoint with a switch: up answers
// the current keywords, down answers 503.
type trustStub struct {
	mu       sync.Mutex
	up       bool
	keywords []string
	calls    int
	srv      *httptest.Server
}

func newTrustStub(t *testing.T, keywords ...string) *trustStub {
	t.Helper()
	st := &trustStub{up: true, keywords: keywords}
	st.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st.mu.Lock()
		defer st.mu.Unlock()
		st.calls++
		if !st.up {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		kws := st.keywords
		if kws == nil {
			kws = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"keywords": kws}})
	}))
	t.Cleanup(st.srv.Close)
	return st
}

func (st *trustStub) set(up bool, keywords ...string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.up = up
	st.keywords = keywords
}

func outageService(st *trustStub, rdb *redis.Client) *Service {
	return &Service{trustSafetyURL: st.srv.URL, trustClient: &http.Client{Timeout: 2 * time.Second}, rdb: rdb}
}

// expireKeywordCache makes the viewer's cache entry older than its 60 s
// freshness, as it is a minute after the last good answer.
func expireKeywordCache(s *Service, viewer uuid.UUID) {
	s.kwMu.Lock()
	defer s.kwMu.Unlock()
	e := s.kwCache[viewer]
	e.expires = time.Now().Add(-time.Second)
	s.kwCache[viewer] = e
}

func twoPosts() []HydratedPost {
	return []HydratedPost{hydratedWithText("huge spoilers inside"), hydratedWithText("a calm day")}
}

func filteredTexts(t *testing.T, s *Service, viewer uuid.UUID) []string {
	t.Helper()
	out, err := s.applyKeywordHideFilter(context.Background(), viewer, twoPosts())
	if err != nil {
		t.Fatalf("the page failed: %v (a trust-safety outage must not fail a page)", err)
	}
	texts := make([]string, 0, len(out))
	for _, p := range out {
		texts = append(texts, p.Text)
	}
	return texts
}

// The same process, a minute after its last good answer: the expired entry
// is the viewer's last known set and keeps filtering.
func TestKeywordOutage_UsesThisProcessLastKnownKeywords(t *testing.T) {
	st := newTrustStub(t, "spoilers")
	s := outageService(st, nil)
	viewer := uuid.New()

	if got := filteredTexts(t, s, viewer); len(got) != 1 {
		t.Fatalf("with trust-safety up: served %v, want the spoiler post dropped", got)
	}

	st.set(false)
	expireKeywordCache(s, viewer)

	got := filteredTexts(t, s, viewer)
	if len(got) != 1 || got[0] != "a calm day" {
		t.Fatalf("during the outage: served %v; the viewer's keyword stopped hiding posts", got)
	}
	if _, source := s.keywordsOrLastKnown(context.Background(), viewer); source != keywordSourceStaleMemory {
		t.Fatalf("source = %q, want %q", source, keywordSourceStaleMemory)
	}
}

// Another pod, or this one after a restart: nothing in memory, so the copy
// in Redis is the last known set.
func TestKeywordOutage_UsesTheRedisCopyAfterARestart(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	st := newTrustStub(t, "spoilers")
	viewer := uuid.New()

	before := outageService(st, rdb)
	filteredTexts(t, before, viewer) // a good answer; stores the copy
	if !mr.Exists(keywordLastKnownKey(viewer)) {
		t.Fatal("a non-empty keyword set was not stored for later outages")
	}
	if ttl := mr.TTL(keywordLastKnownKey(viewer)); ttl <= 0 || ttl > keywordLastKnownTTL {
		t.Fatalf("stored copy TTL = %v, want within %v", ttl, keywordLastKnownTTL)
	}

	st.set(false)
	after := outageService(st, rdb) // a fresh process: empty in-memory cache

	got := filteredTexts(t, after, viewer)
	if len(got) != 1 || got[0] != "a calm day" {
		t.Fatalf("after a restart during the outage: served %v; the viewer's keyword was forgotten", got)
	}
	if _, source := after.keywordsOrLastKnown(context.Background(), viewer); source != keywordSourceStaleRedis {
		t.Fatalf("source = %q, want %q", source, keywordSourceStaleRedis)
	}
}

// A viewer nobody has an answer for is served, without the keyword filter.
// This is the case that used to take the feed down for everyone.
func TestKeywordOutage_UnknownViewerIsServedUnfiltered(t *testing.T) {
	mr := miniredis.RunT(t)
	st := newTrustStub(t)
	st.set(false)
	s := outageService(st, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	viewer := uuid.New()

	if got := filteredTexts(t, s, viewer); len(got) != 2 {
		t.Fatalf("served %v, want both posts", got)
	}
	if _, source := s.keywordsOrLastKnown(context.Background(), viewer); source != keywordSourceNone {
		t.Fatalf("source = %q, want %q", source, keywordSourceNone)
	}
	// The failure is not remembered as an answer: the next request asks again.
	st.mu.Lock()
	calls := st.calls
	st.mu.Unlock()
	if calls < 2 {
		t.Fatalf("trust-safety was asked %d time(s) over two requests; a failure must not be cached", calls)
	}
}

// No Redis at all (a unit-test or degraded process) is the same as no copy.
func TestKeywordOutage_NoRedisIsServedUnfiltered(t *testing.T) {
	st := newTrustStub(t)
	st.set(false)
	if got := filteredTexts(t, outageService(st, nil), uuid.New()); len(got) != 2 {
		t.Fatalf("served %v, want both posts", got)
	}
}

// When trust-safety answers again its answer wins over the last known set:
// a keyword removed meanwhile stops hiding posts.
func TestKeywordOutage_FreshAnswerWinsWhenTrustSafetyReturns(t *testing.T) {
	st := newTrustStub(t, "spoilers")
	s := outageService(st, nil)
	viewer := uuid.New()
	filteredTexts(t, s, viewer)

	st.set(false)
	expireKeywordCache(s, viewer)
	if got := filteredTexts(t, s, viewer); len(got) != 1 {
		t.Fatalf("during the outage: served %v, want the last known keyword applied", got)
	}

	st.set(true) // back, and the viewer has removed the keyword
	if got := filteredTexts(t, s, viewer); len(got) != 2 {
		t.Fatalf("after recovery: served %v, want both posts (the keyword is gone)", got)
	}
	if _, source := s.keywordsOrLastKnown(context.Background(), viewer); source != keywordSourceFresh {
		t.Fatalf("source = %q, want %q", source, keywordSourceFresh)
	}
}

// Only non-empty sets are stored, and a set that becomes empty removes its
// copy, so a removed keyword cannot come back in a later outage.
func TestKeywordLastKnown_EmptySetStoresNothingAndClearsTheCopy(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	st := newTrustStub(t)
	s := outageService(st, rdb)
	viewer := uuid.New()

	filteredTexts(t, s, viewer)
	if mr.Exists(keywordLastKnownKey(viewer)) {
		t.Fatal("an empty keyword set was stored; the common case must cost Redis nothing")
	}

	st.set(true, "spoilers")
	expireKeywordCache(s, viewer)
	filteredTexts(t, s, viewer)
	if !mr.Exists(keywordLastKnownKey(viewer)) {
		t.Fatal("the new keyword was not stored")
	}

	st.set(true) // the viewer removes it
	expireKeywordCache(s, viewer)
	filteredTexts(t, s, viewer)
	if mr.Exists(keywordLastKnownKey(viewer)) {
		t.Fatal("a removed keyword is still stored and would hide posts in the next outage")
	}
}

// A copy in Redis that does not decode is no copy; it never fails the page.
func TestKeywordOutage_DamagedRedisCopyIsIgnored(t *testing.T) {
	mr := miniredis.RunT(t)
	st := newTrustStub(t)
	st.set(false)
	viewer := uuid.New()
	if err := mr.Set(keywordLastKnownKey(viewer), "{not json"); err != nil {
		t.Fatal(err)
	}
	s := outageService(st, redis.NewClient(&redis.Options{Addr: mr.Addr()}))
	if got := filteredTexts(t, s, viewer); len(got) != 2 {
		t.Fatalf("served %v, want both posts", got)
	}
}
