package delivery

import (
	"fmt"
	"strings"
	"sync"
	"time"
)

// Signed-URL cache and HLS segment lifetimes (reels audit 2026-10-30,
// findings 9 and 11).
//
// CACHE. POST /v1/media/batch needs no sign-in and signs every key of up
// to fifty assets with RSA-2048: three to four hundred signatures for one
// anonymous request, and the child playlist signs every segment of a video
// on every fetch. A signature over (key, expiry) is the same capability
// whoever asked for it, so the gate reuses one for most of its life: an
// entry is answered again while at least ttl-SignatureReuseWindow of it
// remains, which makes a loop over the same reels cost one signature per
// object per SignatureReuseWindow instead of one per request. The
// authorization decision is NOT cached: every request is still authorized
// before a cached URL is handed out.
//
// Because a reused URL has less than its full ttl left, every DTO that
// tells a client when to refresh uses GuaranteedProtectedTTL, the least any
// URL in it can have left.
//
// SEGMENTS. A VOD media playlist carries EXT-X-ENDLIST, so a player does not
// reload it, and its segment URLs were signed for MaxProtectedTTL (5 min).
// A 6-10 minute reel, or any reel paused for five minutes, then hit 403 on
// its later segments. Segment URLs are now signed for HLSSegmentTTL, the
// video's duration twice over plus a margin, capped at MaxHLSSegmentTTL.
//
// THE TRADE-OFF. A segment signature cannot be revoked, so after a block or
// a takedown a viewer who already loaded the playlist can keep fetching its
// segments for up to that long (at most MaxHLSSegmentTTL) instead of five
// minutes. It is bounded to .ts segments under an asset's hls/ prefix: the
// playlists themselves are re-authorized on every fetch and served
// no-store, and the original, the renditions, thumbnails and every other
// key keep MaxProtectedTTL. Making the playlist reloadable instead would
// have needed every client player to re-request a VOD playlist, which HLS
// players do not do.

const (
	// SignatureReuseWindow is how long one signature is handed out again.
	SignatureReuseWindow = 2 * time.Minute
	// GuaranteedProtectedTTL is the least lifetime a protected URL the gate
	// hands out has left: MaxProtectedTTL minus the reuse window. DTO
	// expires_at values use it.
	GuaranteedProtectedTTL = MaxProtectedTTL - SignatureReuseWindow

	// MaxHLSSegmentTTL caps an HLS segment signature.
	MaxHLSSegmentTTL = 2 * time.Hour
	// hlsSegmentTTLMargin is added to twice the duration: start-up, a
	// pause, a re-buffer.
	hlsSegmentTTLMargin = 15 * time.Minute
	// hlsSegmentTTLUnknown is used when the duration is not known.
	hlsSegmentTTLUnknown = 30 * time.Minute

	// signCacheMaxEntries bounds the cache's memory (a URL is ~0.5 KB).
	signCacheMaxEntries = 20000
)

// HLSSegmentTTL is the signature lifetime for the segments of a video of
// durationMs: 2x the duration plus hlsSegmentTTLMargin, never less than
// MaxProtectedTTL and never more than MaxHLSSegmentTTL.
func HLSSegmentTTL(durationMs int) time.Duration {
	if durationMs <= 0 {
		return hlsSegmentTTLUnknown
	}
	d := 2*time.Duration(durationMs)*time.Millisecond + hlsSegmentTTLMargin
	if d < MaxProtectedTTL {
		return MaxProtectedTTL
	}
	if d > MaxHLSSegmentTTL {
		return MaxHLSSegmentTTL
	}
	return d
}

// IsHLSSegmentKey reports whether key is an HLS media segment the worker
// wrote: a ".ts" object directly under an "/hls/" directory. Only these may
// carry a signature longer than MaxProtectedTTL.
func IsHLSSegmentKey(key string) bool {
	i := strings.LastIndex(key, "/hls/")
	if i < 0 {
		return false
	}
	name := key[i+len("/hls/"):]
	return name != "" && !strings.Contains(name, "/") && strings.HasSuffix(name, ".ts") && name != ".ts"
}

// SegmentSigner is implemented by signers that can sign an HLS segment for
// longer than MaxProtectedTTL (SignProtectedSegment). A signer that does
// not is used at MaxProtectedTTL.
type SegmentSigner interface {
	SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error)
}

// SignProtectedSegment signs an HLS segment for ttl, up to MaxHLSSegmentTTL.
// Anything that is not a protected HLS segment key is refused: the longer
// lifetime must never reach an original or a rendition.
func (s *Signer) SignProtectedSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	if err := checkSegmentTTL(key, ttl); err != nil {
		return "", err
	}
	return s.signResourceUpTo(s.cdnBaseURL+"/"+key, ttl, MaxHLSSegmentTTL, now)
}

// checkSegmentTTL is the rule SignProtectedSegment enforces, shared with
// the development signer.
func checkSegmentTTL(key string, ttl time.Duration) error {
	if ClassForKey(key) != ClassProtected {
		return fmt.Errorf("delivery: %q is not a protected key", key)
	}
	if !IsHLSSegmentKey(key) {
		return fmt.Errorf("delivery: %q is not an HLS segment; only segments get a long signature", key)
	}
	if ttl <= 0 || ttl > MaxHLSSegmentTTL {
		return fmt.Errorf("delivery: segment ttl %s outside (0, %s]", ttl, MaxHLSSegmentTTL)
	}
	return nil
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
	expires time.Time
}

// signCache holds signed URLs by (key, ttl).
type signCache struct {
	mu      sync.Mutex
	entries map[signCacheKey]signCacheEntry
}

func newSignCache() *signCache {
	return &signCache{entries: make(map[signCacheKey]signCacheEntry)}
}

// reuseWindow is how long a signature of ttl is handed out again: the
// SignatureReuseWindow, but never more than half of a short ttl.
func reuseWindow(ttl time.Duration) time.Duration {
	if w := ttl / 2; w < SignatureReuseWindow {
		return w
	}
	return SignatureReuseWindow
}

// get returns a cached URL for (key, ttl) that has at least
// ttl-reuseWindow(ttl) left at now.
func (c *signCache) get(key string, ttl time.Duration, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[signCacheKey{key, ttl}]
	if !ok {
		return "", false
	}
	if e.expires.Sub(now) < ttl-reuseWindow(ttl) {
		delete(c.entries, signCacheKey{key, ttl})
		return "", false
	}
	return e.url, true
}

func (c *signCache) put(key string, ttl time.Duration, url string, expires time.Time, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= signCacheMaxEntries {
		for k, e := range c.entries {
			if e.expires.Sub(now) < k.ttl-reuseWindow(k.ttl) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= signCacheMaxEntries {
			c.entries = make(map[signCacheKey]signCacheEntry)
		}
	}
	c.entries[signCacheKey{key, ttl}] = signCacheEntry{url: url, expires: expires}
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
	g.cache.put(key, ttl, u, now.Add(ttl), now)
	return u, nil
}

// signSegment signs one HLS segment for ttl when the signer supports long
// segment signatures, and at MaxProtectedTTL otherwise.
func (g *Gate) signSegment(key string, ttl time.Duration, now time.Time) (string, error) {
	ss, ok := g.signer.(SegmentSigner)
	if !ok || ttl <= MaxProtectedTTL || !IsHLSSegmentKey(key) {
		return g.signProtected(key, MaxProtectedTTL, now)
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
		g.cache.put(key, ttl, u, now.Add(ttl), now)
	}
	return u, nil
}
