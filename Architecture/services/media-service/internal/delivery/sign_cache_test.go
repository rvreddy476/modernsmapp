package delivery

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Reels audit 2026-10-30, findings 9 and 11.

// countingSigner counts signatures and embeds the expiry in the URL.
type countingSigner struct {
	mu    sync.Mutex
	signs int
	segs  int
}

func (c *countingSigner) PublicURL(key string) (string, error) { return "https://cdn/" + key, nil }

func (c *countingSigner) SignProtected(key string, ttl time.Duration, now time.Time) (string, error) {
	c.mu.Lock()
	c.signs++
	c.mu.Unlock()
	return "https://cdn/" + key + "?Expires=" + strconv.FormatInt(now.Add(ttl).Unix(), 10) + "&Signature=s", nil
}

func (c *countingSigner) SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	if err := checkSegmentTTL(key, ttl); err != nil {
		return "", err
	}
	c.mu.Lock()
	c.segs++
	c.mu.Unlock()
	return "https://cdn/" + key + "?Expires=" + strconv.FormatInt(now.Add(ttl).Unix(), 10) + "&Signature=seg", nil
}

func expiresOf(t *testing.T, raw string) time.Time {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	sec, err := strconv.ParseInt(u.Query().Get("Expires"), 10, 64)
	if err != nil {
		t.Fatalf("no Expires in %s", raw)
	}
	return time.Unix(sec, 0)
}

func TestRepeatedBatchReusesSignatures(t *testing.T) {
	s := &countingSigner{}
	g := NewGate(s, fakeAuthz{allow: true})
	assets := map[string]map[string]string{
		"m1": {"original": "user/u/m1/original", "720p": "user/u/m1/720p"},
		"m2": {"original": "user/u/m2/original", "720p": "user/u/m2/720p"},
	}
	for i := 0; i < 20; i++ {
		if _, err := g.URLsForAssets(context.Background(), "", assets); err != nil {
			t.Fatal(err)
		}
	}
	if s.signs != 4 {
		t.Fatalf("20 identical batches cost %d signatures, want 4", s.signs)
	}
}

// The cache never carries a decision: a viewer denied after an allowed
// request gets nothing, cached URL or not.
func TestCachedSignatureIsNeverServedWithoutAuthorization(t *testing.T) {
	s := &countingSigner{}
	allow := true
	g := NewGate(s, authzFunc(func() error {
		if allow {
			return nil
		}
		return ErrDeliveryDenied
	}))
	keys := map[string]string{"original": "user/u/m/original"}
	if _, err := g.URLsForAsset(context.Background(), "v", "m", keys); err != nil {
		t.Fatal(err)
	}
	allow = false
	if got, err := g.URLsForAsset(context.Background(), "v", "m", keys); !errors.Is(err, ErrDeliveryDenied) || len(got) != 0 {
		t.Fatalf("denied viewer got %v %v from the cache", got, err)
	}
}

func TestCacheReusesOnlyWhileEnoughLifeRemains(t *testing.T) {
	c := newSignCache()
	now := time.Now()
	c.put("k", MaxProtectedTTL, "u1", now.Add(MaxProtectedTTL), now)
	if u, ok := c.get("k", MaxProtectedTTL, now.Add(SignatureReuseWindow-time.Second)); !ok || u != "u1" {
		t.Fatal("not reused inside the window")
	}
	if _, ok := c.get("k", MaxProtectedTTL, now.Add(SignatureReuseWindow+time.Second)); ok {
		t.Fatal("reused with less than GuaranteedProtectedTTL left")
	}
	if GuaranteedProtectedTTL <= 0 || GuaranteedProtectedTTL != MaxProtectedTTL-SignatureReuseWindow {
		t.Fatalf("GuaranteedProtectedTTL = %s", GuaranteedProtectedTTL)
	}
	// Different ttl, different entry.
	c.put("k", MaxProtectedTTL, "u1", now.Add(MaxProtectedTTL), now)
	c.put("k", time.Hour, "u-hour", now.Add(time.Hour), now)
	if u, _ := c.get("k", MaxProtectedTTL, now); u != "u1" {
		t.Fatal("ttl is not part of the key")
	}
}

func TestCacheIsBounded(t *testing.T) {
	c := newSignCache()
	now := time.Now()
	for i := 0; i < signCacheMaxEntries+10; i++ {
		c.put(strconv.Itoa(i), MaxProtectedTTL, "u", now.Add(MaxProtectedTTL), now)
	}
	if c.len() > signCacheMaxEntries {
		t.Fatalf("cache holds %d entries, cap %d", c.len(), signCacheMaxEntries)
	}
}

func TestHLSSegmentTTL(t *testing.T) {
	cases := map[int]time.Duration{
		0:             hlsSegmentTTLUnknown,
		30_000:        time.Minute + hlsSegmentTTLMargin,
		600_000:       20*time.Minute + hlsSegmentTTLMargin,
		4 * 3_600_000: MaxHLSSegmentTTL,
	}
	for ms, want := range cases {
		if got := HLSSegmentTTL(ms); got != want {
			t.Fatalf("HLSSegmentTTL(%d) = %s, want %s", ms, got, want)
		}
	}
	// A 10-minute reel outlives a 10-minute session with a pause.
	if HLSSegmentTTL(600_000) < 15*time.Minute {
		t.Fatal("a 10-minute reel's segments expire mid-play")
	}
}

