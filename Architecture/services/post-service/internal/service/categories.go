package service

import (
	"errors"
	"regexp"
	"strings"
)

// Video category taxonomy — ONE list for flicks and long videos (MTube,
// 2026-09-27; the flick-only list dates from 2026-09-12).
//
// Fixed and server-owned on purpose. A free-text category is a tag by another
// name: two creators spell "Comedy" three ways and no surface can ever group
// them. The list lives here rather than in a client so a new category is one
// deploy, not an app release, and GET /v1/posts/categories hands the client
// exactly what the server will accept.
//
// Every entry carries a Kind: "all" is offered to both composers, "long" only
// to the Tube studio, "short" only to the reel composer (none today). The 18
// original flick entries are Kind "all", so the reel composer's picker is
// byte-for-byte what it was; the long-video entries are additive.
//
// The ids are the stored values and are part of the API; the labels are
// display text and may change freely. On the wire an entry is
// {id, slug, label, kind}: `id` is the key the Android reel composer has read
// since 2026-09-12 and `slug` is the same value under the name the MTube web
// contract uses — both are served so neither client has to change.

// ErrInvalidCategory is a category outside the taxonomy for that content type.
var ErrInvalidCategory = errors.New("category is not one of the supported categories")

// Category kinds.
const (
	CategoryKindAll   = "all"
	CategoryKindShort = "short"
	CategoryKindLong  = "long"
)

// Category is one entry of the taxonomy as the client sees it.
type Category struct {
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Label string `json:"label"`
	Kind  string `json:"kind"`
}

// videoCategories is ordered for display; keep it human-sorted, not
// alphabetical, so "other" stays last among the shared entries and the
// long-video entries follow in their own display order.
var videoCategories = []Category{
	{ID: "comedy", Slug: "comedy", Label: "Comedy", Kind: CategoryKindAll},
	{ID: "music", Slug: "music", Label: "Music", Kind: CategoryKindAll},
	{ID: "dance", Slug: "dance", Label: "Dance", Kind: CategoryKindAll},
	{ID: "food", Slug: "food", Label: "Food", Kind: CategoryKindAll},
	{ID: "travel", Slug: "travel", Label: "Travel", Kind: CategoryKindAll},
	{ID: "sports", Slug: "sports", Label: "Sports", Kind: CategoryKindAll},
	{ID: "education", Slug: "education", Label: "Education", Kind: CategoryKindAll},
	{ID: "tech", Slug: "tech", Label: "Tech", Kind: CategoryKindAll},
	{ID: "beauty", Slug: "beauty", Label: "Beauty", Kind: CategoryKindAll},
	{ID: "fashion", Slug: "fashion", Label: "Fashion", Kind: CategoryKindAll},
	{ID: "gaming", Slug: "gaming", Label: "Gaming", Kind: CategoryKindAll},
	{ID: "fitness", Slug: "fitness", Label: "Fitness", Kind: CategoryKindAll},
	{ID: "pets", Slug: "pets", Label: "Pets", Kind: CategoryKindAll},
	{ID: "art", Slug: "art", Label: "Art", Kind: CategoryKindAll},
	{ID: "news", Slug: "news", Label: "News", Kind: CategoryKindAll},
	{ID: "lifestyle", Slug: "lifestyle", Label: "Lifestyle", Kind: CategoryKindAll},
	{ID: "business", Slug: "business", Label: "Business", Kind: CategoryKindAll},
	{ID: "other", Slug: "other", Label: "Other", Kind: CategoryKindAll},
	// Long-video only (MTube studio).
	{ID: "film-animation", Slug: "film-animation", Label: "Film & animation", Kind: CategoryKindLong},
	{ID: "science-tech", Slug: "science-tech", Label: "Science & tech", Kind: CategoryKindLong},
	{ID: "howto-style", Slug: "howto-style", Label: "How-to & style", Kind: CategoryKindLong},
	{ID: "people-blogs", Slug: "people-blogs", Label: "People & blogs", Kind: CategoryKindLong},
	{ID: "entertainment", Slug: "entertainment", Label: "Entertainment", Kind: CategoryKindLong},
	{ID: "autos", Slug: "autos", Label: "Autos & vehicles", Kind: CategoryKindLong},
	{ID: "documentary", Slug: "documentary", Label: "Documentary", Kind: CategoryKindLong},
	{ID: "podcasts", Slug: "podcasts", Label: "Podcasts", Kind: CategoryKindLong},
	{ID: "kids", Slug: "kids", Label: "Kids", Kind: CategoryKindLong},
}

