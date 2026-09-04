package service

import (
	"errors"
	"testing"
)

// The taxonomy is the contract the reel composer builds its picker from, so
// every id the endpoint advertises must be accepted, and nothing else may be.
func TestNormalizeFlickCategory(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr error
	}{
		{"empty stays empty", "", "", nil},
		{"whitespace is empty", "   ", "", nil},
		{"exact id", "comedy", "comedy", nil},
		{"case is forgiven", "Comedy", "comedy", nil},
		{"surrounding whitespace is forgiven", "  music ", "music", nil},
		{"last entry", "other", "other", nil},
		{"unknown id", "cooking", "", ErrInvalidCategory},
		{"label is not an id", "Food & Cooking", "", ErrInvalidCategory},
		{"legacy topic slug", "science-technology", "", ErrInvalidCategory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeFlickCategory(tc.in)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err=%v want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}

// Every advertised id round-trips, so the client can never be handed a
// choice the server then refuses.
func TestFlickCategoriesAreAllAccepted(t *testing.T) {
	cats := FlickCategories()
	if len(cats) != 18 {
		t.Fatalf("taxonomy has %d entries, want 18", len(cats))
	}
	seen := map[string]bool{}
	for _, c := range cats {
		if c.ID == "" || c.Label == "" {
			t.Fatalf("entry %+v is missing id or label", c)
		}
		if seen[c.ID] {
			t.Fatalf("duplicate id %q", c.ID)
		}
		seen[c.ID] = true
		if got, err := NormalizeFlickCategory(c.ID); err != nil || got != c.ID {
			t.Fatalf("advertised id %q not accepted: got %q err %v", c.ID, got, err)
		}
	}
	if cats[len(cats)-1].ID != "other" {
		t.Fatalf("\"other\" must stay last for the picker; last is %q", cats[len(cats)-1].ID)
	}
}

// The copy must not alias the shared list.
func TestFlickCategoriesReturnsACopy(t *testing.T) {
	a := FlickCategories()
	a[0].Label = "mutated"
	if FlickCategories()[0].Label == "mutated" {
		t.Fatal("FlickCategories exposed the shared slice")
	}
}
