package service

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// 2026-10-30 audit B1: GET /v1/posts/:postId/thread returned every entry of
// a public or unlisted thread before the private-account, block,
// deactivated-author and age checks the direct read applies. A thread must
// never show what GET /v1/posts/:id would hide.

// fakeListReads is the list read store (list_reads.go) in memory.
type fakeListReads struct {
	posts     map[uuid.UUID]*postgres.Post
	threads   map[uuid.UUID][]postgres.Post
	trending  []postgres.Post
	bookmarks []postgres.Post
	access    map[uuid.UUID]*postgres.PostAccessState
	getPosts  int
}

func newFakeListReads() *fakeListReads {
	return &fakeListReads{
		posts: map[uuid.UUID]*postgres.Post{}, threads: map[uuid.UUID][]postgres.Post{},
		access: map[uuid.UUID]*postgres.PostAccessState{},
	}
}

func (f *fakeListReads) GetPost(_ context.Context, id uuid.UUID) (*postgres.Post, error) {
	f.getPosts++
	p := f.posts[id]
	if p == nil {
		return nil, nil
	}
	cp := *p
	return &cp, nil
}

func (f *fakeListReads) GetPostAccessState(_ context.Context, id uuid.UUID) (*postgres.PostAccessState, error) {
	return f.access[id], nil
}

func (f *fakeListReads) GetThreadPosts(_ context.Context, rootID uuid.UUID) ([]postgres.Post, error) {
	return append([]postgres.Post(nil), f.threads[rootID]...), nil
}

func (f *fakeListReads) GetTrendingPosts(context.Context, []string, int, string) ([]postgres.Post, string, error) {
	return append([]postgres.Post(nil), f.trending...), "next", nil
}

func (f *fakeListReads) GetBookmarks(context.Context, uuid.UUID, []string, int, string, postgres.SavedScope) ([]postgres.Post, string, error) {
	return append([]postgres.Post(nil), f.bookmarks...), "", nil
}

// accountGraph answers the account gate (POST /v1/internal/graph/can,
// view_posts) from deny["viewer|author"] (the anonymous viewer is the nil
// UUID, as privacy_gate.go asks), and both relationship routes from
// rels["viewer|author"]. Anything not listed is a public stranger.
type accountGraph struct {
	mu   sync.Mutex
	deny map[string]bool
	rels map[string]map[string]bool
}

