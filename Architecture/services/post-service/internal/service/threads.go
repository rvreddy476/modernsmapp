package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// Module 1 P0-8 — basic multi-post threads.
//
// Contract:
//   - Creation is atomic and ordered: all entries commit in one
//     transaction or none do (no partially visible thread).
//   - Idempotent: the client's idempotency key maps to exactly one root;
//     a retry returns the original thread.
//   - Feed collapse: only the root entry is main-feed eligible. Entries
//     1..n are stamped main_feed=false through the P0-1 distribution
//     machinery, so social home shows one collapsed item; the thread
//     endpoint expands it. PostTube/profile/deep links are unaffected.
//   - Visibility/auth: one visibility for the whole thread, enforced by
//     the same read filters as any post.

// ErrInvalidThread marks a malformed thread create request (400).
var ErrInvalidThread = errors.New("invalid thread")

const (
	minThreadEntries = 2
	maxThreadEntries = 25
	// threadEntryMaxLen mirrors the composer's per-post text ceiling.
	threadEntryMaxLen = 5000
)

// validPostVisibility mirrors the posts_visibility_check constraint
// (migration 019). Kept here so an invalid value is a 400 at the service
// boundary rather than a 500 from the database.
var validPostVisibility = map[string]bool{
	"public": true, "followers": true, "private": true,
	"unlisted": true, "trusted": true, "close_friends": true,
}

// ThreadEntryInput is one entry of a thread create request.
type ThreadEntryInput struct {
	Text     string
	MediaIDs []uuid.UUID
}

// CreateThreadInput is the full request.
type CreateThreadInput struct {
	AuthorID       uuid.UUID
	Visibility     string
	Entries        []ThreadEntryInput
	IdempotencyKey uuid.UUID
}