var categoryKindByID = func() map[string]string {
	m := make(map[string]string, len(videoCategories))
	for _, c := range videoCategories {
		m[c.ID] = c.Kind
	}
	return m
}()

// VideoCategories returns a copy of the whole taxonomy in display order. A
// copy, so no handler can reorder or mutate the shared list.
func VideoCategories() []Category {
	out := make([]Category, len(videoCategories))
	copy(out, videoCategories)
	return out
}

// FlickCategories returns the entries the reel composer may pick from (Kind
// "all" or "short"), in display order. This is exactly the 18-entry list the
// endpoint served before the long-video entries were added.
func FlickCategories() []Category {
	return categoriesOfKinds(CategoryKindAll, CategoryKindShort)
}

// LongVideoCategories returns the entries the Tube studio may pick from
// (Kind "all" or "long"), in display order.
func LongVideoCategories() []Category {
	return categoriesOfKinds(CategoryKindAll, CategoryKindLong)
}

func categoriesOfKinds(kinds ...string) []Category {
	out := make([]Category, 0, len(videoCategories))
	for _, c := range videoCategories {
		for _, k := range kinds {
			if c.Kind == k {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// categoryAllowedFor reports whether a taxonomy id may be stored on a post of
// the given content type. Flicks (and the legacy "reel" spelling) take "all"
// and "short"; long videos (and the legacy "video" spelling) take "all" and
// "long"; anything else takes the whole list.
func categoryAllowedFor(contentType, id string) bool {
	kind, ok := categoryKindByID[id]
	if !ok {
		return false
	}
	switch contentType {
	case "flick", "reel":
		return kind == CategoryKindAll || kind == CategoryKindShort
	case "long_video", "video":
		return kind == CategoryKindAll || kind == CategoryKindLong
	}
	return true
}

// NormalizeFlickCategory returns the canonical stored id for a client-supplied
// flick category, or ErrInvalidCategory.
//
// Empty is allowed and stays empty: the composer does not force a category,
// and an uncategorised reel is a valid reel. Case and surrounding whitespace
// are forgiven because they carry no intent — "Comedy " is not a different
// choice from "comedy" — but anything outside the list is refused rather than
// coerced to "other", because a silent remap would hide a client bug.
func NormalizeFlickCategory(raw string) (string, error) {
	return NormalizeCategoryFor("flick", raw)
}

// NormalizeCategoryFor is NormalizeFlickCategory for any content type: the
// value is canonicalised by NormalizeCategory and then held to the entries
// categoryAllowedFor admits for that type. Empty stays empty.
func NormalizeCategoryFor(contentType, raw string) (string, error) {
	id := NormalizeCategory(raw)
	if id == "" {
		return "", nil
	}
	if !categoryAllowedFor(contentType, id) {
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
// Synonyms ("Technology" to "tech") are deliberately NOT mapped here. Since
// 2026-09-27 long videos are held to the taxonomy too (NormalizeCategoryFor),
// and migration 051 folded the free-text values that were already stored.
func NormalizeCategory(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if id == "" {
		return ""
	}
	return categoryWhitespaceRe.ReplaceAllString(id, "-")
}

// resolveCreateCategory is what CreatePost stores for a client-supplied
// category. Flicks and long videos are held to the closed taxonomy for their
// kind (422 INVALID_CATEGORY at the route); every other content type goes
// through NormalizeCategory alone, because a plain post's category is not a
// taxonomy concept. Empty stays allowed: a category is a choice, not a
// requirement.
func resolveCreateCategory(contentType, raw string) (string, error) {
	switch contentType {
	case "flick", "reel", "long_video", "video":
		return NormalizeCategoryFor(contentType, raw)
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
