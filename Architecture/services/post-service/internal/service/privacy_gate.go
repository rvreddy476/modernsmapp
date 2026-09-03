package service

import (
	"context"
	"log"
	"os"
	"sync"
	"time"

	"github.com/atpost/post-service/pkg/graphclient"
	"github.com/google/uuid"
)

// Private accounts and the comments audience — the account-level gate that
// sits UNDER every per-post visibility check.
//
// A post's own `visibility` column answers "who did the author address this
// post to". It says nothing about the author having since flipped their
// whole account to private, or restricted comments to friends. Those facts
// live in identity user-service settings and are resolved, together with the
// follow/connection graph, by graph-service's permission matrix behind
// POST /v1/internal/graph/can. This file is post-service's one client for
// that answer; every read and write path funnels through it so a surface
// cannot forget to ask.
//
// Failure shape: DENY. A graph outage, a non-200, a malformed body or a
// target the graph did not answer for all read as "may not". The cost is a
// 404/403 during an incident; the alternative is a private account's posts
// opening up exactly when nobody is watching.
//
// Dev rigs without graph-service: an empty GRAPH_SERVICE_URL keeps the
// long-standing permissive behaviour of checkViewerFollowsAuthor (which the
// existing unit tests rely on). Production always has the URL configured —
// cmd/server defaults it — so the permissive branch cannot be reached there.

// privacyGateCacheTTL bounds how long a (viewer, action, author) answer is
// reused. Three seconds absorbs a scroll's worth of batch hydrations without
// letting a "go private" flip linger past the next screen.
const privacyGateCacheTTL = 3 * time.Second

// anonymousViewerID is what graph-service is asked about when there is no
// viewer. The nil UUID follows nobody and is connected to nobody, so the
// matrix resolves it exactly like a stranger: public authors allow, private
// authors deny.
var anonymousViewerID = uuid.Nil

// privacyGateState is embedded in Service. Declared here so the gate's state
// lives next to the code that owns it.
type privacyGateState struct {
	privacyMu    sync.Mutex
	privacyCache map[string]privacyGateEntry
	// privacyNow is swapped by tests to drive the TTL.
	privacyNow func() time.Time
	// hiddenAuthors answers the auth-service account-lifecycle question
	// (deactivated / pending-delete). A narrow interface — rather than the
	// concrete *postgres.Store already on Service.pgStore — so tests can
	// swap in a fake without a live database, the same way canViewPosts is
	// tested against an httptest fake for graph-service. New() sets this to
	// pgStore, which satisfies it via AnyHidden
	// (internal/store/postgres/purge.go).
	hiddenAuthors hiddenAuthorsStore
}