// CreateThread atomically creates an ordered thread. On idempotent
// replay it returns the previously created thread's posts.
func (s *Service) CreateThread(ctx context.Context, input *CreateThreadInput) ([]postgres.Post, error) {
	if len(input.Entries) < minThreadEntries || len(input.Entries) > maxThreadEntries {
		return nil, fmt.Errorf("%w: threads need %d-%d entries", ErrInvalidThread, minThreadEntries, maxThreadEntries)
	}
	for i, e := range input.Entries {
		if strings.TrimSpace(e.Text) == "" && len(e.MediaIDs) == 0 {
			return nil, fmt.Errorf("%w: entry %d is empty", ErrInvalidThread, i)
		}
		if len(e.Text) > threadEntryMaxLen {
			return nil, fmt.Errorf("%w: entry %d exceeds %d characters", ErrInvalidThread, i, threadEntryMaxLen)
		}
		if len(e.MediaIDs) > 10 {
			return nil, fmt.Errorf("%w: entry %d has more than 10 media items", ErrInvalidThread, i)
		}
	}
	// Codex P1-6: validate the visibility value instead of trusting it —
	// an unchecked value would hit the DB CHECK as a 500 (or, worse,
	// silently widen the audience if the constraint ever loosened).
	visibility := input.Visibility
	if visibility == "" {
		visibility = "public"
	}
	if !validPostVisibility[visibility] {
		return nil, fmt.Errorf("%w: unsupported visibility %q", ErrInvalidThread, visibility)
	}

	// Codex P1-6: enforce media ownership and readiness. Thread entries
	// used ResolveMediaKind only, so any user could attach another
	// user's media — or media still processing / rejected — to a thread.
	allMedia := make([]uuid.UUID, 0)
	for _, e := range input.Entries {
		allMedia = append(allMedia, e.MediaIDs...)
	}
	ownership, err := s.pgStore.BatchGetMediaOwnership(ctx, allMedia)
	if err != nil {
		return nil, fmt.Errorf("verify thread media: %w", err)
	}
	for _, mediaID := range allMedia {
		m, ok := ownership[mediaID]
		if !ok {
			return nil, fmt.Errorf("%w: media %s not found", ErrInvalidThread, mediaID)
		}
		if m.UploaderID != input.AuthorID {
			return nil, fmt.Errorf("%w: media %s belongs to another user", ErrInvalidThread, mediaID)
		}
		if m.ProcessingStatus == "rejected" || m.ModerationStatus == "rejected" {
			return nil, fmt.Errorf("%w: media %s was rejected", ErrInvalidThread, mediaID)
		}
		if m.ProcessingStatus != "ready" && m.ProcessingStatus != "" {
			return nil, fmt.Errorf("%w: media %s is not ready yet (%s)",
				ErrInvalidThread, mediaID, m.ProcessingStatus)
		}
	}

	// Spam gate across the combined text — same detector as CreatePost,
	// run once on the whole thread (a thread is one authored unit).
	combined := ""
	totalMedia := 0
	for _, e := range input.Entries {
		combined += e.Text + "\n"
		totalMedia += len(e.MediaIDs)
	}
	spamResult := s.spam.Check(ctx, input.AuthorID.String(), combined, totalMedia)
	if spamResult.Score > 0.95 {
		return nil, fmt.Errorf("content rejected: %s", spamResult.Reason)
	}
	reviewStatus := "approved"
	if spamResult.Score > 0.7 {
		reviewStatus = "flagged"
	}

	now := time.Now()
	rootID := uuid.New()
	posts := make([]*postgres.Post, 0, len(input.Entries))
	outbox := make([]postgres.ThreadOutboxEvent, 0, len(input.Entries))
	var prevID *uuid.UUID

	for i, e := range input.Entries {
		id := rootID
		if i > 0 {
			id = uuid.New()
		}
		hashtags := extractHashtags(e.Text)
		if len(hashtags) > 20 {
			hashtags = hashtags[:20]
		}
		hashtags = s.filterBlockedHashtags(ctx, hashtags)

		root := rootID
		seq := i
		p := &postgres.Post{
			ID:           id,
			AuthorID:     input.AuthorID,
			Text:         e.Text,
			Visibility:   visibility,
			ContentType:  "post",
			PostType:     "text",
			AppOrigin:    "postbook",
			Hashtags:     hashtags,
			ReviewStatus: reviewStatus,
			ThreadRootID: &root,
			ThreadSeq:    seq,
			// created_at increments by sequence so chronological surfaces
			// order entries deterministically even at equal timestamps.
			CreatedAt: now.Add(time.Duration(i) * time.Millisecond),
		}
		if prevID != nil {
			replyTo := *prevID
			p.ThreadReplyToID = &replyTo
		}
		for _, mediaID := range e.MediaIDs {
			// Kind comes from the ownership lookup already performed
			// above — no second query, and it is the verified row.
			kind := ownership[mediaID].Kind
			if kind == "" {
				kind = "image"
			}
			p.Media = append(p.Media, postgres.PostMedia{MediaID: mediaID, Kind: kind})
		}
		posts = append(posts, p)
		idCopy := id
		prevID = &idCopy

		// Feed collapse via P0-1: entries after the root are stamped
		// main_feed=false so only the root lands in social home.
		//
		// Codex P1-6: the canonical row must agree with the event. v1
		// emitted main_feed=false but left posts.distribution NULL, so a
		// read of the row said "eligible" while the projection said
		// "excluded". Persist the same policy on the row.
		mainFeed := i == 0
		notify := true
		entryPolicy := &DistributionPolicy{
			Version:           distributionPolicyVersion,
			MainFeed:          &mainFeed,
			NotifySubscribers: &notify,
		}
		storedPolicy, err := MarshalPolicy(entryPolicy)
		if err != nil {
			return nil, err
		}
		p.Distribution = storedPolicy
		p.DistributionRev = 1

		pc := events.PostCreatedPayload{
			PostID:            id.String(),
			AuthorID:          input.AuthorID.String(),
			Text:              e.Text,
			Visibility:        visibility,
			ContentType:       "post",
			CreatedAt:         p.CreatedAt,
			MainFeed:          &mainFeed,
			NotifySubscribers: &notify,
			DistributionRev:   1,
			// M2-P0-1: threads run the same spam gate as posts, so a
			// flagged thread must not be searchable either.
			ReviewStatus: reviewStatus,
			SearchRev:    1,
		}
		if s.producer != nil {
			outbox = append(outbox, postgres.ThreadOutboxEvent{
				EventType: events.PostCreated, PostID: id, Payload: pc,
			})
		}
	}

	err = s.pgStore.CreateThread(ctx, posts, input.IdempotencyKey, outbox)
	if err != nil {
		var replay *postgres.ThreadIdemReplayError
		if errors.As(err, &replay) {
			return s.pgStore.GetThreadPosts(ctx, replay.RootPostID)
		}
		return nil, err
	}

	out := make([]postgres.Post, 0, len(posts))
	for _, p := range posts {
		out = append(out, *p)
	}
	return out, nil
}

