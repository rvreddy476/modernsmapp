package delivery

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
)

// Reels audit 2026-10-30, findings 9 and 11; long-video audit lv-2, lv-12.

// countingSigner counts signatures and embeds the expiry in the URL. It
// signs segments one by one (SegmentSigner), like the development signer.
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

// countingCloudFront is the real CloudFront signer with every RSA signature
// counted.
type countingCloudFront struct {
	*Signer
	mu    sync.Mutex
	signs int
	dirs  int
	segs  int
}

func (c *countingCloudFront) SignProtected(key string, ttl time.Duration, now time.Time) (string, error) {
	c.mu.Lock()
	c.signs++
	c.mu.Unlock()
	return c.Signer.SignProtected(key, ttl, now)
}

func (c *countingCloudFront) SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	c.mu.Lock()
	c.segs++
	c.mu.Unlock()
	return c.Signer.SignProtectedSegment(key, ttl, now)
}

func (c *countingCloudFront) SignProtectedSegmentDir(dir string, ttl time.Duration, now time.Time) (SegmentGrant, error) {
	c.mu.Lock()
	c.dirs++
	c.mu.Unlock()
	return c.Signer.SignProtectedSegmentDir(dir, ttl, now)
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

// customPolicyOf checks a custom-policy URL's signature against key and
// that its wildcard Resource matches the URL itself, and returns the
// policy's resource and expiry.
func customPolicyOf(t *testing.T, raw string, key *rsa.PrivateKey) (string, time.Time) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("Expires") != "" || q.Get("Policy") == "" || q.Get("Key-Pair-Id") == "" {
		t.Fatalf("not a custom-policy URL: %s", raw)
	}
	policy := decodeCloudFrontBase64(t, q.Get("Policy"))
	sum := sha1.Sum(policy)
	if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA1, sum[:], decodeCloudFrontBase64(t, q.Get("Signature"))); err != nil {
		t.Fatalf("custom policy signature does not verify: %v", err)
	}
	var p cannedPolicy
	if err := json.Unmarshal(policy, &p); err != nil || len(p.Statement) != 1 {
		t.Fatalf("policy %s: %v", policy, err)
	}
	res := p.Statement[0].Resource
	prefix, ok := strings.CutSuffix(res, "*.ts")
	if !ok || strings.ContainsAny(prefix, "*?") {
		t.Fatalf("resource %q is not <dir>*.ts", res)
	}
	plain := u.Scheme + "://" + u.Host + u.Path
	if !strings.HasPrefix(plain, prefix) || !strings.HasSuffix(plain, ".ts") {
		t.Fatalf("resource %q does not cover %s", res, plain)
	}
	return res, time.Unix(p.Statement[0].Condition.DateLessThan.EpochTime, 0)
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
	c.put("k", MaxProtectedTTL, "u1", now.Add(MaxProtectedTTL))
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
	c.put("k", MaxProtectedTTL, "u1", now.Add(MaxProtectedTTL))
	c.put("k", time.Hour, "u-hour", now.Add(time.Hour))
	if u, _ := c.get("k", MaxProtectedTTL, now); u != "u1" {
		t.Fatal("ttl is not part of the key")
	}
}

// Full, the cache evicts the least recently used entry, one per insert;
// it is never wiped whole (lv-12).
func TestCacheEvictsIncrementallyLeastRecentlyUsedFirst(t *testing.T) {
	c := newSignCacheCap(3)
	now := time.Now()
	for _, k := range []string{"a", "b", "c"} {
		c.put(k, MaxProtectedTTL, "u-"+k, now.Add(MaxProtectedTTL))
	}
	if _, ok := c.get("a", MaxProtectedTTL, now); !ok { // a is now the most recent
		t.Fatal("a missing")
	}
	c.put("d", MaxProtectedTTL, "u-d", now.Add(MaxProtectedTTL))
	if c.len() != 3 {
		t.Fatalf("len %d after one insert past the cap, want 3 (one eviction)", c.len())
	}
	if _, ok := c.get("b", MaxProtectedTTL, now); ok {
		t.Fatal("b, the least recently used, survived")
	}
	for _, k := range []string{"a", "c", "d"} {
		if u, ok := c.get(k, MaxProtectedTTL, now); !ok || u != "u-"+k {
			t.Fatalf("%s evicted (%q, %v); only the least recently used may go", k, u, ok)
		}
	}
	// Re-putting an existing key does not grow or evict.
	c.put("c", MaxProtectedTTL, "u-c2", now.Add(MaxProtectedTTL))
	if c.len() != 3 {
		t.Fatalf("len %d after an update", c.len())
	}
}

