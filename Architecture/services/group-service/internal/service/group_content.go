package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// Every read below goes through the access gate (G-M4): before, the rules,
// channels and wiki of a private group were readable by anyone holding its id.
// Every write is scoped by the PATH group, in the gate and in the SQL (G-M3).

// --- Rules ---

// GetGroupRules returns the rules for a group.
func (s *Service) GetGroupRules(ctx context.Context, actorID, groupID uuid.UUID) ([]store.GroupRule, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListGroupRules(ctx, groupID)
}

// UpdateGroupRules replaces all rules for a group. Only admins may do this.
func (s *Service) UpdateGroupRules(ctx context.Context, actorID, groupID uuid.UUID, rules []store.GroupRule) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankAdmin,
		errors.New("forbidden: only admins can update group rules")); err != nil {
		return err
	}

	if len(rules) > 10 {
		return fmt.Errorf("maximum 10 rules per group")
	}

	return s.store.ReplaceGroupRules(ctx, groupID, rules)
}

// GetGroupMedia returns media posts for a group.
func (s *Service) GetGroupMedia(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupMediaItem, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}

	return s.store.ListGroupMedia(ctx, groupID, limit, offset)
}

// ── Group Channels ───────────────────────────────────────────

func (s *Service) CreateGroupChannel(ctx context.Context, actorID, groupID uuid.UUID, name, chanType, description string) (*store.GroupChannel, error) {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can create channels")); err != nil {
		return nil, err
	}
	ch := &store.GroupChannel{
		GroupID:     groupID,
		Name:        name,
		Type:        chanType,
		Description: description,
		CreatedBy:   actorID,
	}
	return s.store.CreateGroupChannel(ctx, ch)
}

func (s *Service) ListGroupChannels(ctx context.Context, actorID, groupID uuid.UUID) ([]store.GroupChannel, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListGroupChannels(ctx, groupID)
}

func (s *Service) DeleteGroupChannel(ctx context.Context, actorID, groupID, channelID uuid.UUID) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can delete channels")); err != nil {
		return err
	}
	return s.store.DeleteGroupChannel(ctx, channelID, groupID)
}

// ── Group Wiki ───────────────────────────────────────────────

func (s *Service) CreateWikiPage(ctx context.Context, actorID, groupID uuid.UUID, title, content string) (*store.WikiPage, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessWrite); err != nil {
		return nil, err
	}
	return s.store.CreateWikiPage(ctx, groupID, actorID, title, content)
}

// UpdateWikiPage edits a page of the PATH group. The store matches the page by
// id AND group_id, so a member of group A can no longer overwrite (and read
// back) a page of group B by its id (G-M3).
func (s *Service) UpdateWikiPage(ctx context.Context, actorID, groupID, pageID uuid.UUID, title, content string) (*store.WikiPage, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessWrite); err != nil {
		return nil, err
	}
	return s.store.UpdateWikiPage(ctx, groupID, pageID, actorID, title, content)
}

// ListWikiPages returns one page of a group's wiki (the list used to be
// unbounded, with full content).
func (s *Service) ListWikiPages(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.WikiPage, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	return s.store.ListWikiPages(ctx, groupID, limit, offset)
}

func (s *Service) DeleteWikiPage(ctx context.Context, actorID, groupID, pageID uuid.UUID) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can delete wiki pages")); err != nil {
		return err
	}
	return s.store.DeleteWikiPage(ctx, pageID, groupID)
}
