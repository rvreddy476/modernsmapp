package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
)

/*
	counts.comments comes from PostgreSQL post_engagement_counts on every
	read surface — detail, batch (which the feed hydrates from), by-author,
	recent, bookmarks, hashtag, trending, my uploads, and the realtime
	frames. counts.likes still comes from Scylla post_counters; likes have
	their own, separate inconsistency (five stores) that this does not touch.
	See store/postgres/comment_counts.go for the definition.
*/

// countsForPost is the per-post read: Scylla likes + PostgreSQL comments,
// shares and saves.
func (s *Service) countsForPost(ctx context.Context, postID uuid.UUID) (*scylla.Counts, error) {
	counts, err := s.likeCountsForPost(ctx, postID)
	if err != nil {
		return nil, err
	}
	store := s.engagementRowReads()
	if store == nil {
		return counts, nil
	}
	rows, err := store.GetEngagementCountRows(ctx, []uuid.UUID{postID})
	if err != nil {
		return nil, fmt.Errorf("load comment count: %w", err)
	}
	applyEngagementRow(counts, rows[postID])
	return counts, nil
}

// likeCountsForPost is countsForPost's Scylla half, never nil on success.
// A Service built without a counter store (service.New(pg, nil, …): the
// integration rigs that run without Scylla, M7_SCYLLA_HOSTS unset) reads
// zero likes, as countsForSoundPosts does, instead of dereferencing a nil
// store; cmd/server always wires one.
//
// A Scylla error degrades to zero likes, logged, rather than failing the
// read (2026-10-30 audit B6): the post body is PostgreSQL's and is fine; a
// degraded counter cluster must not turn every direct open into a 500.
func (s *Service) likeCountsForPost(ctx context.Context, postID uuid.UUID) (*scylla.Counts, error) {
	reads := s.counterReads()
	if reads == nil {
		return &scylla.Counts{}, nil
	}
	counts, err := reads.GetCounts(ctx, postID)
	if err != nil {
		slog.WarnContext(ctx, "post like count unavailable; serving 0", "post_id", postID, "err", err)
		return &scylla.Counts{}, nil
	}
	if counts == nil {
		counts = &scylla.Counts{}
	}
	return counts, nil
}

// viewerStoredReaction is the viewer's stored reaction on a post ("" for
// none). Without a counter store there is nothing stored to read; see
// likeCountsForPost.
func (s *Service) viewerStoredReaction(ctx context.Context, postID, viewerID uuid.UUID) (string, error) {
	reads := s.counterReads()
	if reads == nil {
		return "", nil
	}
	return reads.GetReaction(ctx, postID, viewerID)
}

// counterReadStore is the Scylla read slice: counts and the viewer's
// reaction, one post or a page. *scylla.InteractionStore satisfies it;
// counterReadSeam replaces it in tests.
type counterReadStore interface {
	GetCounts(ctx context.Context, postID uuid.UUID) (*scylla.Counts, error)
	GetReaction(ctx context.Context, postID, userID uuid.UUID) (string, error)
	BatchGetCounts(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]*scylla.Counts, error)
	BatchGetReactions(ctx context.Context, postIDs []uuid.UUID, userID uuid.UUID) (map[uuid.UUID]string, error)
}

// counterReads is the seam, else the Scylla store, else nil (no counter
// store: zero likes, no reactions).
func (s *Service) counterReads() counterReadStore {
	if s.counterReadSeam != nil {
		return s.counterReadSeam
	}
	if s.scyllaStore != nil {
		return s.scyllaStore
	}
	return nil
}

// engagementRowStore is the PostgreSQL engagement-count read.
type engagementRowStore interface {
	GetEngagementCountRows(ctx context.Context, postIDs []uuid.UUID) (map[uuid.UUID]postgres.EngagementCountRow, error)
}

func (s *Service) engagementRowReads() engagementRowStore {
	if s.engagementRowSeam != nil {
		return s.engagementRowSeam
	}
	if s.pgStore != nil {
		return s.pgStore
	}
	return nil
}

