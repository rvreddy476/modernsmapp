package delivery

import (
	"container/list"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

// Signed-URL cache and HLS segment lifetimes.
//
// CACHE (reels audit 2026-10-30, findings 9 and 11). POST /v1/media/batch
// needs no sign-in and signs every key of up to fifty assets with RSA-2048.
// A signature over (resource, expiry) is the same capability whoever asked
// for it, so the gate reuses one for most of its life: an entry is answered
// again while at least ttl-reuseWindow(ttl) of it remains. The
// authorization decision is NOT cached: every request is still authorized
// before a cached URL is handed out. The cache is a bounded LRU: when full,
// the least recently used entry goes, one at a time (lv-12; it used to be
// wiped whole).
//
// Because a reused URL has less than its full ttl left, every DTO that
// tells a client when to refresh uses the least any URL in it can have left
// (GuaranteedProtectedTTL, segmentGuarantee).
//
// SEGMENTS (long-video audit lv-2 and lv-12; reels audit, segment URLs that
// survive delete/private). A segment signature cannot be revoked, so its
// lifetime IS the window in which a video made private, deleted, taken
// down or behind a lapsed membership keeps playing for a viewer who already
// loaded the playlist. Segments were signed for twice the video's length,
// up to 12 h. They are now signed for one short window, the same for every
// video whatever its length: MEDIA_HLS_SEGMENT_TTL (default
// DefaultHLSSegmentTTL, never more than MaxHLSSegmentTTL).
//
// A VOD media playlist carries EXT-X-ENDLIST, so a player does not reload
// it on its own: a player still playing when the window closes re-fetches
// the child playlist (at the X-HLS-Expires-At it was served, or on a 403
// from a segment) and continues at its position. That re-fetch goes back
// through the access gate, so a revoked viewer is refused there.
//
// ONE SIGNATURE PER PLAYLIST. With the CloudFront signer the gate signs one
// custom-policy grant whose Resource is the asset's hls/ directory with a
// wildcard (<cdn>/<asset>/hls/*.ts) and appends that same query to every
// segment URL, instead of one canned-policy RSA signature per segment (2,400
// for a 4-hour rung, 5-6 s of CPU). The grant covers only the segments of
// the one asset the viewer was just authorized for, for the same window.
// The development MinIO signer has no wildcard form and keeps signing per
// segment (SegmentSigner).

const (
	// SignatureReuseWindow is how long one signature is handed out again.
	SignatureReuseWindow = 2 * time.Minute
	// GuaranteedProtectedTTL is the least lifetime a protected URL the gate
	// hands out has left: MaxProtectedTTL minus the reuse window. DTO
	// expires_at values use it.
	GuaranteedProtectedTTL = MaxProtectedTTL - SignatureReuseWindow

	// HLSSegmentTTLEnv names the environment variable that sets the
	// segment window (a Go duration, e.g. "10m").
	HLSSegmentTTLEnv = "MEDIA_HLS_SEGMENT_TTL"
	// DefaultHLSSegmentTTL is the segment window when HLSSegmentTTLEnv is
	// unset or invalid.
	DefaultHLSSegmentTTL = 10 * time.Minute
	// MinHLSSegmentTTL and MaxHLSSegmentTTL bound the configurable window.
	// MaxHLSSegmentTTL is also the ceiling every segment signer enforces:
	// no segment URL outlives a visibility change by more than this.
	MinHLSSegmentTTL = time.Minute
	MaxHLSSegmentTTL = 15 * time.Minute

	// signCacheMaxEntries bounds the cache's memory (a URL is ~0.5 KB).
	signCacheMaxEntries = 20000
)

// HLSSegmentTTL is the upper bound the playback service asks for: the
// longest window allowed. The gate clamps it to its configured window
// (HLSSegmentTTLFromEnv). durationMs no longer matters: a long video keeps
// playing through playlist re-fetches, not through a longer signature.
func HLSSegmentTTL(durationMs int) time.Duration {
	_ = durationMs
	return MaxHLSSegmentTTL
}

// HLSSegmentTTLFromEnv reads HLSSegmentTTLEnv. Unset is the default; a
// value that does not parse or falls outside [MinHLSSegmentTTL,
// MaxHLSSegmentTTL] is an error, returned with the default (which is
// inside the bound, never a longer window).
func HLSSegmentTTLFromEnv() (time.Duration, error) {
	raw := strings.TrimSpace(os.Getenv(HLSSegmentTTLEnv))
	if raw == "" {
		return DefaultHLSSegmentTTL, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return DefaultHLSSegmentTTL, fmt.Errorf("delivery: %s=%q: %w", HLSSegmentTTLEnv, raw, err)
	}
	if !validHLSSegmentTTL(d) {
		return DefaultHLSSegmentTTL, fmt.Errorf("delivery: %s=%s outside [%s, %s]", HLSSegmentTTLEnv, d, MinHLSSegmentTTL, MaxHLSSegmentTTL)
	}
	return d, nil
}

// validHLSSegmentTTL reports whether d may be the segment window.
func validHLSSegmentTTL(d time.Duration) bool {
	return d >= MinHLSSegmentTTL && d <= MaxHLSSegmentTTL
}

func hlsSegmentTTLOrDefault() time.Duration {
	d, err := HLSSegmentTTLFromEnv()
	if err != nil {
		slog.Warn("delivery: invalid HLS segment window, using the default", "error", err, "default", DefaultHLSSegmentTTL.String())
	}
	return d
}

// SetHLSSegmentTTL overrides the window read from the environment. A value
// outside [MinHLSSegmentTTL, MaxHLSSegmentTTL] is refused.
func (g *Gate) SetHLSSegmentTTL(d time.Duration) error {
	if !validHLSSegmentTTL(d) {
		return fmt.Errorf("delivery: segment window %s outside [%s, %s]", d, MinHLSSegmentTTL, MaxHLSSegmentTTL)
	}
	g.hlsSegmentTTL = d
	return nil
}

// segmentWindow is the lifetime a segment is signed for: the caller's ttl,
// never more than the configured window.
func (g *Gate) segmentWindow(ttl time.Duration) time.Duration {
	w := g.hlsSegmentTTL
	if w <= 0 || w > MaxHLSSegmentTTL {
		w = DefaultHLSSegmentTTL
	}
	if ttl > 0 && ttl < w {
		return ttl
	}
	return w
}

// IsHLSSegmentKey reports whether key is an HLS media segment the worker
// wrote: a ".ts" object directly under an "/hls/" directory.
func IsHLSSegmentKey(key string) bool {
	i := strings.LastIndex(key, "/hls/")
	if i < 0 {
		return false
	}
	name := key[i+len("/hls/"):]
	return name != "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".ts") && name != ".ts"
}

// hlsSegmentDir splits a segment key into its hls/ directory (ending in
// "/hls/") and the segment name.
func hlsSegmentDir(key string) (dir, name string) {
	i := strings.LastIndex(key, "/hls/") + len("/hls/")
	return key[:i], key[i:]
}

// SegmentSigner is implemented by signers that sign one HLS segment for
// longer than MaxProtectedTTL (the development MinIO signer). A signer that
// implements neither this nor SegmentDirSigner signs segments for at most
// MaxProtectedTTL.
type SegmentSigner interface {
	SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error)
}

