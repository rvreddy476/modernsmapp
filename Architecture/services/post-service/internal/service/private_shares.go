package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

/*
	Private sharing (Creator Hub part 6b, 2026-09-28; migration 053).

	  GET /v1/posts/:postId/private-shares   owner only -> {users:[...]}
	  PUT /v1/posts/:postId/private-shares   owner only, body {user_ids}
	    replaces the list -> {users:[...]}
	    each user: {user_id, username, display_name, avatar_url, added_at}

	401 no identity; 404 when the caller may not see the post at all (so a
	probe learns nothing), 403 FORBIDDEN when they may see it but do not own
	it (a viewer on the list lands here and never reads the list); 422
	TOO_MANY_SHARES past MaxPrivateShares distinct ids, 422 INVALID_USER for
	the owner's own id or an id identity-profile does not know; 503 when the
	users cannot be verified.

	What the list does (and does not):
	  * a 'private' post opens to the users on it through the single-post
	    gates only: viewerMayViewPost (detail, comments read, ws room),
	    evaluatePostMediaVisibility (playback / download authorisation) and
	    canViewThread;
	  * it never widens a listing: GetPostsByIDs, by-author, recent,
	    trending, hashtag, reel and series listings keep dropping every
	    private post for everyone but the owner, and nothing here emits an
	    event, so no feed, search document or notification hears of a share;
	  * engagement writes (like, comment, bookmark, Queue) stay owner-only on
	    a private post (loadPostForEngagement is unchanged).
*/

// MaxPrivateShares caps one post's share list.
const MaxPrivateShares = 50

var (
	ErrTooManyShares     = fmt.Errorf("a post can be shared privately with at most %d people", MaxPrivateShares)
	ErrInvalidShareUser  = errors.New("user_ids must name existing accounts other than your own")
	ErrShareUsersUnknown = errors.New("could not verify the users to share with; try again")
)

// privateShareStore is the storage slice (store/postgres/private_shares.go).
type privateShareStore interface {
	ListPrivateShares(ctx context.Context, postID uuid.UUID) ([]postgres.PrivateShare, error)
	ReplacePrivateShares(ctx context.Context, postID, ownerID uuid.UUID, userIDs []uuid.UUID) ([]postgres.PrivateShare, error)
	PrivateSharedPostIDs(ctx context.Context, viewerID uuid.UUID, postIDs []uuid.UUID) (map[uuid.UUID]bool, error)
}

