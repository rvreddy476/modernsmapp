package service

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Hashtag SSE gate (/v1/hashtags/:tag/stream).
//
// The stream announces a new tagged post to everyone watching the tag. It
// must announce only what the hashtag page itself would list for that
// viewer, or the "N new posts" pill (and its snippet) leaks a post the
// viewer may not see. The rules are GetPostsByHashtag's:
//
//   - the row (postgres hashtagPostsFilter): visibility 'public', not
//     deleted, not scheduled, EFFECTIVELY approved (a pending, flagged,
//     rejected or restricted post is not announced);
//   - the author (canViewPosts): private accounts the viewer does not
//     follow, blocks, deactivated authors — fail-closed, so a graph outage
//     drops the event rather than pushes it. The viewer's own post skips
//     the author gate, as on the list.
//
// Cost per event: the row state is the same for every subscriber, so one
// read per post per instance serves them all (hashtagStreamPostTTL). The
// author answer is per (viewer, author) and already cached for
// privacyGateCacheTTL by graphCanResolved, so a burst of posts from one
// author costs one graph call per watching viewer, not one per event.

// hashtagStreamPostTTL bounds how long one post's row state is reused across
// the subscribers of an instance. The event fires right after the create
// commits, so every subscriber's check lands inside this window.
const hashtagStreamPostTTL = 2 * time.Second

// hashtagStreamGateState is embedded in Service.
type hashtagStreamGateState struct {
	hashtagStreamMu    sync.Mutex
	hashtagStreamPosts map[uuid.UUID]hashtagStreamPostEntry
	// hashtagStreamPostState replaces the Postgres read in tests. Nil means
	// pgStore.GetPostAccessState.
	hashtagStreamPostState func(ctx context.Context, postID uuid.UUID) (*postgres.PostAccessState, error)
}

type hashtagStreamPostEntry struct {
	listable bool
	expires  time.Time
}

// HashtagStreamAllows reports whether the hashtag stream may announce the
// new post postID by authorID to viewerID (nil = signed-out stranger).
// Anything undecidable — a failed row read, a graph-service outage — is a
// no.
func (s *Service) HashtagStreamAllows(ctx context.Context, viewerID *uuid.UUID, postID, authorID uuid.UUID) bool {
	if postID == uuid.Nil || authorID == uuid.Nil {
		return false
	}
	if !s.hashtagStreamPostListable(ctx, postID) {
		return false
	}
	if viewerID != nil && *viewerID == authorID {
		return true // self-view is never author-gated (hashtagPostsVisibleTo)
	}
	return s.canViewPosts(ctx, viewerID, []uuid.UUID{authorID})[authorID]
}

// hashtagStreamListable is the row half of hashtagPostsFilter, judged on the
// canonical access state.
func hashtagStreamListable(st *postgres.PostAccessState) bool {
	return st != nil &&
		!st.Deleted &&
		st.PublishAt == nil &&
		st.Visibility == "public" &&
		st.EffectiveReviewStatus() == "approved"
}

func (s *Service) hashtagStreamPostListable(ctx context.Context, postID uuid.UUID) bool {
	now := s.privacyClock()
	s.hashtagStreamMu.Lock()
	if e, ok := s.hashtagStreamPosts[postID]; ok && now.Before(e.expires) {
		s.hashtagStreamMu.Unlock()
		return e.listable
	}
	s.hashtagStreamMu.Unlock()

	read := s.hashtagStreamPostState
	if read == nil {
		if s.pgStore == nil {
			return false
		}
		read = s.pgStore.GetPostAccessState
	}
	st, err := read(ctx, postID)
	if err != nil {
		// Not cached: the next subscriber retries rather than inheriting a
		// blip. Dropping the event is safe — clients catch up on refetch.
		log.Printf("Warning: hashtag stream: post %s state unresolved; dropping: %v", postID, err)
		return false
	}
	listable := hashtagStreamListable(st)

	s.hashtagStreamMu.Lock()
	if s.hashtagStreamPosts == nil {
		s.hashtagStreamPosts = make(map[uuid.UUID]hashtagStreamPostEntry)
	}
	s.hashtagStreamPosts[postID] = hashtagStreamPostEntry{listable: listable, expires: now.Add(hashtagStreamPostTTL)}
	if len(s.hashtagStreamPosts) > 10000 {
		for id, e := range s.hashtagStreamPosts {
			if now.After(e.expires) {
				delete(s.hashtagStreamPosts, id)
			}
		}
	}
	s.hashtagStreamMu.Unlock()
	return listable
}