// SegmentGrant authorizes every segment of one hls/ directory: the URL of
// segment name is BaseURL + name + "?" + Query.
type SegmentGrant struct {
	BaseURL string
	Query   string
}

// URL is the delivery URL of one segment under the grant.
func (g SegmentGrant) URL(name string) string { return g.BaseURL + name + "?" + g.Query }

// SegmentDirSigner is implemented by signers that sign one wildcard grant
// for all the segments of an hls/ directory (CloudFront custom policy).
type SegmentDirSigner interface {
	SignProtectedSegmentDir(dir string, ttl time.Duration, now time.Time) (SegmentGrant, error)
}

// SignProtectedSegment signs one HLS segment for ttl, up to
// MaxHLSSegmentTTL, with a canned policy. Anything that is not a protected
// HLS segment key is refused.
func (s *Signer) SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	if err := checkSegmentTTL(key, ttl); err != nil {
		return "", err
	}
	return s.signResourceUpTo(s.cdnBaseURL+"/"+key, ttl, MaxHLSSegmentTTL, now)
}

// SignProtectedSegmentDir signs ONE custom-policy grant for every ".ts"
// object under dir (a protected ".../hls/" directory) for ttl, up to
// MaxHLSSegmentTTL.
func (s *Signer) SignProtectedSegmentDir(dir string, ttl time.Duration, now time.Time) (SegmentGrant, error) {
	if err := checkSegmentDir(dir, ttl); err != nil {
		return SegmentGrant{}, err
	}
	base := s.cdnBaseURL + "/" + dir
	if strings.ContainsAny(s.cdnBaseURL, "*?") {
		return SegmentGrant{}, fmt.Errorf("delivery: wildcard character in CDN base URL")
	}
	query, err := s.signCustomPolicy(base+"*.ts", ttl, MaxHLSSegmentTTL, now)
	if err != nil {
		return SegmentGrant{}, err
	}
	return SegmentGrant{BaseURL: base, Query: query}, nil
}

