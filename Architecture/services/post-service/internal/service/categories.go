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
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return "", nil
	}
	if _, ok := flickCategoryIDs[id]; !ok {
		return "", ErrInvalidCategory
	}
	return id, nil
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
