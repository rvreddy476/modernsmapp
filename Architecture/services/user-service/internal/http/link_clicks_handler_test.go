package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The signed-in link click counter: one click per viewer per link per hour,
// never the owner's, nothing when Redis fails, 401 signed out.

type fakeLinkClicks struct {
	mu     sync.Mutex
	links  map[string]bool // owner|platform
	counts map[string]int
}

func newFakeLinkClicks() *fakeLinkClicks {
	return &fakeLinkClicks{
		links:  map[string]bool{pvOwnerID.String() + "|website": true},
		counts: map[string]int{},
	}
}

func (f *fakeLinkClicks) UserLinkExists(_ context.Context, owner uuid.UUID, platform string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.links[owner.String()+"|"+platform], nil
}

func (f *fakeLinkClicks) IncrementLinkClick(_ context.Context, owner uuid.UUID, platform string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.counts[owner.String()+"|"+platform]++
	return nil
}

func (f *fakeLinkClicks) count(platform string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.counts[pvOwnerID.String()+"|"+platform]
}

// fakeDedupe is SET NX with a TTL against a settable clock.
type fakeDedupe struct {
	mu   sync.Mutex
	now  time.Time
	keys map[string]time.Time
	err  error
	ttls []time.Duration
}

func newFakeDedupe() *fakeDedupe {
	return &fakeDedupe{now: time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC), keys: map[string]time.Time{}}
}

func (d *fakeDedupe) FirstClick(_ context.Context, key string, window time.Duration) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return false, d.err
	}
	d.ttls = append(d.ttls, window)
	if exp, ok := d.keys[key]; ok && d.now.Before(exp) {
		return false, nil
	}
	d.keys[key] = d.now.Add(window)
	return true, nil
}

func (d *fakeDedupe) advance(by time.Duration) {
	d.mu.Lock()
	d.now = d.now.Add(by)
	d.mu.Unlock()
}

func linkClickRouter(s linkClickStore, d clickDeduper, b blockChecker) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{users: newPrivacyFixture(), pageAdmins: map[string]bool{}}
	h.WithInternalRoutes(pvInternalKey)
	h.WithBlockChecker(b)
	h.withLinkClicks(s, d)
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

