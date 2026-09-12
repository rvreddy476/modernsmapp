package service

import (
	"errors"
	"regexp"
	"strings"
)

// Flick category taxonomy — the fixed list the reel composer picks from.
//
// Fixed and server-owned on purpose. A free-text category is a tag by another
// name: two creators spell "Comedy" three ways and no surface can ever group
// them. The list lives here rather than in Android so a new category is one
// deploy, not an app release, and GET /v1/posts/categories hands the client
// exactly what the server will accept.
//
// The ids are the stored values and are part of the API; the labels are
// display text and may change freely.

// ErrInvalidCategory is a flick category outside the taxonomy.
var ErrInvalidCategory = errors.New("category is not one of the supported flick categories")

// Category is one entry of the taxonomy as the client sees it.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// flickCategories is ordered for display; keep it human-sorted, not
// alphabetical, so "other" stays last.
var flickCategories = []Category{
	{ID: "comedy", Label: "Comedy"},
	{ID: "music", Label: "Music"},
	{ID: "dance", Label: "Dance"},
	{ID: "food", Label: "Food"},
	{ID: "travel", Label: "Travel"},
	{ID: "sports", Label: "Sports"},
	{ID: "education", Label: "Education"},
	{ID: "tech", Label: "Tech"},
	{ID: "beauty", Label: "Beauty"},
	{ID: "fashion", Label: "Fashion"},
	{ID: "gaming", Label: "Gaming"},
	{ID: "fitness", Label: "Fitness"},
	{ID: "pets", Label: "Pets"},
	{ID: "art", Label: "Art"},
	{ID: "news", Label: "News"},
	{ID: "lifestyle", Label: "Lifestyle"},
	{ID: "business", Label: "Business"},
	{ID: "other", Label: "Other"},
}

var flickCategoryIDs = func() map[string]struct{} {
	m := make(map[string]struct{}, len(flickCategories))
	for _, c := range flickCategories {
		m[c.ID] = struct{}{}
	}
	return m
}()

// FlickCategories returns a copy of the taxonomy in display order. A copy, so
// no handler can reorder or mutate the shared list.
func FlickCategories() []Category {
	out := make([]Category, len(flickCategories))
	copy(out, flickCategories)
	return out
}

// NormalizeFlickCategory returns the canonical stored id for a client-supplied
// category, or ErrInvalidCategory.
//
// Empty is allowed and stays empty: the composer does not force a category,
// and an uncategorised reel is a valid reel. Case and surrounding whitespace
// are forgiven because they carry no intent — "Comedy " is not a different
// choice from "comedy" — but anything outside the list is refused rather than
// coerced to "other", because a silent remap would hide a client bug.
func NormalizeFlickCategory(raw string) (string, error) {
	id := NormalizeCategory(raw)
	if id == "" {
		return "", nil
	}
	if _, ok := flickCategoryIDs[id]; !ok {
		return "", ErrInvalidCategory
	}
	return id, nil
}

// categoryWhitespaceRe is any run of whitespace inside a category value.
var categoryWhitespaceRe = regexp.MustCompile(`\s+`)

// NormalizeCategory is the one rule for what posts.category (and
// reel_drafts.category) may hold: trimmed, lowercased, with any internal run
// of whitespace collapsed to a single hyphen. Empty in, empty out.
//
// Why one rule on every write path (2026-09-12): the column was stored as
// the client sent it, so dev held "Education" and "education", "Technology"
// and "tech", as separate values. feed-service's category pages
// (GET /v1/feed/videos?category=) and post-service's own GetRecentPosts
// filter match the stored string exactly, so mixed-case uploads fell into
// separate buckets and some pages came up empty. Lowercasing and trimming
// costs no intent ("Comedy " is the same choice as "comedy"), and the hyphen
// is chosen because both services only accept a `[a-z0-9][a-z0-9_-]*` slug
// as a filter: a stored value with a space inside could never be asked for.
//
// Synonyms ("Technology" to "tech") are deliberately NOT mapped. The only
// list that exists is the flick taxonomy above, which is enforced on flicks
// by NormalizeFlickCategory; long videos carry free text and a guessed remap
// there would hide a client bug rather than fix one. Migration 047 applies
// the same rule to the rows already stored.
func NormalizeCategory(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return ""
	}
	return categoryWhitespaceRe.ReplaceAllString(id, "-")
}

// resolveCreateCategory is what CreatePost stores for a client-supplied
// category. Every content type goes through NormalizeCategory; flicks are
// additionally held to the closed taxonomy. Long videos keep free text (the
// video classifier and the category override route write their own values
// there, and changing that contract is not this pass), so an unknown id is
// stored as its slug rather than refused. Empty stays allowed: a category is
// a choice, not a requirement.
func resolveCreateCategory(contentType, raw string) (string, error) {
	if contentType == "flick" {
		return NormalizeFlickCategory(raw)
	}
	return NormalizeCategory(raw), nil
}

// ErrInvalidCategoryFilter is a `category` query value that is not even
// shaped like a taxonomy id.
var ErrInvalidCategoryFilter = errors.New("category filter must be a lowercase slug (letters, digits, '-', '_')")

// categorySlugRe is the shape of a taxonomy id — the same rule feed-service
// applies before forwarding the value here.
var categorySlugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

// NormalizeCategoryFilter canonicalises a READ-side `category` filter
// (Tube, 2026-09-05). Unlike NormalizeFlickCategory it does not check the
// taxonomy: a filter for an id no post carries is a legitimate question
// with an empty answer, and refusing it would make feed-service's
// discovery fill log an error for a page that is simply empty. Only a
// malformed value is refused. Empty means "no filter".
func NormalizeCategoryFilter(raw string) (string, error) {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return "", nil
	}
	if !categorySlugRe.MatchString(id) {
		return "", ErrInvalidCategoryFilter
	}
	return id, nil
}
