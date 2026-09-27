package service

import (
	"errors"
	"testing"
)

// One video taxonomy (2026-09-27): the 18 flick entries keep their ids and
// order with kind "all", the nine long-video entries follow, and each kind
// of composer is held to exactly the entries it may pick from.

func TestVideoCategoriesKeepsTheEighteenFlickEntriesFirst(t *testing.T) {
	all := VideoCategories()
	wantFlick := []string{"comedy", "music", "dance", "food", "travel", "sports", "education", "tech", "beauty",
		"fashion", "gaming", "fitness", "pets", "art", "news", "lifestyle", "business", "other"}
	if len(all) != len(wantFlick)+9 {
		t.Fatalf("len=%d want %d", len(all), len(wantFlick)+9)
	}
	for i, id := range wantFlick {
		c := all[i]
		if c.ID != id || c.Slug != id || c.Kind != CategoryKindAll {
			t.Fatalf("entry %d = %+v, want id/slug %q kind all", i, c, id)
		}
	}
	wantLong := []string{"film-animation", "science-tech", "howto-style", "people-blogs", "entertainment",
		"autos", "documentary", "podcasts", "kids"}
	for i, id := range wantLong {
		c := all[len(wantFlick)+i]
		if c.ID != id || c.Slug != id || c.Kind != CategoryKindLong || c.Label == "" {
			t.Fatalf("long entry %d = %+v, want %q kind long", i, c, id)
		}
	}
	// FlickCategories is byte-for-byte the historic list.
	flick := FlickCategories()
	if len(flick) != 18 {
		t.Fatalf("FlickCategories len=%d want 18", len(flick))
	}
	for i := range flick {
		if flick[i].ID != wantFlick[i] {
			t.Fatalf("FlickCategories[%d]=%q want %q", i, flick[i].ID, wantFlick[i])
		}
	}
	if got := len(LongVideoCategories()); got != 27 {
		t.Fatalf("LongVideoCategories len=%d want 27", got)
	}
	// A copy, not the shared slice.
	all[0].Label = "mutated"
	if VideoCategories()[0].Label == "mutated" {
		t.Fatal("VideoCategories returned the shared slice")
	}
}

func TestNormalizeCategoryForHoldsEachKindToItsEntries(t *testing.T) {
	cases := []struct {
		contentType, in, want string
		wantErr               error
	}{
		{"flick", "comedy", "comedy", nil},
		{"flick", "Film & animation", "", ErrInvalidCategory},
		{"flick", "podcasts", "", ErrInvalidCategory},
		{"reel", "music", "music", nil},
		{"long_video", "podcasts", "podcasts", nil},
		{"long_video", "Science-Tech ", "science-tech", nil},
		{"long_video", "comedy", "comedy", nil},
		{"long_video", "Science & Technology", "", ErrInvalidCategory},
		{"long_video", "nonprofits", "", ErrInvalidCategory},
		{"video", "kids", "kids", nil},
		{"long_video", "", "", nil},
		{"post", "podcasts", "podcasts", nil},
	}
	for _, tc := range cases {
		got, err := NormalizeCategoryFor(tc.contentType, tc.in)
		if !errors.Is(err, tc.wantErr) || got != tc.want {
			t.Errorf("NormalizeCategoryFor(%q, %q) = (%q, %v), want (%q, %v)", tc.contentType, tc.in, got, err, tc.want, tc.wantErr)
		}
	}
}

// The create path holds long videos to the taxonomy now; plain posts keep
// free text through NormalizeCategory alone.
func TestResolveCreateCategoryValidatesLongVideos(t *testing.T) {
	if got, err := resolveCreateCategory("long_video", "Documentary"); err != nil || got != "documentary" {
		t.Fatalf("long_video documentary = (%q, %v)", got, err)
	}
	if _, err := resolveCreateCategory("long_video", "Film & Animation"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("free-text long_video category accepted: %v", err)
	}
	if got, err := resolveCreateCategory("post", "Film & Animation"); err != nil || got != "film-&-animation" {
		t.Fatalf("post category = (%q, %v), want slugged free text", got, err)
	}
}
