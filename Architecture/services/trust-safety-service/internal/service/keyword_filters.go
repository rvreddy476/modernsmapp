package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Self-service keyword filters ("Content preferences → Filter keywords").
//
// A user keeps up to MaxUserKeywords words; feed-service hides any post whose
// text contains one of them. The list is the user's own and nobody else's:
// it is stored under scope='user' with scope_id = the user, action='hide',
// and is read back by feed-service over the internal endpoint. Nothing here
// is ever shared with other users or with the post author.

const (
	// MaxUserKeywords is the hard cap on a single user's filter list.
	MaxUserKeywords = 50
	// MaxKeywordRunes bounds one keyword after trimming. Long enough for a
	// short phrase, short enough that nobody stores a paragraph.
	MaxKeywordRunes = 40
)

var (
	// ErrTooManyKeywords: the submitted list is over MaxUserKeywords.
	ErrTooManyKeywords = errors.New("too many keywords")
	// ErrInvalidKeyword: a keyword is empty after trimming, too long, or
	// contains control characters.
	ErrInvalidKeyword = errors.New("invalid keyword")
)

// NormalizeKeywords validates and canonicalises a submitted keyword list.
//
// Each entry is whitespace-trimmed, has one leading '#' stripped (a user who
// types "#tag" means the word "tag" — feed-service matches both spellings),
// is lower-cased, and must be 1..MaxKeywordRunes runes with no control
// characters. Duplicates after normalisation collapse to the first
// occurrence, so the returned list is at most len(raw) long and order-stable.
//
// The count check runs on the raw input: submitting 60 entries that dedupe
// to 3 is still a client that ignored the limit, and the response should
// say so rather than quietly succeed.
func NormalizeKeywords(raw []string) ([]string, error) {
	if len(raw) > MaxUserKeywords {
		return nil, fmt.Errorf("%w: %d submitted, limit is %d", ErrTooManyKeywords, len(raw), MaxUserKeywords)
	}
	out := make([]string, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for i, entry := range raw {
		k, err := normalizeKeyword(entry)
		if err != nil {
			return nil, fmt.Errorf("%w at index %d: %v", ErrInvalidKeyword, i, err)
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, k)
	}
	return out, nil
}

func normalizeKeyword(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", errors.New("not valid UTF-8")
	}
	k := strings.TrimSpace(s)
	k = strings.TrimPrefix(k, "#")
	k = strings.TrimSpace(k)
	k = strings.ToLower(k)
	if k == "" {
		return "", errors.New("empty")
	}
	if n := utf8.RuneCountInString(k); n > MaxKeywordRunes {
		return "", fmt.Errorf("%d characters, limit is %d", n, MaxKeywordRunes)
	}
	for _, r := range k {
		if unicode.IsControl(r) {
			return "", errors.New("contains a control character")
		}
	}
	// Collapse interior runs of whitespace so "a   b" and "a b" are the same
	// filter — feed-service matches on word sequences, not raw bytes.
	k = strings.Join(strings.Fields(k), " ")
	return k, nil
}

// GetUserKeywordFilters returns the user's own hide list. Never nil: an
// empty list is a real answer, not an absence.
func (s *Service) GetUserKeywordFilters(ctx context.Context, userID uuid.UUID) ([]string, error) {
	if s.extras == nil {
		return nil, errors.New("keyword filters are unavailable")
	}
	keywords, err := s.extras.ListUserHideKeywords(ctx, userID)
	if err != nil {
		return nil, err
	}
	if keywords == nil {
		keywords = []string{}
	}
	return keywords, nil
}

// ReplaceUserKeywordFilters validates, normalises, and atomically replaces
// the user's hide list. Returns the stored (normalised) list.
func (s *Service) ReplaceUserKeywordFilters(ctx context.Context, userID uuid.UUID, raw []string) ([]string, error) {
	// Validate before checking store availability: a bad request is a bad
	// request even when the store is down, and the caller should hear 400.
	keywords, err := NormalizeKeywords(raw)
	if err != nil {
		return nil, err
	}
	if s.extras == nil {
		return nil, errors.New("keyword filters are unavailable")
	}
	if err := s.extras.ReplaceUserHideKeywords(ctx, userID, keywords); err != nil {
		return nil, err
	}
	return keywords, nil
}