func newAccountGraph(t *testing.T) (*accountGraph, *httptest.Server) {
	t.Helper()
	g := &accountGraph{deny: map[string]bool{}, rels: map[string]map[string]bool{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		defer g.mu.Unlock()
		switch r.URL.Path {
		case "/v1/internal/graph/can":
			var req canRequest
			_ = json.NewDecoder(r.Body).Decode(&req)
			data := map[string]bool{}
			for _, id := range req.TargetIDs {
				data[id] = !g.deny[req.ViewerID+"|"+id]
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
		case "/v1/graph/relationship":
			key := r.URL.Query().Get("user_id") + "|" + r.URL.Query().Get("other_id")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": g.rels[key]})
		case "/v1/graph/relationships/batch":
			var req struct {
				ViewerID  string   `json:"viewer_id"`
				TargetIDs []string `json:"target_ids"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			out := map[string]any{}
			for _, id := range req.TargetIDs {
				rel := g.rels[req.ViewerID+"|"+id]
				if rel == nil {
					rel = map[string]bool{}
				}
				out[id] = rel
			}
			_ = json.NewEncoder(w).Encode(out)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return g, srv
}

func (g *accountGraph) denyView(viewer *uuid.UUID, author uuid.UUID) {
	v := uuid.Nil
	if viewer != nil {
		v = *viewer
	}
	g.deny[v.String()+"|"+author.String()] = true
}

func (g *accountGraph) relate(viewer, author uuid.UUID, rel map[string]bool) {
	g.rels[viewer.String()+"|"+author.String()] = rel
}

type fakeHidden map[uuid.UUID]bool

func (f fakeHidden) AnyHidden(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if f[id] {
			out[id] = true
		}
	}
	return out, nil
}

// threadFixture is a three-entry public thread by author.
type threadFixture struct {
	author  uuid.UUID
	root    uuid.UUID
	entries []postgres.Post
	store   *fakeListReads
	graph   *accountGraph
	svc     *Service
}

func newThreadFixture(t *testing.T) *threadFixture {
	t.Helper()
	g, srv := newAccountGraph(t)
	f := &threadFixture{author: uuid.New(), store: newFakeListReads(), graph: g}
	f.root = uuid.New()
	root := f.root
	for i := 0; i < 3; i++ {
		id := root
		if i > 0 {
			id = uuid.New()
		}
		f.entries = append(f.entries, postgres.Post{
			ID: id, AuthorID: f.author, Visibility: "public", ReviewStatus: "approved",
			ContentType: "post", ThreadRootID: &root, ThreadSeq: i, Text: "entry",
		})
	}
	f.sync()
	f.svc = &Service{graphServiceURL: srv.URL, httpClient: srv.Client(), listReadSeam: f.store}
	return f
}

// sync writes the entries back to the store (after a test edits them).
func (f *threadFixture) sync() {
	f.store.threads[f.root] = append([]postgres.Post(nil), f.entries...)
	for i := range f.entries {
		p := f.entries[i]
		f.store.posts[p.ID] = &p
	}
}

func (f *threadFixture) read(t *testing.T, postID uuid.UUID, viewer *uuid.UUID) ([]postgres.Post, error) {
	t.Helper()
	// A fresh privacy cache per read: the answers under test change between
	// reads, and the 3 s cache is not what is being tested.
	f.svc.privacyCache = nil
	return f.svc.GetThread(context.Background(), postID, viewer)
}

func TestGetThread_RegressionAuditB1(t *testing.T) {
	t.Run("blocked viewer", func(t *testing.T) {
		f := newThreadFixture(t)
		viewer := uuid.New()
		// graph-service denies view_posts across a block in either direction.
		f.graph.denyView(&viewer, f.author)
		f.graph.relate(viewer, f.author, map[string]bool{"blocked_by": true})
		for _, e := range f.entries {
			if got, err := f.read(t, e.ID, &viewer); !errors.Is(err, ErrPostNotFound) {
				t.Fatalf("a blocked viewer read the thread via entry %d: %d entries, err %v", e.ThreadSeq, len(got), err)
			}
		}
		if f.svc.viewerMayViewPost(context.Background(), &f.entries[0], &viewer) {
			t.Fatal("precondition: the direct read must deny the blocked viewer")
		}
	})

	t.Run("anonymous viewer, private account", func(t *testing.T) {
		f := newThreadFixture(t)
		f.graph.denyView(nil, f.author)
		if got, err := f.read(t, f.entries[1].ID, nil); !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("a signed-out stranger read a private account's thread: %d entries, err %v", len(got), err)
		}
	})

	t.Run("private author, signed-in stranger and follower", func(t *testing.T) {
		f := newThreadFixture(t)
		stranger, follower := uuid.New(), uuid.New()
		f.graph.denyView(&stranger, f.author)
		f.graph.relate(follower, f.author, map[string]bool{"follows": true})
		if got, err := f.read(t, f.root, &stranger); !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("a non-follower read a private account's thread: %d entries, err %v", len(got), err)
		}
		got, err := f.read(t, f.root, &follower)
		if err != nil || len(got) != 3 {
			t.Fatalf("an approved follower lost the thread: %d entries, err %v", len(got), err)
		}
		got, err = f.read(t, f.root, &f.author)
		if err != nil || len(got) != 3 {
			t.Fatalf("the author lost their own thread: %d entries, err %v", len(got), err)
		}
	})

	t.Run("deactivated author", func(t *testing.T) {
		f := newThreadFixture(t)
		f.svc.hiddenAuthors = fakeHidden{f.author: true}
		viewer := uuid.New()
		if _, err := f.read(t, f.root, &viewer); !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("a deactivated author's thread opened: %v", err)
		}
		if _, err := f.read(t, f.root, nil); !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("a deactivated author's thread opened to a stranger: %v", err)
		}
	})
}

func TestGetThread_PublicThreadOpensToStrangers(t *testing.T) {
	f := newThreadFixture(t)
	viewer := uuid.New()
	for _, v := range []*uuid.UUID{nil, &viewer} {
		got, err := f.read(t, f.entries[2].ID, v)
		if err != nil || len(got) != 3 {
			t.Fatalf("a public thread lost entries: %d, err %v", len(got), err)
		}
		for i, e := range got {
			if e.ThreadSeq != i {
				t.Fatalf("thread order changed: %v", got)
			}
		}
	}
}

func TestGetThread_PerEntryGates(t *testing.T) {
	t.Run("age-restricted entry", func(t *testing.T) {
		f := newThreadFixture(t)
		f.entries[1].AgeRestricted = true
		f.sync()
		// Anonymous: no known age, so the 18+ entry is left out.
		got, err := f.read(t, f.root, nil)
		if err != nil || len(got) != 2 || got[1].ID != f.entries[2].ID {
			t.Fatalf("the 18+ entry reached a viewer of unknown age: %d entries, err %v", len(got), err)
		}
		// Asking for the 18+ entry itself is the direct read's 404.
		if _, err := f.read(t, f.entries[1].ID, nil); !errors.Is(err, ErrPostNotFound) {
			t.Fatalf("the 18+ entry opened by deep link: %v", err)
		}
		// The author always reads their own.
		if got, err := f.read(t, f.root, &f.author); err != nil || len(got) != 3 {
			t.Fatalf("the author lost their own 18+ entry: %d, %v", len(got), err)
		}
	})

	t.Run("scheduled entry", func(t *testing.T) {
		f := newThreadFixture(t)
		at := time.Now().Add(time.Hour)
		f.entries[2].PublishAt = &at
		f.sync()
		viewer := uuid.New()
		if got, err := f.read(t, f.root, &viewer); err != nil || len(got) != 2 {
			t.Fatalf("a scheduled entry reached a stranger: %d entries, err %v", len(got), err)
		}
		if got, err := f.read(t, f.root, &f.author); err != nil || len(got) != 3 {
			t.Fatalf("the author lost their scheduled entry: %d, %v", len(got), err)
		}
	})

	t.Run("entry narrowed to followers", func(t *testing.T) {
		f := newThreadFixture(t)
		f.entries[1].Visibility = "followers"
		f.sync()
		stranger, follower := uuid.New(), uuid.New()
		f.graph.relate(follower, f.author, map[string]bool{"follows": true})
		if got, err := f.read(t, f.root, &stranger); err != nil || len(got) != 2 {
			t.Fatalf("a followers-only entry reached a stranger: %d entries, err %v", len(got), err)
		}
		if got, err := f.read(t, f.root, &follower); err != nil || len(got) != 3 {
			t.Fatalf("a follower lost the followers-only entry: %d, %v", len(got), err)
		}
	})

	t.Run("held entry", func(t *testing.T) {
		f := newThreadFixture(t)
		f.entries[1].ActiveRestrictionCount = 1
		f.sync()
		viewer := uuid.New()
		if got, err := f.read(t, f.root, &viewer); err != nil || len(got) != 2 {
			t.Fatalf("a held entry reached a viewer: %d entries, err %v", len(got), err)
		}
	})
}

// The thread gate and the direct read agree for every viewer the audit
// named (the scratch-copy probe, kept as a regression).
func TestCanViewThread_AgreesWithTheDirectRead(t *testing.T) {
	g, srv := newAccountGraph(t)
	author, viewer := uuid.New(), uuid.New()
	g.denyView(&viewer, author)
	g.denyView(nil, author)
	g.relate(viewer, author, map[string]bool{"blocked": true, "blocked_by": true})
	svc := &Service{graphServiceURL: srv.URL, httpClient: srv.Client()}
	for _, vis := range []string{"public", "unlisted"} {
		root := &postgres.Post{ID: uuid.New(), AuthorID: author, Visibility: vis, ReviewStatus: "approved"}
		if svc.viewerMayViewPost(context.Background(), root, &viewer) {
			t.Fatal("precondition: the direct read should deny")
		}
		if svc.canViewThread(context.Background(), root, &viewer) {
			t.Errorf("%s: the thread gate admits a signed-in viewer the direct read refuses", vis)
		}
		if svc.canViewThread(context.Background(), root, nil) {
			t.Errorf("%s: the thread gate admits an anonymous viewer the direct read refuses", vis)
		}
	}
}
