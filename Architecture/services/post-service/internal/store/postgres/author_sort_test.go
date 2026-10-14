package postgres

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Profile grid sort (T-21): sort validation and the cursor contract. The
// queries themselves run against Postgres in author_sort_integration_test.go.

func TestParseAuthorPostSort(t *testing.T) {
	for raw, want := range map[string]AuthorPostSort{
		"": AuthorSortLatest, "latest": AuthorSortLatest, "LATEST": AuthorSortLatest, " popular ": AuthorSortPopular, "oldest": AuthorSortOldest,
	} {
		got, err := ParseAuthorPostSort(raw)
		if err != nil || got != want {
			t.Fatalf("ParseAuthorPostSort(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"newest", "top", "recent", "likes", "popular;drop"} {
		if _, err := ParseAuthorPostSort(raw); !errors.Is(err, ErrInvalidAuthorSort) {
			t.Fatalf("ParseAuthorPostSort(%q) err = %v, want ErrInvalidAuthorSort", raw, err)
		}
	}
	if !reflect.DeepEqual(AuthorPostSorts, []AuthorPostSort{AuthorSortLatest, AuthorSortPopular, AuthorSortOldest}) {
		t.Fatalf("AuthorPostSorts = %v", AuthorPostSorts)
	}
}

func TestAuthorCursorRoundTripsPerSort(t *testing.T) {
	id := uuid.New()
	// Microsecond precision is what Postgres stores; the cursor must not lose it.
	at := time.Date(2026, 10, 14, 9, 30, 15, 123456000, time.UTC)
	for _, sort := range AuthorPostSorts {
		raw := EncodeAuthorCursor(sort, true, at, 42, id)
		if strings.ContainsAny(raw, "+/=") {
			t.Fatalf("%s cursor %q is not URL-safe", sort, raw)
		}
		got, err := decodeAuthorCursor(sort, raw)
		if err != nil {
			t.Fatalf("%s: decode: %v", sort, err)
		}
		if got.Sort != sort || !got.Pinned || !got.CreatedAt.Equal(at) || got.Likes != 42 || got.ID != id || got.legacy {
			t.Fatalf("%s: round trip = %+v", sort, got)
		}
		// The same input always mints the same cursor (stable across retries).
		if again := EncodeAuthorCursor(sort, true, at, 42, id); again != raw {
			t.Fatalf("%s: cursor not deterministic: %q vs %q", sort, raw, again)
		}
	}
}

func TestAuthorCursorRejectsAnotherSortsCursor(t *testing.T) {
	raw := EncodeAuthorCursor(AuthorSortPopular, false, time.Now(), 7, uuid.New())
	for _, sort := range []AuthorPostSort{AuthorSortLatest, AuthorSortOldest} {
		if _, err := decodeAuthorCursor(sort, raw); !errors.Is(err, ErrInvalidAuthorCursor) {
			t.Fatalf("popular cursor used with %s: err = %v, want ErrInvalidAuthorCursor", sort, err)
		}
	}
}

func TestAuthorCursorLegacyTimestampOnlyForLatest(t *testing.T) {
	at := time.Date(2026, 10, 1, 8, 0, 0, 5000, time.UTC)
	legacy := at.Format(time.RFC3339Nano)
	got, err := decodeAuthorCursor(AuthorSortLatest, legacy)
	if err != nil || !got.legacy || !got.CreatedAt.Equal(at) {
		t.Fatalf("legacy latest cursor = %+v, %v", got, err)
	}
	for _, sort := range []AuthorPostSort{AuthorSortPopular, AuthorSortOldest} {
		if _, err := decodeAuthorCursor(sort, legacy); !errors.Is(err, ErrInvalidAuthorCursor) {
			t.Fatalf("legacy cursor with %s: err = %v, want ErrInvalidAuthorCursor", sort, err)
		}
	}
}

func TestAuthorCursorRejectsGarbage(t *testing.T) {
	for _, raw := range []string{"not-a-cursor", "e30", "%%%", EncodeAuthorCursor(AuthorSortLatest, false, time.Time{}, 0, uuid.New()),
		EncodeAuthorCursor(AuthorSortLatest, false, time.Now(), 0, uuid.Nil)} {
		if _, err := decodeAuthorCursor(AuthorSortLatest, raw); !errors.Is(err, ErrInvalidAuthorCursor) {
			t.Fatalf("decode(%q) err = %v, want ErrInvalidAuthorCursor", raw, err)
		}
	}
}

func TestAuthorSortSQL(t *testing.T) {
	id := uuid.New()
	at := time.Date(2026, 10, 14, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		sort    AuthorPostSort
		cur     *authorCursor
		order   string
		where   string
		argsLen int
	}{
		{AuthorSortLatest, nil, "COALESCE(is_pinned, false) DESC, created_at DESC, id DESC", "", 0},
		{AuthorSortLatest, &authorCursor{Pinned: true, CreatedAt: at, ID: id}, "", "(COALESCE(is_pinned, false), created_at, id) < ($4, $5, $6)", 3},
		{AuthorSortLatest, &authorCursor{CreatedAt: at, legacy: true}, "", "created_at < $4", 1},
		{AuthorSortOldest, nil, "created_at ASC, id ASC", "", 0},
		{AuthorSortOldest, &authorCursor{CreatedAt: at, ID: id}, "", "(created_at, id) > ($4, $5)", 2},
		{AuthorSortPopular, nil, authorLikesExpr + " DESC, id DESC", "", 0},
		{AuthorSortPopular, &authorCursor{Likes: 3, ID: id}, "", "(" + authorLikesExpr + ", id) < ($4, $5)", 2},
	} {
		order, where, args := authorSortSQL(tc.sort, tc.cur, 4)
		if tc.order != "" && order != tc.order {
			t.Fatalf("%s order = %q, want %q", tc.sort, order, tc.order)
		}
		if where != tc.where || len(args) != tc.argsLen {
			t.Fatalf("%s where = %q (%d args), want %q (%d)", tc.sort, where, len(args), tc.where, tc.argsLen)
		}
	}
	// Pinned-first is latest's alone.
	for _, sort := range []AuthorPostSort{AuthorSortPopular, AuthorSortOldest} {
		if order, _, _ := authorSortSQL(sort, nil, 3); strings.Contains(order, "is_pinned") {
			t.Fatalf("%s must not order by is_pinned: %q", sort, order)
		}
	}
}