// pageCounts is the counts of a page of posts in two round trips — Scylla's
// like counters for the page (bounded fan-out in the store) and PostgreSQL's
// comment / share / save rows in one query — instead of two per row. It
// never fails the page: either half that errors is logged and reads zero.
// Every id has a non-nil Counts.
func (s *Service) pageCounts(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]*scylla.Counts {
	out := make(map[uuid.UUID]*scylla.Counts, len(ids))
	if len(ids) == 0 {
		return out
	}
	if reads := s.counterReads(); reads != nil {
		got, err := reads.BatchGetCounts(ctx, ids)
		if err != nil {
			slog.WarnContext(ctx, "page like counts unavailable; serving 0", "posts", len(ids), "err", err)
		} else {
			for id, c := range got {
				if c != nil {
					out[id] = c
				}
			}
		}
	}
	var rows map[uuid.UUID]postgres.EngagementCountRow
	if store := s.engagementRowReads(); store != nil {
		var err error
		rows, err = store.GetEngagementCountRows(ctx, ids)
		if err != nil {
			slog.WarnContext(ctx, "page engagement counts unavailable; serving 0", "posts", len(ids), "err", err)
			rows = nil
		}
	}
	overlayEngagementRows(ids, out, rows)
	return out
}

// pageReactions is the viewer's stored reaction on each post of a page.
// A Scylla error degrades to "no reaction shown", logged, never a failed
// page (2026-10-30 audit B6).
func (s *Service) pageReactions(ctx context.Context, ids []uuid.UUID, viewerID uuid.UUID) map[uuid.UUID]string {
	reads := s.counterReads()
	if reads == nil || len(ids) == 0 {
		return map[uuid.UUID]string{}
	}
	got, err := reads.BatchGetReactions(ctx, ids, viewerID)
	if err != nil {
		slog.WarnContext(ctx, "viewer reactions unavailable; serving none", "posts", len(ids), "err", err)
		return map[uuid.UUID]string{}
	}
	if got == nil {
		got = map[uuid.UUID]string{}
	}
	return got
}

// overlayCommentCounts replaces the comment figure in a Scylla batch with the
// PostgreSQL one, and adds the share and save figures, in a single query for
// the page.
func (s *Service) overlayCommentCounts(ctx context.Context, ids []uuid.UUID, byPost map[uuid.UUID]*scylla.Counts) error {
	rows, err := s.pgStore.GetEngagementCountRows(ctx, ids)
	if err != nil {
		return fmt.Errorf("load comment counts: %w", err)
	}
	overlayEngagementRows(ids, byPost, rows)
	return nil
}

// overlayEngagementRows is overlayCommentCounts' pure half: every id gets a
// Counts (zeros when it had none) carrying the PostgreSQL figures.
func overlayEngagementRows(ids []uuid.UUID, byPost map[uuid.UUID]*scylla.Counts, rows map[uuid.UUID]postgres.EngagementCountRow) {
	for _, id := range ids {
		c := byPost[id]
		if c == nil {
			c = &scylla.Counts{}
			byPost[id] = c
		}
		applyEngagementRow(c, rows[id])
	}
}

// applyEngagementRow writes one post's PostgreSQL figures onto its Counts:
// counts.comments, counts.shares (share_count) and counts.saves
// (bookmark_count). A post with no row reads zero for all three.
func applyEngagementRow(c *scylla.Counts, row postgres.EngagementCountRow) {
	c.Comments = row.Comments
	c.Shares = row.Shares
	c.Saves = row.Saves
}

/*
comment_change — the realtime frame for every comment mutation.

Published on Redis channel post:<post_id> (the room ws-gateway subscribes
a viewer to after checking they may see the post), inside the {type,
payload} envelope the gateway relays; the gateway drops the actor's own
frame (they already know). It carries NO comment body: a subscriber
re-reads the thread through the authorized list endpoint, which applies
moderation and block rules; the frame only says "something changed".

	{"type":"comment_change","payload":{
	   "event_id":   uuid,        // for de-duplication
	   "version":    int64,       // unix micros; later wins
	   "post_id":    uuid,
	   "comment_id": uuid,
	   "parent_id":  uuid|absent, // for replies
	   "change":     "created"|"replied"|"edited"|"deleted"|"reaction"|"moderated",
	   "actor_id":   uuid,
	   "comments":   int64        // the authoritative count after the change
	}}
*/
const (
	CommentChangeCreated   = "created"
	CommentChangeReplied   = "replied"
	CommentChangeEdited    = "edited"
	CommentChangeDeleted   = "deleted"
	CommentChangeReaction  = "reaction"
	CommentChangeModerated = "moderated"
)

type commentChangePayload struct {
	EventID   string  `json:"event_id"`
	Version   int64   `json:"version"`
	PostID    string  `json:"post_id"`
	CommentID string  `json:"comment_id"`
	ParentID  *string `json:"parent_id,omitempty"`
	Change    string  `json:"change"`
	ActorID   string  `json:"actor_id"`
	Comments  int64   `json:"comments"`
}

