package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/google/uuid"
)

// "Don't recommend this account" (feed_author_feedback) used to be applied
// only at the hydration tail, AFTER the keyset window and the discovery
// fills were cut. A viewer who had muted a prolific author got pages that
// came back short or empty while the cursor advanced, and fill slots went
// to authors they had excluded. The muted set now travels with the block
// set that every surface applies before the window is cut
// (resolveBlockedSet), failing closed like the block lookup does.

// fakeReelTimeline is the fan-out timeline as a slice, newest first, cut
// exactly as the Scylla read is: rows after `before`, at most `limit`.
type fakeReelTimeline struct {
	rows []scylla.FeedItem
}

func (f *fakeReelTimeline) GetHomeTimelineByContentTypesBefore(_ context.Context, _ uuid.UUID, _ []string, before string, limit int) ([]scylla.FeedItem, error) {
	start := 0
	if before != "" {
		for i, r := range f.rows {
			if r.CursorToken == before {
				start = i + 1
			}
		}
	}
	rest := f.rows[start:]
	if len(rest) > limit {
		rest = rest[:limit]
	}
	return rest, nil
}

// reelRows builds a timeline of reels by the given authors, in order,
// newest first, with a distinct cursor token per row.
func reelRows(authors ...uuid.UUID) []scylla.FeedItem {
	now := time.Now()
	rows := make([]scylla.FeedItem, 0, len(authors))
	for i, a := range authors {
		rows = append(rows, scylla.FeedItem{
			PostID: uuid.New(), AuthorID: a, ContentType: "reel",
			CreatedAt: now.Add(-time.Duration(i) * time.Minute), CursorToken: fmt.Sprintf("tok-%d", i),
		})
	}
	return rows
}

// newMutedAuthorService is a Service whose graph-service blocks nobody,
// whose post-service serves `recent` from /v1/posts/recent, and whose
// timeline and feedback store are the fakes given.
func newMutedAuthorService(t *testing.T, tl *fakeReelTimeline, fb *fakeFeedbackStore, recent []map[string]any) *Service {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/internal/graph/blocked-and-muted":
			_ = json.NewEncoder(w).Encode(map[string]any{"user_ids": []string{}})
		case "/v1/posts/recent":
			_ = json.NewEncoder(w).Encode(map[string]any{"data": recent})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)

	s := New(nil, nil, nil)
	s.graphURL = srv.URL
	s.postServiceURL = srv.URL
	s.windows = tl
	s.feedback = fb
	return s
}

func postIDs(items []FeedItem) map[uuid.UUID]struct{} {
	out := make(map[uuid.UUID]struct{}, len(items))
	for _, it := range items {
		out[it.PostID] = struct{}{}
	}
	return out
}