func TestCacheIsBounded(t *testing.T) {
	c := newSignCache()
	now := time.Now()
	for i := 0; i < signCacheMaxEntries+10; i++ {
		c.put(strconv.Itoa(i), MaxProtectedTTL, "u", now.Add(MaxProtectedTTL))
	}
	if c.len() != signCacheMaxEntries {
		t.Fatalf("cache holds %d entries, want exactly the cap %d", c.len(), signCacheMaxEntries)
	}
	if _, ok := c.get("9", MaxProtectedTTL, now); ok {
		t.Fatal("the oldest entries were not the ones evicted")
	}
	if _, ok := c.get("10", MaxProtectedTTL, now); !ok {
		t.Fatal("more than the overflow was evicted")
	}
}

// The segment window is short and the same for every video, whatever its
// length (lv-2: it was twice the duration, up to 12 h).
func TestHLSSegmentWindowIsShortAndLengthIndependent(t *testing.T) {
	if DefaultHLSSegmentTTL > 15*time.Minute || MaxHLSSegmentTTL > 15*time.Minute {
		t.Fatalf("default %s / cap %s: the window must be at most 15 minutes", DefaultHLSSegmentTTL, MaxHLSSegmentTTL)
	}
	if DefaultHLSSegmentTTL < MinHLSSegmentTTL || DefaultHLSSegmentTTL > MaxHLSSegmentTTL {
		t.Fatalf("default %s outside its own bound", DefaultHLSSegmentTTL)
	}
	longest := processing.DefaultMaxVideoDurationSeconds * 1000
	g := NewGate(&countingSigner{}, fakeAuthz{allow: true})
	for _, ms := range []int{-1, 0, 30_000, 600_000, 2 * 3_600_000, longest, 48 * 3_600_000} {
		if got := g.segmentWindow(HLSSegmentTTL(ms)); got != DefaultHLSSegmentTTL {
			t.Fatalf("segment window for %d ms = %s, want %s", ms, got, DefaultHLSSegmentTTL)
		}
	}
	// A caller asking for less gets less; asking for more gets the window.
	if got := g.segmentWindow(2 * time.Minute); got != 2*time.Minute {
		t.Fatalf("segmentWindow(2m) = %s", got)
	}
	if got := g.segmentWindow(12 * time.Hour); got != DefaultHLSSegmentTTL {
		t.Fatalf("segmentWindow(12h) = %s", got)
	}
}

func TestHLSSegmentWindowFromEnv(t *testing.T) {
	cases := map[string]struct {
		want time.Duration
		ok   bool
	}{
		"":        {DefaultHLSSegmentTTL, true},
		"5m":      {5 * time.Minute, true},
		"15m":     {15 * time.Minute, true},
		"1m":      {time.Minute, true},
		"16m":     {DefaultHLSSegmentTTL, false},
		"12h":     {DefaultHLSSegmentTTL, false},
		"30s":     {DefaultHLSSegmentTTL, false},
		"-5m":     {DefaultHLSSegmentTTL, false},
		"forever": {DefaultHLSSegmentTTL, false},
	}
	for raw, c := range cases {
		t.Setenv(HLSSegmentTTLEnv, raw)
		got, err := HLSSegmentTTLFromEnv()
		if got != c.want || (err == nil) != c.ok {
			t.Fatalf("%s=%q: %s, %v; want %s ok=%v", HLSSegmentTTLEnv, raw, got, err, c.want, c.ok)
		}
		if g := NewGate(&countingSigner{}, fakeAuthz{allow: true}); g.segmentWindow(HLSSegmentTTL(0)) != c.want {
			t.Fatalf("%s=%q: gate window %s, want %s", HLSSegmentTTLEnv, raw, g.segmentWindow(HLSSegmentTTL(0)), c.want)
		}
	}
	g := NewGate(&countingSigner{}, fakeAuthz{allow: true})
	if err := g.SetHLSSegmentTTL(MaxHLSSegmentTTL + time.Second); err == nil {
		t.Fatal("SetHLSSegmentTTL accepted a window over the cap")
	}
	if err := g.SetHLSSegmentTTL(3 * time.Minute); err != nil || g.segmentWindow(time.Hour) != 3*time.Minute {
		t.Fatalf("SetHLSSegmentTTL(3m): %v, window %s", err, g.segmentWindow(time.Hour))
	}
}