// GetThread returns the ordered, visible entries of the thread containing
// postID (any entry deep-links to full context — P0-8 acceptance).
// Visibility: private/followers-scoped threads are only returned to the
// author here; follower-scope refinement rides on the standard read path.
func (s *Service) GetThread(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) ([]postgres.Post, error) {
	p, err := s.pgStore.GetPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.ThreadRootID == nil {
		return nil, ErrPostNotFound
	}
	entries, err := s.pgStore.GetThreadPosts(ctx, *p.ThreadRootID)
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, ErrPostNotFound
	}

	// Codex P1-6: apply the real visibility policy instead of
	// author-only. v1 hid a followers-scoped thread from legitimate
	// followers, which is a correctness bug, not a safe default.
	if !s.canViewThread(ctx, &entries[0], viewerID) {
		return nil, ErrPostNotFound
	}
	return entries, nil
}

// canViewThread applies the same visibility rules a single post read
// uses: public is open; the author always sees their own; followers /
// trusted / close_friends consult graph-service; private is author-only.
func (s *Service) canViewThread(ctx context.Context, root *postgres.Post, viewerID *uuid.UUID) bool {
	if root.Visibility == "public" || root.Visibility == "unlisted" {
		// unlisted is reachable by direct link (which this is) but is
		// excluded from feeds/search elsewhere.
		return true
	}
	if viewerID == nil {
		return false
	}
	if *viewerID == root.AuthorID {
		return true
	}
	// One lookup serves both the block check and the audience check.
	// Fail-closed: an unavailable graph denies restricted content.
	rel, err := s.fetchRelationship(ctx, *viewerID, root.AuthorID)
	if err != nil || rel == nil {
		return false
	}
	// fixes-v2 / Codex P1-6: BOTH block directions. A viewer who blocked
	// the author must not receive the author's restricted content either.
	if rel.Blocked || rel.BlockedBy {
		return false
	}

	switch root.Visibility {
	case "followers":
		return rel.Follows
	case "trusted", "close_friends":
		// EXACT audience membership. v1 accepted any connection, which is
		// strictly broader than the author's close-friends list and
		// leaked restricted threads to connected-but-not-trusted users.
		return rel.IsCloseFriend
	default: // private and anything unrecognized
		return false
	}
}

type threadRelationship struct {
	Follows       bool `json:"follows"`
	Blocked       bool `json:"blocked"`
	BlockedBy     bool `json:"blocked_by"`
	IsConnection  bool `json:"is_connection"`
	IsCloseFriend bool `json:"is_close_friend"`
}

func (s *Service) fetchRelationship(ctx context.Context, viewerID, authorID uuid.UUID) (*threadRelationship, error) {
	if s.graphServiceURL == "" {
		return nil, fmt.Errorf("graph service not configured")
	}
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s",
		s.graphServiceURL, viewerID, authorID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("graph-service returned %d", resp.StatusCode)
	}
	var env struct {
		Data threadRelationship `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}
