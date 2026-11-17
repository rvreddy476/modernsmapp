package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// reels-18: every engagement write applies the direct read's whole decision.
// A stranger who could not open the post (pending, flagged, rejected, under
// a restriction, still processing, still scheduled) gets the refusal a
// missing post gets and nothing is written; the author keeps their access.

type liveGateRig struct {
	svc      *Service
	store    *fakeListReads
	author   uuid.UUID
	stranger uuid.UUID
	states   fakeMediaStates
}

func newLiveGateRig() *liveGateRig {
	r := &liveGateRig{store: newFakeListReads(), author: uuid.New(), stranger: uuid.New(), states: fakeMediaStates{}}
	// No graph-service: the audience rule passes everyone (dev-rig rule), so
	// only the live-state gate can refuse here.
	r.svc = &Service{listReadSeam: r.store, mediaStates: r.states}
	return r
}

// post stores one public reel by the author with one media item, ready and
// passed unless mutate changes it.
func (r *liveGateRig) post(mutate func(p *postgres.Post, m *postgres.MediaOwnership)) uuid.UUID {
	id, mediaID := uuid.New(), uuid.New()
	p := &postgres.Post{ID: id, AuthorID: r.author, ContentType: "flick", Visibility: "public", ReviewStatus: "approved",
		Media: []postgres.PostMedia{{MediaID: mediaID}}}
	m := postgres.MediaOwnership{ProcessingStatus: mediaReady, ModerationStatus: mediaPassed}
	if mutate != nil {
		mutate(p, &m)
	}
	r.store.posts[id] = p
	r.states[mediaID] = m
	return id
}

func liveGateCases() map[string]func(p *postgres.Post, m *postgres.MediaOwnership) {
	at := time.Now().Add(2 * time.Hour)
	return map[string]func(p *postgres.Post, m *postgres.MediaOwnership){
		"pending":    func(p *postgres.Post, _ *postgres.MediaOwnership) { p.ReviewStatus = "pending" },
		"flagged":    func(p *postgres.Post, _ *postgres.MediaOwnership) { p.ReviewStatus = "flagged" },
		"rejected":   func(p *postgres.Post, _ *postgres.MediaOwnership) { p.ReviewStatus = "rejected" },
		"restricted": func(p *postgres.Post, _ *postgres.MediaOwnership) { p.ActiveRestrictionCount = 1 },
		"processing": func(_ *postgres.Post, m *postgres.MediaOwnership) { m.ProcessingStatus = "processing" },
		"scheduled":  func(p *postgres.Post, _ *postgres.MediaOwnership) { p.PublishAt = &at; p.IsScheduled = true },
	}
}

func TestEngagementLoaderRefusesAStrangerOnAPostThatIsNotLive(t *testing.T) {
	ctx := context.Background()
	for name, mutate := range liveGateCases() {
		t.Run(name, func(t *testing.T) {
			r := newLiveGateRig()
			id := r.post(mutate)
			if _, err := r.svc.loadPostForEngagement(ctx, id, r.stranger); !errors.Is(err, ErrPostNotVisible) {
				t.Fatalf("stranger: err = %v; want the not-visible refusal (404)", err)
			}
			if ok, err := r.svc.PostVisibleTo(ctx, id, &r.stranger); err != nil || ok {
				t.Fatalf("stranger read gate: open=%v err=%v; want closed", ok, err)
			}
			if p, err := r.svc.loadPostForEngagement(ctx, id, r.author); err != nil || p == nil {
				t.Fatalf("author: %v, %v; want their own post", p, err)
			}
		})
	}
	// A live public post stays open to a stranger, and a missing one is the
	// not-found the refusal mirrors.
	r := newLiveGateRig()
	live := r.post(nil)
	if p, err := r.svc.loadPostForEngagement(ctx, live, r.stranger); err != nil || p == nil {
		t.Fatalf("a live post: %v, %v", p, err)
	}
	if _, err := r.svc.loadPostForEngagement(ctx, uuid.New(), r.stranger); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("a missing post: %v", err)
	}
}

// An unreadable media table fails closed: no engagement on a post whose
// processing state is unknown.
func TestEngagementLoaderFailsClosedWithoutMediaState(t *testing.T) {
	r := newLiveGateRig()
	id := r.post(nil)
	r.svc.mediaStates = failingMediaStates{}
	if _, err := r.svc.loadPostForEngagement(context.Background(), id, r.stranger); err == nil {
		t.Fatal("an unreadable media state let the engagement through")
	}
}

// Two write routes driven end to end through the real loader: the Queue
// (watch later) and a user playlist. A stranger's add of a pending reel
// writes nothing and answers as a missing post; the author's succeeds.
func TestEngagementWritesOnAPendingPostWriteNothingForAStranger(t *testing.T) {
	ctx := context.Background()
	r := newLiveGateRig()
	pending := r.post(func(p *postgres.Post, _ *postgres.MediaOwnership) { p.ReviewStatus = "pending" })

	queue := newFakeSystemPlaylistStore()
	r.svc.systemPlaylists = queue
	if _, err := r.svc.AddToWatchLater(ctx, r.stranger, pending); !errors.Is(err, ErrPostNotVisible) {
		t.Fatalf("stranger watch-later: %v", err)
	}
	if len(queue.adds) != 0 {
		t.Fatal("a stranger queued a pending post")
	}
	if _, err := r.svc.AddToWatchLater(ctx, r.author, pending); err != nil || len(queue.adds) != 1 {
		t.Fatalf("author watch-later: %v (adds %d)", err, len(queue.adds))
	}

	pl := &postgres.Playlist{ID: uuid.New(), CreatorID: r.stranger, Visibility: "public", Kind: postgres.PlaylistKindUser}
	items := &fakePlaylistItems{}
	r.svc.authoringOwners = &fakeAuthoringStore{playlist: pl}
	r.svc.playlistItemsSeam = items
	if _, err := r.svc.AddPlaylistItem(ctx, r.stranger, pl.ID, pending, nil); !errors.Is(err, ErrPostNotFound) {
		t.Fatalf("stranger playlist add: %v; want ErrPostNotFound (404)", err)
	}
	if items.calls != 0 {
		t.Fatal("a stranger's playlist add of a pending post reached the store")
	}
	live := r.post(nil)
	if _, err := r.svc.AddPlaylistItem(ctx, r.stranger, pl.ID, live, nil); err != nil || items.calls != 1 {
		t.Fatalf("a live post: %v (calls %d)", err, items.calls)
	}
}