// lv-2 acceptance: a 4-hour asset's child playlist. Every segment URL dies
// within the window of issue, and the whole playlist costs O(1) signatures
// (lv-12: it was one per segment, 2,400 for this rung).
func TestFourHourPlaylistIsShortLivedAndSignedOnce(t *testing.T) {
	s, key := testSigner(t)
	cf := &countingCloudFront{Signer: s}
	g := NewGate(cf, fakeAuthz{allow: true})
	const segments = 2400 // 4 h of 6 s segments
	keys := map[string]string{"playlist": "user/u/m/hls/720p.m3u8"}
	for i := 0; i < segments; i++ {
		name := fmt.Sprintf("720p_%05d.ts", i)
		keys[name] = "user/u/m/hls/" + name
	}
	ttl := HLSSegmentTTL(processing.DefaultMaxVideoDurationSeconds * 1000)
	before := time.Now()
	urls, exp, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, ttl)
	if err != nil {
		t.Fatal(err)
	}
	limit := before.Add(DefaultHLSSegmentTTL + time.Second)
	if cf.dirs != 1 || cf.segs != 0 || cf.signs != 1 {
		t.Fatalf("signatures for %d segments: dir=%d per-segment=%d other=%d; want 1, 0, 1", segments, cf.dirs, cf.segs, cf.signs)
	}
	for name, u := range urls {
		if name == "playlist" {
			if e := expiresOf(t, u); e.After(before.Add(MaxProtectedTTL + time.Second)) {
				t.Fatalf("playlist key signed until %s", e)
			}
			continue
		}
		res, until := customPolicyOf(t, u, key)
		if res != "https://d111.cloudfront.net/user/u/m/hls/*.ts" {
			t.Fatalf("grant resource %q reaches past the asset's hls/ directory", res)
		}
		if until.After(limit) {
			t.Fatalf("%s valid until %s, past the %s window", name, until, DefaultHLSSegmentTTL)
		}
	}
	if exp.IsZero() || exp.After(limit) || exp.Before(before.Add(DefaultHLSSegmentTTL-SignatureReuseWindow-time.Second)) {
		t.Fatalf("X-HLS-Expires-At %s, want about now + %s", exp, DefaultHLSSegmentTTL-SignatureReuseWindow)
	}
	// A second fetch (another viewer, a quality switch) reuses the grant.
	if _, _, err := g.HLSURLsForAsset(context.Background(), "v2", "m", keys, ttl); err != nil {
		t.Fatal(err)
	}
	if cf.dirs != 1 {
		t.Fatalf("second fetch re-signed the grant (%d)", cf.dirs)
	}
}

// After access is revoked, the next playlist fetch is refused, and every
// URL handed out before the revocation dies within the window.
func TestPlaylistRefetchAfterRevocationIsRefused(t *testing.T) {
	s, key := testSigner(t)
	allow := true
	g := NewGate(s, authzFunc(func() error {
		if allow {
			return nil
		}
		return ErrDeliveryDenied
	}))
	keys := map[string]string{
		"playlist":    "user/u/m/hls/720p.m3u8",
		"720p_000.ts": "user/u/m/hls/720p_000.ts",
	}
	urls, _, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, HLSSegmentTTL(4*3_600_000))
	if err != nil {
		t.Fatal(err)
	}
	allow = false // made private, deleted, taken down, membership lapsed
	revoked := time.Now()
	if got, exp, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, HLSSegmentTTL(4*3_600_000)); !errors.Is(err, ErrDeliveryDenied) || len(got) != 0 || !exp.IsZero() {
		t.Fatalf("revoked viewer re-fetched the playlist: %v %s %v", got, exp, err)
	}
	if _, until := customPolicyOf(t, urls["720p_000.ts"], key); until.After(revoked.Add(DefaultHLSSegmentTTL + time.Second)) {
		t.Fatalf("a segment issued before the revocation works until %s, past the window", until)
	}
}

