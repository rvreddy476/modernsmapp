package events

import (
	"encoding/json"
	"log/slog"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/atpost/search-service/internal/store/search"
)

// Creator metadata edits (2026-11-17, long-video finding lv-29 / LV-A-5).
//
// Editing a published post's title, text, tags, hashtags, category or
// language must reach search. The carrier is the existing
// PostSearchEligibilityChanged event: post-service emits it from the edit
// transaction with a bumped search_rev, and applySearchEligibility replaces
// the document whole at that revision. That gives the two properties an
// edit needs for free:
//
//   - ordered: the projection script refuses a revision at or below the
//     stored one, so a replayed or reordered older edit cannot overwrite
//     a newer title;
//   - gated: a non-public, unapproved, scheduled, deleted or members-only
//     post takes the removal path, so an edit never indexes a video that
//     is not publicly visible.
//
// The title and text already ride on the event. The fields below are
// optional additions to the payload (post-service lane contract, see the
// lane report). They are read with a local decode of the raw payload, as
// tier_required_id is (membership_gate.go), so this consumer works before
// and after the producer starts sending them: absent fields index nothing.
//
//	hashtags  []string  the post's own hashtag column (merged with the
//	                    #tags parsed from text)
//	tags      []string  creator tags (searched)
//	category  string    category slug (keyword)
//	language  string    language code (keyword)

// postContentPayload is the subset of a post payload this file reads.
// RawMessage per field so one malformed field never discards the others.
type postContentPayload struct {
	Hashtags json.RawMessage `json:"hashtags"`
	Tags     json.RawMessage `json:"tags"`
	Category json.RawMessage `json:"category"`
	Language json.RawMessage `json:"language"`
}

// Bounds on what a payload may put on a document. post-service validates
// its own limits; these only keep a malformed event from bloating the index.
const (
	maxContentTags    = 50
	maxContentTagLen  = 100
	maxCategoryLen    = 64
	maxLanguageLen    = 16
	maxExtraHashtags  = 100
	maxHashtagRuneLen = 100
)

// hashtagTokenRegex is a whole hashtag as hashtagRegex captures one.
var hashtagTokenRegex = regexp.MustCompile(`^\w+$`)

// applyContentFields sets doc.Hashtags (text tags ∪ payload hashtags),
// Tags, Category and Language from the raw payload. A field of the wrong
// JSON type is skipped with a warning: it is decoration on an eligible
// document, so holding the partition for it would be worse than indexing
// without it.
func applyContentFields(doc *search.PostDoc, raw json.RawMessage) {
	var p postContentPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			slog.Warn("search: post content fields unreadable; indexing without them",
				"post_id", doc.PostID, "error", err)
			p = postContentPayload{}
		}
	}
	doc.Hashtags = mergeHashtags(doc.Hashtags, stringList(doc.PostID, "hashtags", p.Hashtags))
	doc.Tags = normalizeTags(stringList(doc.PostID, "tags", p.Tags))
	doc.Category = keywordValue(doc.PostID, "category", p.Category, maxCategoryLen)
	doc.Language = keywordValue(doc.PostID, "language", p.Language, maxLanguageLen)
}

// stringList decodes a JSON string array; null or absent is empty.
func stringList(postID, field string, v json.RawMessage) []string {
	if len(v) == 0 || string(v) == "null" {
		return nil
	}
	var out []string
	if err := json.Unmarshal(v, &out); err != nil {
		slog.Warn("search: post content field has the wrong type; skipped",
			"post_id", postID, "field", field)
		return nil
	}
	return out
}

// keywordValue decodes a JSON string as a lowercase trimmed keyword.
// Empty, wrong-typed or over-long values index nothing.
func keywordValue(postID, field string, v json.RawMessage, maxLen int) string {
	if len(v) == 0 || string(v) == "null" {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		slog.Warn("search: post content field has the wrong type; skipped",
			"post_id", postID, "field", field)
		return ""
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) > maxLen {
		return ""
	}
	return s
}

// mergeHashtags appends payload hashtags (leading '#' stripped, lowercased,
// one \w+ token each) to the tags parsed from text, without duplicates.
func mergeHashtags(fromText, extra []string) []string {
	if len(extra) == 0 {
		return fromText
	}
	seen := make(map[string]struct{}, len(fromText)+len(extra))
	out := make([]string, 0, len(fromText)+len(extra))
	for _, t := range fromText {
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	for i, raw := range extra {
		if i >= maxExtraHashtags {
			break
		}
		t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(raw), "#"))
		// Same token rule as the text parser (hashtagRegex): one \w+ run.
		if utf8.RuneCountInString(t) > maxHashtagRuneLen || !hashtagTokenRegex.MatchString(t) {
			continue
		}
		if _, ok := seen[t]; !ok {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// normalizeTags trims, drops empty and over-long tags, and dedupes
// case-insensitively (first spelling wins). Tags stay as typed otherwise:
// they are analysed text in the index.
func normalizeTags(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, raw := range in {
		t := strings.TrimSpace(raw)
		if t == "" || utf8.RuneCountInString(t) > maxContentTagLen {
			continue
		}
		k := strings.ToLower(t)
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, t)
		if len(out) == maxContentTags {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