func TestOnlyHLSSegmentsAreSegmentKeys(t *testing.T) {
	yes := []string{"user/u/m/hls/720p_000.ts", "user/u/m/hls/360p_123.ts"}
	no := []string{"user/u/m/original", "user/u/m/hls/720p.m3u8", "user/u/m/hls/master.m3u8",
		"user/u/m/720p", "user/u/m/hls/", "user/u/m/hls/.ts", "user/u/m/hls/a/b.ts", "user/u/m/x.ts"}
	for _, k := range yes {
		if !IsHLSSegmentKey(k) {
			t.Fatalf("%s is a segment", k)
		}
	}
	for _, k := range no {
		if IsHLSSegmentKey(k) {
			t.Fatalf("%s is not a segment", k)
		}
	}
}

func TestLongSignatureIsRefusedForAnythingButASegment(t *testing.T) {
	s := gateSigner(t)
	now := time.Now()
	u, err := s.SignProtectedSegment("user/u/m/hls/720p_000.ts", time.Hour, now)
	if err != nil {
		t.Fatalf("segment refused: %v", err)
	}
	if !expiresOf(t, u).Equal(time.Unix(now.Add(time.Hour).Unix(), 0)) {
		t.Fatalf("segment expiry %s, want an hour", expiresOf(t, u))
	}
	for _, k := range []string{"user/u/m/original", "user/u/m/720p", "user/u/m/hls/720p.m3u8", PublicPrefix + "hls/a_000.ts"} {
		if _, err := s.SignProtectedSegment(k, time.Hour, now); err == nil {
			t.Fatalf("%s got a long signature", k)
		}
	}
	if _, err := s.SignProtectedSegment("user/u/m/hls/720p_000.ts", MaxHLSSegmentTTL+time.Second, now); err == nil {
		t.Fatal("segment ttl over the cap accepted")
	}
	if _, err := s.SignProtected("user/u/m/original", time.Hour, now); err == nil {
		t.Fatal("the ordinary signer accepted an hour")
	}
}

func TestPlaylistSignsSegmentsLongAndEverythingElseShort(t *testing.T) {
	s := &countingSigner{}
	g := NewGate(s, fakeAuthz{allow: true})
	keys := map[string]string{
		"playlist":    "user/u/m/hls/720p.m3u8",
		"720p_000.ts": "user/u/m/hls/720p_000.ts",
		"720p_001.ts": "user/u/m/hls/720p_001.ts",
	}
	before := time.Now()
	got, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, u := range got {
		exp := expiresOf(t, u)
		if strings.HasSuffix(name, ".ts") {
			if exp.Before(before.Add(time.Hour - time.Second)) {
				t.Fatalf("segment %s expires at %s, want an hour", name, exp)
			}
		} else if exp.After(before.Add(MaxProtectedTTL + time.Second)) {
			t.Fatalf("%s got a long signature (%s)", name, exp)
		}
	}
	if s.segs != 2 {
		t.Fatalf("segment signatures = %d, want 2", s.segs)
	}
	denied := NewGate(s, fakeAuthz{allow: false})
	if got, err := denied.HLSURLsForAsset(context.Background(), "v", "m", keys, time.Hour); !errors.Is(err, ErrDeliveryDenied) || len(got) != 0 {
		t.Fatalf("denied viewer got %v %v", got, err)
	}
}

// A signer without long segment support signs segments at the short TTL.
func TestSegmentsFallBackToShortTTLWithoutSegmentSigner(t *testing.T) {
	g := NewGate(shortOnly{&countingSigner{}}, fakeAuthz{allow: true})
	before := time.Now()
	got, err := g.HLSURLsForAsset(context.Background(), "v", "m",
		map[string]string{"a.ts": "user/u/m/hls/a_000.ts"}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if exp := expiresOf(t, got["a.ts"]); exp.After(before.Add(MaxProtectedTTL + time.Second)) {
		t.Fatalf("expiry %s past the short ttl", exp)
	}
}

type shortOnly struct{ s *countingSigner }

func (o shortOnly) PublicURL(k string) (string, error) { return o.s.PublicURL(k) }
func (o shortOnly) SignProtected(k string, ttl time.Duration, now time.Time) (string, error) {
	return o.s.SignProtected(k, ttl, now)
}

// signSegment itself never gives a playlist (or any non-segment key) the
// long lifetime, whoever calls it.
func TestSignSegmentKeepsNonSegmentsShort(t *testing.T) {
	g := NewGate(&countingSigner{}, fakeAuthz{allow: true})
	now := time.Now()
	u, err := g.signSegment("user/u/m/hls/720p.m3u8", time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if exp := expiresOf(t, u); exp.After(now.Add(MaxProtectedTTL + time.Second)) {
		t.Fatalf("a playlist got a long signature (%s)", exp)
	}
}

// The segment rule on its own (the development signer has no second
// ttl check behind it).
func TestSegmentRule(t *testing.T) {
	seg := "user/u/m/hls/720p_000.ts"
	if err := CheckSegmentTTL(seg, MaxHLSSegmentTTL); err != nil {
		t.Fatalf("the cap itself refused: %v", err)
	}
	for name, c := range map[string]struct {
		key string
		ttl time.Duration
	}{
		"over the cap": {seg, MaxHLSSegmentTTL + time.Second},
		"zero":         {seg, 0},
		"a playlist":   {"user/u/m/hls/720p.m3u8", time.Hour},
		"an original":  {"user/u/m/original", time.Hour},
		"public":       {PublicPrefix + "hls/a_000.ts", time.Hour},
	} {
		if err := CheckSegmentTTL(c.key, c.ttl); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