// The wildcard grant is only ever signed for a protected hls/ directory.
func TestSegmentDirGrantRule(t *testing.T) {
	s, _ := testSigner(t)
	now := time.Now()
	if _, err := s.SignProtectedSegmentDir("user/u/m/hls/", MaxHLSSegmentTTL, now); err != nil {
		t.Fatalf("the cap itself refused: %v", err)
	}
	for name, c := range map[string]struct {
		dir string
		ttl time.Duration
	}{
		"over the cap":        {"user/u/m/hls/", MaxHLSSegmentTTL + time.Second},
		"zero":                {"user/u/m/hls/", 0},
		"public":              {PublicPrefix + "u/m/hls/", time.Minute},
		"not hls":             {"user/u/m/", time.Minute},
		"a whole user":        {"user/u/", time.Minute},
		"wildcard in the dir": {"user/*/hls/", time.Minute},
		"single-char wild":    {"user/u/m?/hls/", time.Minute},
		"no trailing slash":   {"user/u/m/hls", time.Minute},
	} {
		if _, err := s.SignProtectedSegmentDir(c.dir, c.ttl, now); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// The playlist names the instant its signed segments stop working, cache
// reuse included, so a client can re-fetch it before then.
func TestPlaylistExpiryIsTheLeastLifeAnySegmentHasLeft(t *testing.T) {
	s := &countingSigner{}
	g := NewGate(s, fakeAuthz{allow: true})
	keys := map[string]string{
		"playlist":    "user/u/m/hls/720p.m3u8",
		"720p_000.ts": "user/u/m/hls/720p_000.ts",
	}
	ttl := HLSSegmentTTL(8 * 3_600_000)
	w := DefaultHLSSegmentTTL
	before := time.Now()
	urls, exp, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, ttl)
	if err != nil {
		t.Fatal(err)
	}
	after := time.Now()
	if exp.Before(before.Add(w-SignatureReuseWindow)) || exp.After(after.Add(w-SignatureReuseWindow)) {
		t.Fatalf("expires_at %s, want now + %s", exp, w-SignatureReuseWindow)
	}
	if !exp.Before(expiresOf(t, urls["720p_000.ts"]).Add(time.Second)) {
		t.Fatalf("expires_at %s is after the segment's own expiry %s", exp, expiresOf(t, urls["720p_000.ts"]))
	}
	// A reused signature (the cache) still has at least that much left.
	_, exp2, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, ttl)
	if err != nil {
		t.Fatal(err)
	}
	if s.segs != 1 {
		t.Fatalf("segment signatures = %d, want 1 (reused)", s.segs)
	}
	if exp2.Before(exp) {
		t.Fatalf("second expires_at %s before the first %s", exp2, exp)
	}

	// A signer without segment support: the short guarantee.
	short := NewGate(shortOnly{&countingSigner{}}, fakeAuthz{allow: true})
	before = time.Now()
	_, exp, err = short.HLSURLsForAsset(context.Background(), "v", "m", keys, ttl)
	if err != nil {
		t.Fatal(err)
	}
	if exp.Before(before.Add(GuaranteedProtectedTTL)) || exp.After(time.Now().Add(GuaranteedProtectedTTL)) {
		t.Fatalf("short-signer expires_at %s, want now + %s", exp, GuaranteedProtectedTTL)
	}

	// No protected segment (a master playlist): nothing expires mid-play.
	_, exp, err = g.HLSURLsForAsset(context.Background(), "v", "m",
		map[string]string{"playlist": "user/u/m/hls/master.m3u8"}, ttl)
	if err != nil || !exp.IsZero() {
		t.Fatalf("master playlist expires_at = %s, %v; want zero", exp, err)
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

func TestSegmentSignatureIsRefusedForAnythingButASegment(t *testing.T) {
	s := gateSigner(t)
	now := time.Now()
	u, err := s.SignProtectedSegment("user/u/m/hls/720p_000.ts", 10*time.Minute, now)
	if err != nil {
		t.Fatalf("segment refused: %v", err)
	}
	if !expiresOf(t, u).Equal(time.Unix(now.Add(10*time.Minute).Unix(), 0)) {
		t.Fatalf("segment expiry %s, want ten minutes", expiresOf(t, u))
	}
	for _, k := range []string{"user/u/m/original", "user/u/m/720p", "user/u/m/hls/720p.m3u8", PublicPrefix + "hls/a_000.ts"} {
		if _, err := s.SignProtectedSegment(k, 10*time.Minute, now); err == nil {
			t.Fatalf("%s got a segment signature", k)
		}
	}
	if _, err := s.SignProtectedSegment("user/u/m/hls/720p_000.ts", MaxHLSSegmentTTL+time.Second, now); err == nil {
		t.Fatal("segment ttl over the cap accepted")
	}
	if _, err := s.SignProtected("user/u/m/original", 10*time.Minute, now); err == nil {
		t.Fatal("the ordinary signer accepted ten minutes")
	}
}

func TestPlaylistSignsSegmentsForTheWindowAndEverythingElseShort(t *testing.T) {
	s := &countingSigner{}
	g := NewGate(s, fakeAuthz{allow: true})
	keys := map[string]string{
		"playlist":    "user/u/m/hls/720p.m3u8",
		"720p_000.ts": "user/u/m/hls/720p_000.ts",
		"720p_001.ts": "user/u/m/hls/720p_001.ts",
	}
	before := time.Now()
	got, _, err := g.HLSURLsForAsset(context.Background(), "v", "m", keys, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for name, u := range got {
		exp := expiresOf(t, u)
		if strings.HasSuffix(name, ".ts") {
			if exp.Before(before.Add(DefaultHLSSegmentTTL-time.Second)) || exp.After(time.Now().Add(DefaultHLSSegmentTTL+time.Second)) {
				t.Fatalf("segment %s expires at %s, want the %s window", name, exp, DefaultHLSSegmentTTL)
			}
		} else if exp.After(before.Add(MaxProtectedTTL + time.Second)) {
			t.Fatalf("%s got a segment-length signature (%s)", name, exp)
		}
	}
	if s.segs != 2 {
		t.Fatalf("segment signatures = %d, want 2", s.segs)
	}
	denied := NewGate(s, fakeAuthz{allow: false})
	if got, _, err := denied.HLSURLsForAsset(context.Background(), "v", "m", keys, time.Hour); !errors.Is(err, ErrDeliveryDenied) || len(got) != 0 {
		t.Fatalf("denied viewer got %v %v", got, err)
	}
}

// A signer without segment support signs segments at the short TTL.
func TestSegmentsFallBackToShortTTLWithoutSegmentSigner(t *testing.T) {
	g := NewGate(shortOnly{&countingSigner{}}, fakeAuthz{allow: true})
	before := time.Now()
	got, _, err := g.HLSURLsForAsset(context.Background(), "v", "m",
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
// segment lifetime or the wildcard grant, whoever calls it.
func TestSignSegmentKeepsNonSegmentsShort(t *testing.T) {
	for name, signer := range map[string]URLSigner{"per-segment": &countingSigner{}, "cloudfront": gateSigner(t)} {
		g := NewGate(signer, fakeAuthz{allow: true})
		now := time.Now()
		u, err := g.signSegment("user/u/m/hls/720p.m3u8", 10*time.Minute, now)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(u, "Policy=") {
			t.Fatalf("%s: a playlist got the wildcard grant", name)
		}
		if exp := expiresOf(t, u); exp.After(now.Add(MaxProtectedTTL + time.Second)) {
			t.Fatalf("%s: a playlist got a segment-length signature (%s)", name, exp)
		}
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
		"12 hours":     {seg, 12 * time.Hour},
		"zero":         {seg, 0},
		"a playlist":   {"user/u/m/hls/720p.m3u8", time.Minute},
		"an original":  {"user/u/m/original", time.Minute},
		"public":       {PublicPrefix + "hls/a_000.ts", time.Minute},
	} {
		if err := CheckSegmentTTL(c.key, c.ttl); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}
