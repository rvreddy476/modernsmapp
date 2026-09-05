package service

import (
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Scheduled publish (2026-09-05): the window, and the author-only gate.

func TestValidatePublishAtWindow(t *testing.T) {
	now := time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC)
	at := func(d time.Duration) *time.Time { v := now.Add(d); return &v }
	cases := []struct {
		name string
		in   *time.Time
		ok   bool
	}{
		{"nil publishes now", nil, true},
		{"zero time is invalid", &time.Time{}, false},
		{"in the past", at(-time.Hour), false},
		{"now", at(0), false},
		{"2 minutes out is too soon", at(2 * time.Minute), false},
		{"4m59s out is too soon", at(5*time.Minute - time.Second), false},
		{"exactly 5 minutes is allowed", at(5 * time.Minute), true},
		{"6 minutes", at(6 * time.Minute), true},
		{"a week", at(7 * 24 * time.Hour), true},
		{"exactly 30 days is allowed", at(30 * 24 * time.Hour), true},
		{"30 days + 1s is too far", at(30*24*time.Hour + time.Second), false},
		{"a year", at(365 * 24 * time.Hour), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidatePublishAt(tc.in, now)
			if tc.ok && err != nil {
				t.Fatalf("want ok, got %v", err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidPublishAt) {
				t.Fatalf("want ErrInvalidPublishAt, got %v", err)
			}
		})
	}
}

func TestHiddenWhileScheduledIsAuthorOnly(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	future := time.Now().Add(time.Hour)
	scheduled := &postgres.Post{AuthorID: author, PublishAt: &future}
	live := &postgres.Post{AuthorID: author}

	if hiddenWhileScheduled(scheduled, &author) {
		t.Fatal("the author must see their own scheduled post")
	}
	if !hiddenWhileScheduled(scheduled, &stranger) {
		t.Fatal("another viewer must not see a scheduled post")
	}
	if !hiddenWhileScheduled(scheduled, nil) {
		t.Fatal("an anonymous viewer must not see a scheduled post")
	}
	if hiddenWhileScheduled(live, &stranger) || hiddenWhileScheduled(live, nil) {
		t.Fatal("a live post is not gated by the schedule rule")
	}
	if hiddenWhileScheduled(nil, &stranger) {
		t.Fatal("nil post is not hidden")
	}
}

// hiddenFromViewer is what every page filter applies: either gate hides.
func TestHiddenFromViewerFoldsBothGates(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	future := time.Now().Add(time.Hour)
	cases := []struct {
		name   string
		post   *postgres.Post
		viewer *uuid.UUID
		hidden bool
	}{
		{"scheduled, stranger", &postgres.Post{AuthorID: author, PublishAt: &future}, &stranger, true},
		{"scheduled, author", &postgres.Post{AuthorID: author, PublishAt: &future}, &author, false},
		{"processing, stranger", &postgres.Post{AuthorID: author, IsProcessing: true}, &stranger, true},
		{"processing, author", &postgres.Post{AuthorID: author, IsProcessing: true}, &author, false},
		{"scheduled and processing, author", &postgres.Post{AuthorID: author, IsProcessing: true, PublishAt: &future}, &author, false},
		{"scheduled and processing, anonymous", &postgres.Post{AuthorID: author, IsProcessing: true, PublishAt: &future}, nil, true},
		{"live, anonymous", &postgres.Post{AuthorID: author}, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hiddenFromViewer(tc.post, tc.viewer); got != tc.hidden {
				t.Fatalf("hidden=%v want %v", got, tc.hidden)
			}
		})
	}
}

// The page filter drops scheduled rows for anyone but the author, and the
// order of what remains is preserved.
func TestPageFilterDropsScheduledForStrangers(t *testing.T) {
	author, stranger := uuid.New(), uuid.New()
	future := time.Now().Add(time.Hour)
	a := &postgres.Post{ID: uuid.New(), AuthorID: author}
	b := &postgres.Post{ID: uuid.New(), AuthorID: author, PublishAt: &future}
	c := &postgres.Post{ID: uuid.New(), AuthorID: author}
	page := func() []PostDetail { return []PostDetail{{Post: a}, {Post: b}, {Post: c}} }

	var out []PostDetail
	for _, d := range page() {
		if hiddenFromViewer(d.Post, &stranger) {
			continue
		}
		out = append(out, d)
	}
	if len(out) != 2 || out[0].ID != a.ID || out[1].ID != c.ID {
		t.Fatalf("stranger page: got %d rows", len(out))
	}
	out = nil
	for _, d := range page() {
		if hiddenFromViewer(d.Post, &author) {
			continue
		}
		out = append(out, d)
	}
	if len(out) != 3 || out[1].ID != b.ID {
		t.Fatalf("author page: got %d rows", len(out))
	}
}
