package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

/*
	Who wrote an anonymous post — for the group's owner, admins and
	moderators only.

	The post's wire shape masks the author for EVERYONE (store/anonymous.go),
	including admins, so nothing an admin's browser caches or a member's
	devtools shows carries the real id. Moderation needs the identity
	sometimes, so it is a separate, deliberate read: this endpoint, answered
	only to the roles that run the group, and written to group_admin_audit
	every time it answers for an anonymous post. A member asking gets 403 and
	learns nothing; a moderator asking leaves a trail.
*/

type AuthorReveal struct {
	PostID      uuid.UUID `json:"post_id"`
	AuthorID    string    `json:"author_id"`
	IsAnonymous bool      `json:"is_anonymous"`
	// Present only when an anonymous author was revealed (and audited).
	RevealedAt *time.Time `json:"revealed_at,omitempty"`
}

var ErrRevealForbidden = fmt.Errorf("forbidden: only the group's owner, admins and moderators can see who posted anonymously")

// canModerate reports whether the actor runs this group: its creator, or an
// active member with the admin or moderator role.
func (s *Service) canModerate(ctx context.Context, groupID, creatorID, actorID uuid.UUID) (bool, error) {
	if creatorID == actorID {
		return true, nil
	}
	m, err := s.store.GetMember(ctx, groupID, actorID)
	if err != nil {
		return false, err
	}
	if m == nil || m.Status != "active" {
		return false, nil
	}
	return m.Role == "admin" || m.Role == "moderator" || m.Role == "owner", nil
}

func (s *Service) RevealPostAuthor(ctx context.Context, actorID, groupID, postID uuid.UUID) (*AuthorReveal, error) {
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil || g.Status == "deleted" {
		return nil, fmt.Errorf("not found: group not found")
	}
	ok, err := s.canModerate(ctx, groupID, g.CreatorID, actorID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrRevealForbidden
	}
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return nil, err
	}
	if p == nil || p.GroupID != groupID {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	out := &AuthorReveal{PostID: p.ID, AuthorID: p.AuthorID, IsAnonymous: p.IsAnonymous}
	if !p.IsAnonymous {
		return out, nil
	}
	// The audit row is written BEFORE the identity leaves this function; a
	// reveal that could not be recorded is not answered.
	if err := s.store.RecordAuthorReveal(ctx, actorID, groupID, postID, p.AuthorID); err != nil {
		return nil, fmt.Errorf("record reveal: %w", err)
	}
	now := time.Now()
	out.RevealedAt = &now
	return out, nil
}
