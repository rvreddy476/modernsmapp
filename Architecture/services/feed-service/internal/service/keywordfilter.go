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
// Fail-closed, same policy as block/mute (M2-P0-3 / blocksafety_test.go):
// a failed keyword lookup is an ERROR, never "nobody filtered". The empty
// keyword set is the overwhelmingly common case and is cached like any
// other answer, so the steady-state cost is one map lookup per request.

// keywordFilterCacheTTL bounds how stale a viewer's keyword set may be.
// A keyword added in trust-safety takes effect within a minute.
const keywordFilterCacheTTL = 60 * time.Second

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
	s.kwCache[viewerID] = keywordCacheEntry{keywords: keywords, expires: now.Add(keywordFilterCacheTTL)}
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

// applyKeywordHideFilter drops every hydrated post whose visible text
// contains one of the viewer's filter keywords. Fail-closed: a lookup
// failure propagates as an error and the surface answers with an error
// response rather than an unfiltered page.
func (s *Service) applyKeywordHideFilter(ctx context.Context, viewerID uuid.UUID, posts []HydratedPost) ([]HydratedPost, error) {
	keywords, err := s.getFilterKeywords(ctx, viewerID)
	if err != nil {
		return nil, fmt.Errorf("feed unavailable: keyword filters could not be resolved: %w", err)
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
	return false
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
