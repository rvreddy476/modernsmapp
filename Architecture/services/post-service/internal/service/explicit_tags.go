package service

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Explicit hashtags and mentions (founder, 2026-09-05).
//
// The reel studio has separate fields for hashtags and mentions; they are
// not mixed into the caption. `POST /v1/posts` therefore accepts
// `hashtags: ["tag", …]` and `mentions: ["username", …]` next to `text`.
// Both are MERGED with what the caption parser still extracts (an old
// client, or a user who types `#tag` anyway), deduped, and persisted
// through the same tables the parsed ones always used — posts.hashtags,
// hashtag use-counts, post_mentions — so hashtag pages, search and mention
// notifications behave as if the tags had been in the text. The caption is
// stored as given.

const (
	// MaxExplicitHashtags bounds the `hashtags` field.
	MaxExplicitHashtags = 30
	// MaxHashtagLength is the per-tag ceiling (after the leading # is dropped).
	MaxHashtagLength = 50
	// MaxExplicitMentions bounds the `mentions` field.
	MaxExplicitMentions = 20
	// MaxMentionLength is the per-username ceiling (after the leading @).
	MaxMentionLength = 30

	// maxMergedHashtags / maxMergedMentions cap the union of parsed and
	// explicit tags. Parsed hashtags were already capped at 20 and parsed
	// mentions at maxMentionsPerPost (10, a fan-out guard: one user-service
	// lookup per mention); the explicit fields add their own ceilings.
	maxMergedHashtags = 20 + MaxExplicitHashtags
	maxMergedMentions = maxMentionsPerPost + MaxExplicitMentions
)

var (
	ErrTooManyHashtags = errors.New("too many hashtags")
	ErrInvalidHashtag  = errors.New("invalid hashtag")
	ErrTooManyMentions = errors.New("too many mentions")
	ErrInvalidMention  = errors.New("invalid mention")

	// explicitHashtagPattern is the contract — letters, digits and underscore
	// in any script, nothing else, after the leading # is stripped — plus
	// combining marks (\p{M}), which Indic, Thai and Arabic words need and
	// which the caption parser (hashtagRegex) already accepts. A superset of
	// the wire contract: anything the client accepts, the server accepts.
	explicitHashtagPattern = regexp.MustCompile(`^[\p{L}\p{M}\p{N}_]+$`)
	// explicitMentionPattern mirrors dbMentionRegex's alphabet (post_mentions
	// stores the username; user-service resolves it at notification time).
	explicitMentionPattern = regexp.MustCompile(`^[A-Za-z0-9_.]+$`)
)

// NormalizeExplicitHashtags validates and canonicalises the `hashtags`
// field: at most MaxExplicitHashtags entries, each 1–MaxHashtagLength
// characters of `^[\p{L}\p{N}_]+$` after a leading # is dropped, lowercased
// for the index, deduped in order. nil in, nil out.
func NormalizeExplicitHashtags(raw []string) ([]string, error) {
	if len(raw) > MaxExplicitHashtags {
		return nil, fmt.Errorf("%w: at most %d", ErrTooManyHashtags, MaxExplicitHashtags)
	}
	var out []string
	seen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		tag := strings.TrimPrefix(strings.TrimSpace(r), "#")
		if tag == "" || len([]rune(tag)) > MaxHashtagLength || !explicitHashtagPattern.MatchString(tag) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidHashtag, r)
		}
		tag = strings.ToLower(tag)
		if _, dup := seen[tag]; dup {
			continue
		}
		seen[tag] = struct{}{}
		out = append(out, tag)
	}
	return out, nil
}

// NormalizeExplicitMentions validates and canonicalises the `mentions`
// field: at most MaxExplicitMentions usernames, each 1–MaxMentionLength
// characters of the username alphabet after a leading @ is dropped, deduped
// in order. Case is kept: usernames resolve through user-service, which
// owns their case rules.
func NormalizeExplicitMentions(raw []string) ([]string, error) {
	if len(raw) > MaxExplicitMentions {
		return nil, fmt.Errorf("%w: at most %d", ErrTooManyMentions, MaxExplicitMentions)
	}
	var out []string
	seen := make(map[string]struct{}, len(raw))
	for _, r := range raw {
		name := strings.TrimPrefix(strings.TrimSpace(r), "@")
		if name == "" || len(name) > MaxMentionLength || !explicitMentionPattern.MatchString(name) {
			return nil, fmt.Errorf("%w: %q", ErrInvalidMention, r)
		}
		key := strings.ToLower(name)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, name)
	}
	return out, nil
}

// mergeTags is the union of the caption-parsed list and the explicit list:
// parsed first (so an old client's behaviour is unchanged), explicit after,
// duplicates dropped case-insensitively, bounded by max. The parsed list is
// already canonical (lowercase hashtags; usernames as typed).
func mergeTags(parsed, explicit []string, max int) []string {
	if len(parsed) == 0 && len(explicit) == 0 {
		return parsed
	}
	out := make([]string, 0, len(parsed)+len(explicit))
	seen := make(map[string]struct{}, len(parsed)+len(explicit))
	add := func(v string) {
		key := strings.ToLower(v)
		if _, dup := seen[key]; dup {
			return
		}
		seen[key] = struct{}{}
		out = append(out, v)
	}
	for _, v := range parsed {
		add(v)
	}
	for _, v := range explicit {
		add(v)
	}
	if max > 0 && len(out) > max {
		out = out[:max]
	}
	return out
}