// PrivateShareUser is one entry of the owner's list.
type PrivateShareUser struct {
	UserID      uuid.UUID `json:"user_id"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	AddedAt     time.Time `json:"added_at"`
}

// PrivateSharesView is the body of both routes.
type PrivateSharesView struct {
	Users []PrivateShareUser `json:"users"`
}

// sharedWithViewer reports whether viewerID is on postID's share list.
// Fails closed: no store or a lookup error is "not shared".
func (s *Service) sharedWithViewer(ctx context.Context, postID uuid.UUID, viewerID *uuid.UUID) bool {
	if viewerID == nil || *viewerID == uuid.Nil || s.privateShare == nil {
		return false
	}
	shared, err := s.privateShare.PrivateSharedPostIDs(ctx, *viewerID, []uuid.UUID{postID})
	if err != nil {
		slog.WarnContext(ctx, "private share lookup failed; denying", "post_id", postID, "err", err)
		return false
	}
	return shared[postID]
}

// requireShareOwner establishes that callerID owns postID: 404 when the
// caller may not see the post, 403 when they may but it is not theirs.
func (s *Service) requireShareOwner(ctx context.Context, callerID, postID uuid.UUID) error {
	if s.postEdits == nil || s.privateShare == nil {
		return ErrAuthoringStoreUnavailable
	}
	if callerID == uuid.Nil {
		return ErrNotPostAuthor
	}
	p, err := s.postEdits.GetPost(ctx, postID)
	if err != nil {
		return fmt.Errorf("load post: %w", err)
	}
	if p == nil {
		return ErrPostNotFound
	}
	if p.AuthorID == callerID {
		return nil
	}
	if s.pgStore != nil {
		visible, err := s.PostVisibleTo(ctx, postID, &callerID)
		if err != nil {
			return err
		}
		if !visible {
			return ErrPostNotVisible
		}
	} else if p.Visibility == "private" && !s.sharedWithViewer(ctx, postID, &callerID) {
		return ErrPostNotVisible
	}
	return ErrNotPostAuthor
}

// ListPrivateShares is GET /v1/posts/:postId/private-shares.
func (s *Service) ListPrivateShares(ctx context.Context, callerID, postID uuid.UUID) (*PrivateSharesView, error) {
	if err := s.requireShareOwner(ctx, callerID, postID); err != nil {
		return nil, err
	}
	rows, err := s.privateShare.ListPrivateShares(ctx, postID)
	if err != nil {
		return nil, err
	}
	return s.privateSharesView(ctx, callerID, rows)
}

// SetPrivateShares is PUT /v1/posts/:postId/private-shares.
func (s *Service) SetPrivateShares(ctx context.Context, callerID, postID uuid.UUID, userIDs []uuid.UUID) (*PrivateSharesView, error) {
	if err := s.requireShareOwner(ctx, callerID, postID); err != nil {
		return nil, err
	}
	ids := dedupeUUIDs(userIDs)
	if len(ids) > MaxPrivateShares {
		return nil, ErrTooManyShares
	}
	for _, id := range ids {
		if id == callerID {
			return nil, ErrInvalidShareUser
		}
	}
	if len(ids) > 0 {
		known, err := s.shareProfiles(ctx, callerID, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if _, ok := known[id]; !ok {
				return nil, ErrInvalidShareUser
			}
		}
	}
	rows, err := s.privateShare.ReplacePrivateShares(ctx, postID, callerID, ids)
	if err != nil {
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			return nil, ErrPostNotFound
		case errors.Is(err, postgres.ErrPostEditNotOwned):
			return nil, ErrNotPostAuthor
		case errors.Is(err, postgres.ErrPrivateShareUnknownUser):
			return nil, ErrInvalidShareUser
		}
		return nil, err
	}
	return s.privateSharesView(ctx, callerID, rows)
}

// shareProfiles resolves ids through identity-profile's batch (the same
// contract comment authors use), asked as the owner so an account the owner
// cannot see does not resolve. Unconfigured or failing: ErrShareUsersUnknown
// (the write refuses rather than store an unverified id).
func (s *Service) shareProfiles(ctx context.Context, ownerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]commentProfile, error) {
	if s.profileServiceURL == "" {
		return nil, ErrShareUsersUnknown
	}
	raw := make([]string, 0, len(ids))
	for _, id := range ids {
		raw = append(raw, id.String())
	}
	profiles, err := s.fetchCommentProfiles(ctx, &ownerID, raw)
	if err != nil {
		slog.WarnContext(ctx, "private shares: profile lookup failed", "err", err)
		return nil, ErrShareUsersUnknown
	}
	return profiles, nil
}

// privateSharesView names each row. After a write the profiles were just
// verified, so a lookup failure here only blanks the names; the list read
// is best-effort the same way.
func (s *Service) privateSharesView(ctx context.Context, ownerID uuid.UUID, rows []postgres.PrivateShare) (*PrivateSharesView, error) {
	out := &PrivateSharesView{Users: make([]PrivateShareUser, 0, len(rows))}
	var profiles map[uuid.UUID]commentProfile
	if len(rows) > 0 && s.profileServiceURL != "" {
		ids := make([]string, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.UserID.String())
		}
		if p, err := s.fetchCommentProfiles(ctx, &ownerID, ids); err == nil {
			profiles = p
		} else {
			slog.WarnContext(ctx, "private shares: name hydration skipped", "err", err)
		}
	}
	for _, r := range rows {
		u := PrivateShareUser{UserID: r.UserID, AddedAt: r.AddedAt}
		if p, ok := profiles[r.UserID]; ok {
			u.Username, u.DisplayName = p.Username, p.DisplayName
			if p.AvatarMediaID != nil {
				u.AvatarURL = fmt.Sprintf("/v1/media/%s/serve/avatar", p.AvatarMediaID)
			}
		}
		out.Users = append(out.Users, u)
	}
	return out, nil
}
