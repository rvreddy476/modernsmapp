package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

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

// countsForPost is the per-post read: Scylla likes + PostgreSQL comments.
func (s *Service) countsForPost(ctx context.Context, postID uuid.UUID) (*scylla.Counts, error) {
	counts, err := s.scyllaStore.GetCounts(ctx, postID)
	if err != nil {
		return nil, err
	}
	if counts == nil {
		counts = &scylla.Counts{}
	}
	n, err := s.pgStore.GetCommentCount(ctx, postID)
	if err != nil {
		return nil, fmt.Errorf("load comment count: %w", err)
	}
	counts.Comments = n
	return counts, nil
}

// overlayCommentCounts replaces the comment figure in a Scylla batch with the
// PostgreSQL one, in a single query for the page.
func (s *Service) overlayCommentCounts(ctx context.Context, ids []uuid.UUID, byPost map[uuid.UUID]*scylla.Counts) error {
	pg, err := s.pgStore.GetCommentCounts(ctx, ids)
	if err != nil {
		return fmt.Errorf("load comment counts: %w", err)
	}
	for _, id := range ids {
		c := byPost[id]
		if c == nil {
			c = &scylla.Counts{}
			byPost[id] = c
		}
		c.Comments = pg[id]
	}
	return nil
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
	p, err := s.getCachedPostBody(ctx, postID)
	if err != nil {
		return false, err
	}
	if p == nil {
		return false, nil
	}
	if p.ReviewStatus != "" && p.ReviewStatus != "approved" {
		if viewerID == nil || *viewerID != p.AuthorID {
			return false, nil
		}
	}
	if hiddenFromViewer(p, viewerID) {
		return false, nil
	}
	return s.viewerMayViewPost(ctx, p, viewerID), nil
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
