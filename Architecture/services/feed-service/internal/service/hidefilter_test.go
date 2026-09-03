package service

import (
	"testing"

	"github.com/google/uuid"
)

// Account-control hide gate (hidefilter.go). Same pure-filter test shape as
// blocksafety_test.go's TestApplyBlockFilter_* — filterHiddenAuthors is the
// step applyHiddenAuthorFilter delegates to after resolving the hidden set
// from Postgres.

func TestFilterHiddenAuthors_ExcludesHiddenAuthorEvenWhenNotBlocked(t *testing.T) {
	hiddenAuthor := uuid.New()
	okAuthor := uuid.New()

	items := []FeedItem{
		feedItemBy(okAuthor),
		feedItemBy(hiddenAuthor),
		feedItemBy(okAuthor),
	}

	hidden := map[uuid.UUID]struct{}{hiddenAuthor: {}}
	out := filterHiddenAuthors(items, hidden)
	if len(out) != 2 {
		t.Fatalf("expected 2 surviving items, got %d", len(out))
	}
	for _, it := range out {
		if it.AuthorID == hiddenAuthor {
			t.Fatal("a hidden author's post survived the filter")
		}
	}
}

func TestFilterHiddenAuthors_UnhideRestoresPosts(t *testing.T) {
	author := uuid.New()
	items := []FeedItem{feedItemBy(author), feedItemBy(author)}

	// hidden
	if out := filterHiddenAuthors(items, map[uuid.UUID]struct{}{author: {}}); len(out) != 0 {
		t.Fatalf("hidden author's posts must be excluded, got %d survivors", len(out))
	}
	// unhidden: an empty (or absent) hidden set is a pass-through, exactly
	// like an unhide (DELETE FROM hidden_authors) leaving no row behind.
	if out := filterHiddenAuthors(items, nil); len(out) != 2 {
		t.Fatalf("unhide must restore both posts, got %d", len(out))
	}
	if out := filterHiddenAuthors(items, map[uuid.UUID]struct{}{}); len(out) != 2 {
		t.Fatalf("empty hidden set must restore both posts, got %d", len(out))
	}
}

func TestFilterHiddenAuthors_EmptySetIsPassThrough(t *testing.T) {
	items := []FeedItem{feedItemBy(uuid.New()), feedItemBy(uuid.New())}
	if got := filterHiddenAuthors(items, nil); len(got) != 2 {
		t.Fatalf("no hidden authors means no filtering; got %d of 2", len(got))
	}
}

func TestFilterHiddenAuthors_KeepsNonHiddenAuthorsOnly(t *testing.T) {
	hiddenA, hiddenB, ok := uuid.New(), uuid.New(), uuid.New()
	items := []FeedItem{feedItemBy(hiddenA), feedItemBy(ok), feedItemBy(hiddenB)}
	hidden := map[uuid.UUID]struct{}{hiddenA: {}, hiddenB: {}}

	out := filterHiddenAuthors(items, hidden)
	if len(out) != 1 || out[0].AuthorID != ok {
		t.Fatalf("expected only the non-hidden author's post to survive, got %+v", out)
	}
}

func TestUniqueAuthorIDs_Dedupes(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	items := []FeedItem{feedItemBy(a), feedItemBy(b), feedItemBy(a)}
	ids := uniqueAuthorIDs(items)
	if len(ids) != 2 {
		t.Fatalf("expected 2 unique authors, got %d: %v", len(ids), ids)
	}
}