// checkSegmentDir is the rule SignProtectedSegmentDir enforces: a protected
// hls/ directory with no wildcard characters of its own (the policy's only
// wildcard is the one the signer adds), for (0, MaxHLSSegmentTTL].
func checkSegmentDir(dir string, ttl time.Duration) error {
	if ClassForKey(dir) != ClassProtected {
		return fmt.Errorf("delivery: %q is not a protected key", dir)
	}
	if !strings.HasSuffix(dir, "/hls/") || strings.ContainsAny(dir, "*?") {
		return fmt.Errorf("delivery: %q is not an HLS segment directory", dir)
	}
	return checkSegmentLifetime(ttl)
}

// checkSegmentLifetime is the ceiling on every segment signature, per
// segment or per directory.
func checkSegmentLifetime(ttl time.Duration) error {
	if ttl <= 0 || ttl > MaxHLSSegmentTTL {
		return fmt.Errorf("delivery: segment ttl %s outside (0, %s]", ttl, MaxHLSSegmentTTL)
	}
	return nil
}

// checkSegmentTTL is the rule SignProtectedSegment enforces, shared with
// the development signer.
func checkSegmentTTL(key string, ttl time.Duration) error {
	if ClassForKey(key) != ClassProtected {
		return fmt.Errorf("delivery: %q is not a protected key", key)
	}
	if !IsHLSSegmentKey(key) {
		return fmt.Errorf("delivery: %q is not an HLS segment; only segments get a segment signature", key)
	}
	return checkSegmentLifetime(ttl)
}

// CheckSegmentTTL exposes checkSegmentTTL to signers outside this package
// (the development MinIO signer in cmd/server).
func CheckSegmentTTL(key string, ttl time.Duration) error { return checkSegmentTTL(key, ttl) }

type signCacheKey struct {
	key string
	ttl time.Duration
}

type signCacheEntry struct {
	url     string
	grant   SegmentGrant
	expires time.Time
}

type signCacheItem struct {
	k signCacheKey
	e signCacheEntry
}

// signCache holds signed URLs and segment grants by (resource, ttl); when
// full, the least recently used entry is evicted, one per insert.
type signCache struct {
	mu      sync.Mutex
	max     int
	order   *list.List // front: most recently used; values are *signCacheItem
	entries map[signCacheKey]*list.Element
}

func newSignCache() *signCache { return newSignCacheCap(signCacheMaxEntries) }

func newSignCacheCap(max int) *signCache {
	if max < 1 {
		max = 1
	}
	return &signCache{max: max, order: list.New(), entries: make(map[signCacheKey]*list.Element)}
}

// reuseWindow is how long a signature of ttl is handed out again: the
// SignatureReuseWindow, but never more than half of a short ttl.
func reuseWindow(ttl time.Duration) time.Duration {
	if w := ttl / 2; w < SignatureReuseWindow {
		return w
	}
	return SignatureReuseWindow
}

