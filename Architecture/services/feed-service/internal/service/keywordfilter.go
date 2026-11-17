package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Filter keywords ("Content preferences → Filter keywords", trust-safety
// self-service). A post whose text contains any of the viewer's keywords is
// dropped after hydration, before the response — every surface (home, reels,
// flicks, videos, watch) passes through the same HydratePosts tail, so every
// surface gets the same filter.
//
// The lookup itself is strict (M2-P0-3 / blocksafety_test.go): a failed
// keyword lookup is an ERROR, never read as "nobody filtered". The empty
// keyword set is the overwhelmingly common case and is cached like any
// other answer, so the steady-state cost is one map lookup per request.
//
// What a page does with that error changed on 17 Nov 2026. It used to fail
// the page, so every surface answered 502 for every viewer while
// trust-safety was unreachable, although almost nobody has a keyword. Now
// the page is filtered with the viewer's LAST KNOWN keywords (this process's
// copy, then the copy in Redis), and only a viewer with no known keywords is
// served without the keyword filter (keywordsOrLastKnown). This is not the
// block/mute policy and must not be copied to it: a keyword hides content
// from the viewer at the viewer's own wish, where a block also keeps the
// viewer's content away from someone else. Blocks, mutes and feed mutes
// still fail the page.

// keywordFilterCacheTTL bounds how stale a viewer's keyword set may be.
// A keyword added in trust-safety takes effect within a minute.
const keywordFilterCacheTTL = 60 * time.Second

// keywordCacheMaxEntries caps the keyword cache (audit M4): one entry per
// viewer, so without a cap every viewer since the process started stayed
// in memory. 100k viewers is a few MB; past it, boundTTLMap evicts.
const keywordCacheMaxEntries = 100000

// keywordLastKnownTTL is how long the Redis copy of a viewer's keywords is
// kept for use while trust-safety cannot be asked. Longer than any outage
// worth planning for; a keyword removed during an outage keeps hiding posts
// until trust-safety answers again, which errs on the side the viewer chose.
const keywordLastKnownTTL = 7 * 24 * time.Hour

// keywordLastKnownKey holds a viewer's last non-empty keyword set as JSON.
// Only non-empty sets are stored: no key means "no keywords known", which is
// what an empty set means too, so the common case costs Redis nothing.
func keywordLastKnownKey(viewerID uuid.UUID) string {
	return "feed:kwfilter:last:" + viewerID.String()
}

type keywordCacheEntry struct {
	keywords []string
	expires  time.Time
}

// getFilterKeywords returns the viewer's hide keywords, serving from the
// 60s in-process cache when fresh. Errors are NOT cached: the next request
// retries rather than serving an unfiltered minute.
func (s *Service) getFilterKeywords(ctx context.Context, viewerID uuid.UUID) ([]string, error) {
	now := time.Now()

	s.kwMu.Lock()
	if entry, ok := s.kwCache[viewerID]; ok && now.Before(entry.expires) {
		kws := entry.keywords
		s.kwMu.Unlock()
		return kws, nil
	}
	s.kwMu.Unlock()

	keywords, err := s.fetchFilterKeywords(ctx, viewerID)
	if err != nil {
		return nil, err
	}

	s.kwMu.Lock()
	if s.kwCache == nil {
		s.kwCache = make(map[uuid.UUID]keywordCacheEntry)
	}
	previous, hadPrevious := s.kwCache[viewerID]
	s.rememberKeywords(ctx, viewerID, keywords, hadPrevious && len(previous.keywords) > 0)
	s.kwCache[viewerID] = keywordCacheEntry{keywords: keywords, expires: now.Add(keywordFilterCacheTTL)}
	boundTTLMap(s.kwCache, keywordCacheMaxEntries, now, func(e keywordCacheEntry) time.Time { return e.expires })
	s.kwMu.Unlock()
	return keywords, nil
}

// fetchFilterKeywords calls trust-safety's internal endpoint. Any non-200,
// transport failure, or malformed body is an error — an empty list must be
// a positive answer, never a decode accident (same reasoning as
// getBlockedAndMuted).
func (s *Service) fetchFilterKeywords(ctx context.Context, viewerID uuid.UUID) ([]string, error) {
	url := fmt.Sprintf("%s/v1/internal/keyword-filters?user_id=%s", s.trustSafetyURL, viewerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := s.trustClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trust-safety keyword-filters request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("trust-safety keyword-filters returned %d: %s", resp.StatusCode, string(body))
	}
	var envelope struct {
		Data struct {
			Keywords []string `json:"keywords"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode keyword-filters: %w", err)
	}
	if envelope.Data.Keywords == nil {
		return []string{}, nil
	}
	return envelope.Data.Keywords, nil
}

// rememberKeywords keeps the Redis copy of a freshly fetched set in step:
// a non-empty set is stored, and a set that has become empty removes the
// copy this process knows it stored. Best effort: a Redis failure only
// means there is less to fall back on.
func (s *Service) rememberKeywords(ctx context.Context, viewerID uuid.UUID, keywords []string, hadKeywords bool) {
	if s.rdb == nil {
		return
	}
	if len(keywords) == 0 {
		if hadKeywords {
			s.rdb.Del(ctx, keywordLastKnownKey(viewerID))
		}
		return
	}
	raw, err := json.Marshal(keywords)
	if err != nil {
		return
	}
	s.rdb.Set(ctx, keywordLastKnownKey(viewerID), raw, keywordLastKnownTTL)
}

// Where a page's keywords came from, for the fallback counter.
const (
	keywordSourceFresh       = "fresh"
	keywordSourceStaleMemory = "stale_memory"
	keywordSourceStaleRedis  = "stale_redis"
	keywordSourceNone        = "none"
)

// keywordsOrLastKnown is getFilterKeywords for a page that must be served:
// when trust-safety cannot be asked it returns the viewer's last known
// keywords (this process's expired cache entry, then the Redis copy), and
// with neither, no keywords. The source says which, and every source but
// "fresh" is counted.
func (s *Service) keywordsOrLastKnown(ctx context.Context, viewerID uuid.UUID) ([]string, string) {
	keywords, err := s.getFilterKeywords(ctx, viewerID)
	if err == nil {
		return keywords, keywordSourceFresh
	}

	s.kwMu.Lock()
	entry, ok := s.kwCache[viewerID]
	s.kwMu.Unlock()
	if ok {
		keywordFilterFallbacks.WithLabelValues(keywordSourceStaleMemory).Inc()
		return entry.keywords, keywordSourceStaleMemory
	}

	if s.rdb != nil {
		if raw, rerr := s.rdb.Get(ctx, keywordLastKnownKey(viewerID)).Bytes(); rerr == nil {
			var stored []string
			if json.Unmarshal(raw, &stored) == nil && len(stored) > 0 {
				keywordFilterFallbacks.WithLabelValues(keywordSourceStaleRedis).Inc()
				return stored, keywordSourceStaleRedis
			}
		}
	}

	keywordFilterFallbacks.WithLabelValues(keywordSourceNone).Inc()
	return nil, keywordSourceNone
}

// applyKeywordHideFilter drops every hydrated post whose visible text
// contains one of the viewer's filter keywords. A failed trust-safety lookup
// does not fail the page: it is filtered with the viewer's last known
// keywords (keywordsOrLastKnown). Feed mutes are this service's own data and
// still fail the page when they cannot be read.
func (s *Service) applyKeywordHideFilter(ctx context.Context, viewerID uuid.UUID, posts []HydratedPost) ([]HydratedPost, error) {
	keywords, _ := s.keywordsOrLastKnown(ctx, viewerID)
	// Topics and hashtags muted through /v1/feed/mute (feedmutes.go).
	mutes, err := s.feedMutes(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: feed mutes could not be resolved: %w", err)
	}
	if len(mutes.words) > 0 {
		keywords = append(append(make([]string, 0, len(keywords)+len(mutes.words)), keywords...), mutes.words...)
	}
	return filterPostsByKeywords(posts, keywords), nil
}

// filterPostsByKeywords is the pure filtering step, split out for tests.
func filterPostsByKeywords(posts []HydratedPost, keywords []string) []HydratedPost {
	if len(keywords) == 0 || len(posts) == 0 {
		return posts
	}
	out := posts[:0]
	for _, p := range posts {
		if postMatchesAnyKeyword(p, keywords) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// postMatchesAnyKeyword checks every text surface the client renders:
// the caption (Text), rich-text payload, and each media alt text.
func postMatchesAnyKeyword(p HydratedPost, keywords []string) bool {
	if textContainsAnyKeyword(p.Text, keywords) {
		return true
	}
	if len(p.RichText) > 0 && textContainsAnyKeyword(richTextPlain(p.RichText), keywords) {
		return true
	}
	for _, m := range p.Media {
		if textContainsAnyKeyword(m.AltText, keywords) {
			return true
		}
	}
	return quotedNoteMatchesAnyKeyword(p.QuotedNote, keywords)
}

// quotedNoteMatchesAnyKeyword checks an Echo quote's embedded quoted_note
// (2026-11-13) on the same surfaces. A hit drops the WHOLE quote, which is
// how Home already treats shared content: a repost row IS its original
// post, so an original that hits a keyword takes the repost row with it
// (and a blocked or muted original author does too, repostfilter.go).
// Home has no partial preview to hide, so neither does Echo:
// the outer note goes, rather than being served with quoted_note blanked.
//
// quoted_note is one level deep (contract), so this never recurses
// further. A quoted_note that does not decode is matched on every string
// it holds, so a malformed preview can only be dropped, never let through.
func quotedNoteMatchesAnyKeyword(raw json.RawMessage, keywords []string) bool {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false
	}
	var q HydratedPost
	if err := json.Unmarshal(raw, &q); err != nil {
		return textContainsAnyKeyword(richTextPlain(raw), keywords)
	}
	q.QuotedNote = nil
	return postMatchesAnyKeyword(q, keywords)
}

// richTextPlain flattens a rich-text JSON payload to the string values it
// contains, joined by spaces. It walks the structure generically (any
// nesting of objects/arrays) so a schema change cannot silently blind the
// filter; non-string leaves are ignored.
func richTextPlain(raw json.RawMessage) string {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		// Not decodable — fall back to matching the raw bytes; string
		// boundaries in JSON are quotes, which are non-word runes.
		return string(raw)
	}
	var sb strings.Builder
	collectStrings(v, &sb)
	return sb.String()
}

func collectStrings(v interface{}, sb *strings.Builder) {
	switch t := v.(type) {
	case string:
		if sb.Len() > 0 {
			sb.WriteByte(' ')
		}
		sb.WriteString(t)
	case []interface{}:
		for _, e := range t {
			collectStrings(e, sb)
		}
	case map[string]interface{}:
		for _, e := range t {
			collectStrings(e, sb)
		}
	}
}

// textContainsAnyKeyword reports whether text contains any keyword as a
// whole word (case-insensitive, unicode-aware). Keywords arrive already
// lowercased and '#'-stripped from trust-safety, but are lowered again here
// so the matcher is safe standalone.
func textContainsAnyKeyword(text string, keywords []string) bool {
	if text == "" {
		return false
	}
	lowered := strings.ToLower(text)
	for _, kw := range keywords {
		if containsWholeWord(lowered, strings.ToLower(kw)) {
			return true
		}
	}
	return false
}

// containsWholeWord reports whether keyword occurs in text bounded by
// non-word runes (anything that is not a letter or digit). '#' is a
// non-word rune, so the keyword "tag" matches the hashtag "#tag"; the
// keyword "cat" does NOT match "category" or "concatenate". Both inputs
// must already be lowercased.
func containsWholeWord(text, keyword string) bool {
	if keyword == "" {
		return false
	}
	for from := 0; ; {
		idx := strings.Index(text[from:], keyword)
		if idx < 0 {
			return false
		}
		start := from + idx
		end := start + len(keyword)
		if isWordBoundary(text, start, end) {
			return true
		}
		// Advance one rune past this occurrence and keep looking.
		_, size := utf8.DecodeRuneInString(text[start:])
		if size == 0 {
			return false
		}
		from = start + size
	}
}

func isWordBoundary(text string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(text[:start])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	if end < len(text) {
		r, _ := utf8.DecodeRuneInString(text[end:])
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
	}
	return true
}