// hiddenAuthorsStore is satisfied by *postgres.Store.
type hiddenAuthorsStore interface {
	AnyHidden(ctx context.Context, authorIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

type privacyGateEntry struct {
	allowed bool
	expires time.Time
}

func privacyGateKey(viewer uuid.UUID, action string, target uuid.UUID) string {
	return viewer.String() + "|" + action + "|" + target.String()
}

func (s *Service) privacyGraphClient() *graphclient.Client {
	key := s.internalServiceKey
	if key == "" {
		key = os.Getenv("INTERNAL_SERVICE_KEY")
	}
	return graphclient.New(s.graphServiceURL, key, s.httpClient)
}

func (s *Service) privacyClock() time.Time {
	if s.privacyNow != nil {
		return s.privacyNow()
	}
	return time.Now()
}

// graphCan resolves action for every target, batching at graph-service's
// per-call ceiling and serving fresh answers from the short cache. The result
// has an entry for EVERY requested target; unresolved ones are false.
func (s *Service) graphCan(ctx context.Context, viewerID uuid.UUID, action string, targetIDs []uuid.UUID) map[uuid.UUID]bool {
	out := make(map[uuid.UUID]bool, len(targetIDs))
	if len(targetIDs) == 0 {
		return out
	}
	if s.graphServiceURL == "" {
		// No policy configured — the same "no graph, no gate" rule the
		// follow check has always applied on dev rigs.
		for _, id := range targetIDs {
			out[id] = true
		}
		return out
	}

	now := s.privacyClock()
	var pending []uuid.UUID
	seen := make(map[uuid.UUID]bool, len(targetIDs))
	s.privacyMu.Lock()
	for _, id := range targetIDs {
		if seen[id] {
			continue
		}
		seen[id] = true
		if id == viewerID {
			out[id] = true // self-view is always allowed; no round trip
			continue
		}
		if e, ok := s.privacyCache[privacyGateKey(viewerID, action, id)]; ok && now.Before(e.expires) {
			out[id] = e.allowed
			continue
		}
		pending = append(pending, id)
	}
	s.privacyMu.Unlock()

	if len(pending) == 0 {
		return out
	}

	client := s.privacyGraphClient()
	for start := 0; start < len(pending); start += graphclient.MaxCanBatch {
		end := start + graphclient.MaxCanBatch
		if end > len(pending) {
			end = len(pending)
		}
		chunk := pending[start:end]
		ids := make([]string, len(chunk))
		for i, id := range chunk {
			ids[i] = id.String()
		}
		answers, err := client.Can(ctx, viewerID.String(), action, ids)
		if err != nil {
			// Deny the whole chunk. Errors are NOT cached: the next request
			// retries rather than serving three seconds of denial after a
			// blip has cleared.
			log.Printf("Warning: privacy gate %s unresolved for %d authors; denying: %v", action, len(chunk), err)
			for _, id := range chunk {
				out[id] = false
			}
			continue
		}
		s.privacyMu.Lock()
		if s.privacyCache == nil {
			s.privacyCache = make(map[string]privacyGateEntry)
		}
		for _, id := range chunk {
			allowed := answers[id.String()] // absent ⇒ false ⇒ deny
			out[id] = allowed
			s.privacyCache[privacyGateKey(viewerID, action, id)] = privacyGateEntry{allowed: allowed, expires: now.Add(privacyGateCacheTTL)}
		}
		// Opportunistic sweep so the map cannot grow without bound in a
		// long-lived process; entries are tiny and the TTL is seconds.
		if len(s.privacyCache) > 50000 {
			for k, e := range s.privacyCache {
				if now.After(e.expires) {
					delete(s.privacyCache, k)
				}
			}
		}
		s.privacyMu.Unlock()
	}
	return out
}

// canViewPosts reports, per author, whether viewerID may read that author's
// post surfaces (account_visibility). A nil viewer is the anonymous stranger.
//
// This is also where the auth-service account-lifecycle gate lives
// (Architecture/shared/events/events.go "Account control"): a deactivated
// author, or one inside the 30-day deletion recovery window, has a row in
// post_hidden_authors (internal/store/postgres/purge.go SetUserHidden) and is
// denied here regardless of what the follow/privacy graph answer says.
// Every existing caller of canViewPosts/canViewAuthor — GetPostsByIDs,
// by-author listing, GetRecentPosts, comments, etc. — picks this up for
// free because they all funnel through this one function.
func (s *Service) canViewPosts(ctx context.Context, viewerID *uuid.UUID, authorIDs []uuid.UUID) map[uuid.UUID]bool {
	viewer := anonymousViewerID
	if viewerID != nil {
		viewer = *viewerID
	}
	out := s.graphCan(ctx, viewer, graphclient.ActionViewPosts, authorIDs)
	return s.denyHiddenAuthors(ctx, viewer, out)
}

// denyHiddenAuthors forces false onto every target in `answers` whose author
// is currently hidden, leaving self-view (viewer == author) untouched. Same
// fail-closed shape as graphCan: a lookup error denies the checked authors
// rather than silently falling back to the graph's answer.
func (s *Service) denyHiddenAuthors(ctx context.Context, viewer uuid.UUID, answers map[uuid.UUID]bool) map[uuid.UUID]bool {
	if s.hiddenAuthors == nil || len(answers) == 0 {
		return answers
	}
	toCheck := make([]uuid.UUID, 0, len(answers))
	for id := range answers {
		if id == viewer {
			continue // self-view is never gated by hidden
		}
		toCheck = append(toCheck, id)
	}
	if len(toCheck) == 0 {
		return answers
	}
	hidden, err := s.hiddenAuthors.AnyHidden(ctx, toCheck)
	if err != nil {
		log.Printf("Warning: hidden-authors lookup unresolved for %d authors; denying: %v", len(toCheck), err)
		for _, id := range toCheck {
			answers[id] = false
		}
		return answers
	}
	for id := range hidden {
		answers[id] = false
	}
	return answers
}

// canViewAuthor is the single-author form of canViewPosts.
func (s *Service) canViewAuthor(ctx context.Context, viewerID *uuid.UUID, authorID uuid.UUID) bool {
	return s.canViewPosts(ctx, viewerID, []uuid.UUID{authorID})[authorID]
}

// canComment reports whether viewerID may comment on authorID's content
// (allow_comments_from: everyone | friends). The author always may.
func (s *Service) canComment(ctx context.Context, viewerID, authorID uuid.UUID) bool {
	return s.graphCan(ctx, viewerID, graphclient.ActionComment, []uuid.UUID{authorID})[authorID]
}