// BuildCommentChange is the pure part, so tests can pin the frame.
func BuildCommentChange(postID, commentID uuid.UUID, parentID *uuid.UUID, change string, actorID uuid.UUID, comments int64, now time.Time) ([]byte, error) {
	p := commentChangePayload{
		EventID:   uuid.NewString(),
		Version:   now.UnixMicro(),
		PostID:    postID.String(),
		CommentID: commentID.String(),
		Change:    change,
		ActorID:   actorID.String(),
		Comments:  comments,
	}
	if parentID != nil {
		v := parentID.String()
		p.ParentID = &v
	}
	return json.Marshal(map[string]any{"type": "comment_change", "payload": p})
}

// publishCommentChange is best-effort and never blocks the request path.
func (s *Service) publishCommentChange(postID, commentID uuid.UUID, parentID *uuid.UUID, change string, actorID uuid.UUID) {
	if s.rdb == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := s.pgStore.GetCommentCount(ctx, postID)
		if err != nil {
			slog.Warn("comment_change: count read failed", "post_id", postID, "err", err)
		}
		frame, err := BuildCommentChange(postID, commentID, parentID, change, actorID, n, time.Now())
		if err != nil {
			return
		}
		if err := s.rdb.Publish(ctx, "post:"+postID.String(), frame).Err(); err != nil {
			slog.Warn("comment_change: publish failed", "post_id", postID, "err", err)
		}
	}()
}

// PostVisibleTo answers ws-gateway's room question: may this viewer see this
// post? It is the same decision GetPost makes before returning a body
// (review status, scheduled/hidden, visibility incl. private accounts and
// blocks), so a room admits exactly the audience the post itself admits.
func (s *Service) PostVisibleTo(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (bool, error) {
	p, err := s.loadVisiblePost(ctx, postID, viewerID)
	return p != nil, err
}

// loadVisiblePost is PostVisibleTo returning the post: nil (no error) when
// the viewer may not see it. Private sharing counts (viewerMayViewPost);
// the age gate does not — callers that serve content add checkAgeGate.
func (s *Service) loadVisiblePost(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) (*postgres.Post, error) {
	p, err := s.getCachedPostBody(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, nil
	}
	if p.EffectiveReviewStatus() != "" && p.EffectiveReviewStatus() != "approved" {
		if viewerID == nil || *viewerID != p.AuthorID {
			return nil, nil
		}
	}
	if hiddenFromViewer(p, viewerID) {
		return nil, nil
	}
	if !s.viewerMayViewPost(ctx, p, viewerID) {
		return nil, nil
	}
	return p, nil
}

// singlePostRead is the direct read's decision for routes that hold only a
// post id and serve its content (GET /v1/videos/:id, the comments read):
// ErrPostNotVisible when the viewer may not see the post, an age refusal
// (age_gate.go) when it is 18+ and they do not pass, nil otherwise.
// readGate replaces it in tests.
func (s *Service) singlePostRead(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) error {
	if s.readGate != nil {
		return s.readGate(ctx, postID, viewerID)
	}
	if s.pgStore == nil {
		return ErrAuthoringStoreUnavailable
	}
	p, err := s.loadVisiblePost(ctx, postID, viewerID)
	if err != nil {
		return err
	}
	if p == nil {
		return ErrPostNotVisible
	}
	return s.checkAgeGate(ctx, p, viewerID)
}

// PostAuthor is the author block GET /v1/posts/:id now carries, in the shape
// the feed already uses (feed-service enrichRenderData), from the same
// source (identity-profile's batch). Best-effort: a profile-service blip
// leaves it absent rather than failing the read.
type PostAuthor struct {
	ID            uuid.UUID  `json:"id"`
	DisplayName   string     `json:"display_name"`
	Username      string     `json:"username"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id,omitempty"`
	AvatarURL     string     `json:"avatar_url,omitempty"`
}

func (s *Service) fetchPostAuthor(ctx context.Context, viewerID *uuid.UUID, authorID uuid.UUID) *PostAuthor {
	if s.profileServiceURL == "" {
		return nil
	}
	profiles, err := s.fetchCommentProfiles(ctx, viewerID, []string{authorID.String()})
	if err != nil {
		slog.Debug("post author hydration skipped", "author_id", authorID, "err", err)
		return nil
	}
	pr, ok := profiles[authorID]
	if !ok {
		return &PostAuthor{ID: authorID, DisplayName: "Deleted account"}
	}
	a := &PostAuthor{ID: authorID, DisplayName: pr.DisplayName, Username: pr.Username, AvatarMediaID: pr.AvatarMediaID}
	if pr.AvatarMediaID != nil {
		a.AvatarURL = fmt.Sprintf("/v1/media/%s/serve/avatar", pr.AvatarMediaID)
	}
	return a
}