func TestMutedAuthor_ReelsWindowIsCutWithoutMutedAuthors(t *testing.T) {
	ctx := context.Background()
	viewer, a, b := uuid.New(), uuid.New(), uuid.New()

	t.Run("the page is full and holds no muted author", func(t *testing.T) {
		tl := &fakeReelTimeline{rows: reelRows(a, b, a, b, a)} // A1 B1 A2 B2 A3
		fb := newFakeFeedbackStore()
		fb.mute(viewer, b)
		s := newMutedAuthorService(t, tl, fb, nil)

		page, next, err := s.GetFlickFeedPage(ctx, viewer, 3, "", false)
		if err != nil {
			t.Fatalf("GetFlickFeedPage: %v", err)
		}
		if len(page) != 3 {
			t.Fatalf("got %d rows, want 3: the window must be cut after the muted author is removed, not before", len(page))
		}
		for _, it := range page {
			if it.AuthorID == b {
				t.Fatalf("post %s by the muted author reached the page", it.PostID)
			}
		}
		// Every A row fit on the page, so there is nothing to page to.
		if next != "" {
			t.Fatalf("cursor %q on an exhausted timeline, want none", next)
		}
	})

	t.Run("the cursor is the last row returned, so no unmuted row is skipped", func(t *testing.T) {
		tl := &fakeReelTimeline{rows: reelRows(a, b, a, b, a, a)} // A1 B1 A2 B2 A3 A4
		fb := newFakeFeedbackStore()
		fb.mute(viewer, b)
		s := newMutedAuthorService(t, tl, fb, nil)

		first, next, err := s.GetFlickFeedPage(ctx, viewer, 3, "", false)
		if err != nil {
			t.Fatalf("first page: %v", err)
		}
		if len(first) != 3 {
			t.Fatalf("first page has %d rows, want 3", len(first))
		}
		if want := tl.rows[4].CursorToken; next != want { // A3 is the last row returned
			t.Fatalf("cursor %q, want the last returned row's token %q", next, want)
		}
		second, last, err := s.GetFlickFeedPage(ctx, viewer, 3, next, false)
		if err != nil {
			t.Fatalf("second page: %v", err)
		}
		if len(second) != 1 || second[0].PostID != tl.rows[5].PostID {
			t.Fatalf("second page = %+v, want exactly A4", second)
		}
		if last != "" {
			t.Fatalf("cursor %q after the last row, want none", last)
		}
		seen := postIDs(append(first, second...))
		for _, r := range tl.rows {
			_, got := seen[r.PostID]
			if r.AuthorID == b && got {
				t.Fatalf("muted author's post %s was returned", r.PostID)
			}
			if r.AuthorID != b && !got {
				t.Fatalf("unmuted post %s (%s) was skipped across the two pages", r.PostID, r.CursorToken)
			}
		}
	})
}

func TestMutedAuthor_ReelsDiscoveryFillNeverCarriesAMutedAuthor(t *testing.T) {
	ctx := context.Background()
	viewer, muted, stranger := uuid.New(), uuid.New(), uuid.New()
	recentReel := func(author uuid.UUID) map[string]any {
		return map[string]any{"id": uuid.New().String(), "author_id": author.String(), "created_at": time.Now(), "content_type": "reel"}
	}
	recent := []map[string]any{recentReel(muted), recentReel(stranger), recentReel(muted), recentReel(stranger)}

	fb := newFakeFeedbackStore()
	fb.mute(viewer, muted)
	s := newMutedAuthorService(t, &fakeReelTimeline{}, fb, recent) // follows nobody: first page is all fill

	page, _, err := s.GetFlickFeedPage(ctx, viewer, 3, "", false)
	if err != nil {
		t.Fatalf("GetFlickFeedPage: %v", err)
	}
	if len(page) != 2 {
		t.Fatalf("got %d rows, want the stranger's 2 reels and nothing by the muted author", len(page))
	}
	for _, it := range page {
		if it.AuthorID == muted {
			t.Fatalf("the discovery fill handed the page reel %s by a muted author", it.PostID)
		}
		if it.Source != sourceColdStart {
			t.Fatalf("fill row %s carries source %q, want %q", it.PostID, it.Source, sourceColdStart)
		}
	}
}

func TestMutedAuthor_ReadFailureFailsClosed(t *testing.T) {
	ctx := context.Background()
	viewer, a := uuid.New(), uuid.New()
	fb := newFakeFeedbackStore()
	fb.err = errors.New("feed_author_feedback unavailable")
	s := newMutedAuthorService(t, &fakeReelTimeline{rows: reelRows(a, a, a)}, fb, nil)

	if page, _, err := s.GetFlickFeedPage(ctx, viewer, 3, "", false); err == nil {
		t.Fatalf("reels page served %d rows with the muted-author state unresolved; want an error", len(page))
	} else if !errors.Is(err, fb.err) {
		t.Fatalf("reels page error %v does not wrap the store error", err)
	}
	if page, _, err := s.GetVideoFeedPage(ctx, viewer, 3, "", false, false); err == nil {
		t.Fatalf("Tube page served %d rows with the muted-author state unresolved; want an error", len(page))
	}
}