func clickLink(r *gin.Engine, owner uuid.UUID, platform string, viewer *uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/users/"+owner.String()+"/links/"+platform+"/click", nil)
	if viewer != nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func clickCounted(t *testing.T, w *httptest.ResponseRecorder) bool {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s, want 200", w.Code, w.Body.String())
	}
	var env struct {
		Data LinkClickResult `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v (%s)", err, w.Body.String())
	}
	return env.Data.Counted
}

func TestLinkClickCountsOncePerViewerPerHour(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	r := linkClickRouter(s, d, &fakeBlocks{})
	viewer := pvStrangerID

	if !clickCounted(t, clickLink(r, pvOwnerID, "website", &viewer)) || s.count("website") != 1 {
		t.Fatalf("first click: count %d, want 1", s.count("website"))
	}
	d.advance(59 * time.Minute)
	if clickCounted(t, clickLink(r, pvOwnerID, "website", &viewer)) || s.count("website") != 1 {
		t.Fatalf("repeat within the hour counted: %d", s.count("website"))
	}
	other := uuid.New()
	if !clickCounted(t, clickLink(r, pvOwnerID, "website", &other)) || s.count("website") != 2 {
		t.Fatalf("second viewer not counted: %d", s.count("website"))
	}
	d.advance(2 * time.Minute)
	if !clickCounted(t, clickLink(r, pvOwnerID, "website", &viewer)) || s.count("website") != 3 {
		t.Fatalf("click after the hour not counted: %d", s.count("website"))
	}
	for _, ttl := range d.ttls {
		if ttl != time.Hour {
			t.Fatalf("dedupe window %v, want 1h", ttl)
		}
	}
}

func TestLinkClickOwnerDoesNotCount(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	r := linkClickRouter(s, d, &fakeBlocks{})
	owner := pvOwnerID
	if clickCounted(t, clickLink(r, pvOwnerID, "website", &owner)) || s.count("website") != 0 {
		t.Fatalf("owner's click counted: %d", s.count("website"))
	}
}

func TestLinkClickRedisErrorCountsNothing(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	d.err = errors.New("redis: connection refused")
	r := linkClickRouter(s, d, &fakeBlocks{})
	viewer := pvStrangerID
	for i := 0; i < 3; i++ {
		if clickCounted(t, clickLink(r, pvOwnerID, "website", &viewer)) {
			t.Fatal("counted with Redis down")
		}
	}
	if s.count("website") != 0 {
		t.Fatalf("count %d with Redis down, want 0", s.count("website"))
	}
	// No deduper at all counts nothing either.
	r = linkClickRouter(s, nil, &fakeBlocks{})
	if clickCounted(t, clickLink(r, pvOwnerID, "website", &viewer)) || s.count("website") != 0 {
		t.Fatal("counted without a deduper")
	}
}

func TestLinkClickUnauthenticatedRefused(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	r := linkClickRouter(s, d, &fakeBlocks{})
	w := clickLink(r, pvOwnerID, "website", nil)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", w.Code)
	}
	if s.count("website") != 0 || len(d.keys) != 0 {
		t.Fatal("signed-out click reached the counter")
	}
}

func TestLinkClickUnknownLinkOrOwnerIs404(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	r := linkClickRouter(s, d, &fakeBlocks{})
	viewer := pvStrangerID
	for name, w := range map[string]*httptest.ResponseRecorder{
		"unknown platform": clickLink(r, pvOwnerID, "myspace", &viewer),
		"missing owner":    clickLink(r, uuid.New(), "website", &viewer),
	} {
		if w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d body %s, want 404", name, w.Code, w.Body.String())
		}
	}
	if len(d.keys) != 0 {
		t.Fatal("a 404 burned a dedupe key")
	}
}

func TestLinkClickAcrossABlockIs404(t *testing.T) {
	for _, dir := range blockDirections {
		s, d := newFakeLinkClicks(), newFakeDedupe()
		r := linkClickRouter(s, d, &fakeBlocks{rows: map[pairKey]bool{{dir.blocker, dir.blkd}: true}})
		viewer := pvStrangerID
		if w := clickLink(r, pvOwnerID, "website", &viewer); w.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", dir.name, w.Code)
		}
		if s.count("website") != 0 {
			t.Fatalf("%s: counted across a block", dir.name)
		}
	}
}

// The response and the dedupe key name no account.
func TestLinkClickNeverExposesTheViewer(t *testing.T) {
	s, d := newFakeLinkClicks(), newFakeDedupe()
	r := linkClickRouter(s, d, &fakeBlocks{})
	viewer := pvStrangerID
	w := clickLink(r, pvOwnerID, "website", &viewer)
	if strings.Contains(w.Body.String(), viewer.String()) {
		t.Fatalf("response names the viewer: %s", w.Body.String())
	}
	for k := range d.keys {
		if strings.Contains(k, viewer.String()) || strings.Contains(k, pvOwnerID.String()) {
			t.Fatalf("dedupe key names an account: %s", k)
		}
	}
}

func TestOldAnonymousClickRouteStaysRetired(t *testing.T) {
	r := linkClickRouter(newFakeLinkClicks(), newFakeDedupe(), &fakeBlocks{})
	req := httptest.NewRequest(http.MethodPost, "/v1/users/links/website/click", nil)
	req.Header.Set("X-User-Id", pvStrangerID.String())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusGone || !strings.Contains(w.Body.String(), canonicalLinkClickRoute) {
		t.Fatalf("status %d body %s, want 410 naming %s", w.Code, w.Body.String(), canonicalLinkClickRoute)
	}
}