// lookup returns the entry for k if it has at least
// k.ttl-reuseWindow(k.ttl) left at now, and marks it recently used.
func (c *signCache) lookup(k signCacheKey, now time.Time) (signCacheEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[k]
	if !ok {
		return signCacheEntry{}, false
	}
	it := el.Value.(*signCacheItem)
	if it.e.expires.Sub(now) < k.ttl-reuseWindow(k.ttl) {
		c.order.Remove(el)
		delete(c.entries, k)
		return signCacheEntry{}, false
	}
	c.order.MoveToFront(el)
	return it.e, true
}

func (c *signCache) store(k signCacheKey, e signCacheEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.entries[k]; ok {
		el.Value.(*signCacheItem).e = e
		c.order.MoveToFront(el)
		return
	}
	c.entries[k] = c.order.PushFront(&signCacheItem{k: k, e: e})
	for c.order.Len() > c.max {
		last := c.order.Back()
		c.order.Remove(last)
		delete(c.entries, last.Value.(*signCacheItem).k)
	}
}

// get returns a cached URL for (key, ttl) that has at least
// ttl-reuseWindow(ttl) left at now.
func (c *signCache) get(key string, ttl time.Duration, now time.Time) (string, bool) {
	e, ok := c.lookup(signCacheKey{key, ttl}, now)
	return e.url, ok
}

func (c *signCache) put(key string, ttl time.Duration, url string, expires time.Time) {
	c.store(signCacheKey{key, ttl}, signCacheEntry{url: url, expires: expires})
}

func (c *signCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// signProtected is every protected signature the gate issues for a
// delivery read, through the cache.
func (g *Gate) signProtected(key string, ttl time.Duration, now time.Time) (string, error) {
	if g.cache == nil {
		return g.signer.SignProtected(key, ttl, now)
	}
	if u, ok := g.cache.get(key, ttl, now); ok {
		return u, nil
	}
	u, err := g.signer.SignProtected(key, ttl, now)
	if err != nil {
		return "", err
	}
	g.cache.put(key, ttl, u, now.Add(ttl))
	return u, nil
}

// segmentSignTTL is the lifetime signSegment signs a segment of window ttl
// for: ttl itself with a segment-capable signer, at most MaxProtectedTTL
// otherwise.
func (g *Gate) segmentSignTTL(ttl time.Duration) time.Duration {
	switch g.signer.(type) {
	case SegmentDirSigner, SegmentSigner:
		return ttl
	}
	if ttl > MaxProtectedTTL {
		return MaxProtectedTTL
	}
	return ttl
}

// segmentGuarantee is the least lifetime a segment URL signed for window
// ttl has left when it is handed out, cache reuse included.
func (g *Gate) segmentGuarantee(ttl time.Duration) time.Duration {
	s := g.segmentSignTTL(ttl)
	return s - reuseWindow(s)
}

// signSegment signs one HLS segment for ttl (already clamped to the
// window): one wildcard grant per hls/ directory with a SegmentDirSigner,
// one signature per segment with a SegmentSigner, and at most
// MaxProtectedTTL otherwise. A key that is not a segment gets the ordinary
// MaxProtectedTTL signature.
func (g *Gate) signSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	if !IsHLSSegmentKey(key) {
		return g.signProtected(key, MaxProtectedTTL, now)
	}
	if ds, ok := g.signer.(SegmentDirSigner); ok {
		dir, name := hlsSegmentDir(key)
		ck := signCacheKey{dir + "*.ts", ttl}
		if g.cache != nil {
			if e, ok := g.cache.lookup(ck, now); ok {
				return e.grant.URL(name), nil
			}
		}
		grant, err := ds.SignProtectedSegmentDir(dir, ttl, now)
		if err != nil {
			return "", err
		}
		if g.cache != nil {
			g.cache.store(ck, signCacheEntry{grant: grant, expires: now.Add(ttl)})
		}
		return grant.URL(name), nil
	}
	ss, ok := g.signer.(SegmentSigner)
	if !ok {
		return g.signProtected(key, g.segmentSignTTL(ttl), now)
	}
	if g.cache != nil {
		if u, ok := g.cache.get(key, ttl, now); ok {
			return u, nil
		}
	}
	u, err := ss.SignProtectedSegment(key, ttl, now)
	if err != nil {
		return "", err
	}
	if g.cache != nil {
		g.cache.put(key, ttl, u, now.Add(ttl))
	}
	return u, nil
}
