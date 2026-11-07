package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// ── V2 Group Posts ───────────────────────────────────────────

type CreateGroupPostV2Params struct {
	Body           string          `json:"body"`
	Title          string          `json:"title,omitempty"`
	ContentType    string          `json:"content_type"`
	ChannelID      *uuid.UUID      `json:"channel_id,omitempty"`
	TypePayload    json.RawMessage `json:"type_payload,omitempty"`
	Attachments    json.RawMessage `json:"attachments,omitempty"`
	IsAnnouncement bool            `json:"is_announcement"`
	// Post without the author's name shown to other members. Refused unless
	// the group has opted in.
	IsAnonymous bool `json:"is_anonymous"`
	// Additional groups to post the same body to. Empty for an ordinary
	// single post, which keeps the existing response shape — that is the
	// compatibility hinge for every caller that already exists.
	AlsoPostTo []uuid.UUID `json:"also_post_to,omitempty"`
	// Stable across retries of the SAME composer action. Required only when
	// also_post_to is used, so a single post is unchanged.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

/*
	CreateGroupPostV2 posts to one group.

	The evaluation lives in createOnePost (cross_post.go), shared with every
	target of a cross-post so the two cannot drift into different answers for
	the same person and group. Here a policy refusal becomes an ERROR, because
	a caller who named exactly one group should be told why it failed; in a
	batch the same refusal is an outcome for that target and does not fail the
	request.
*/
func (s *Service) CreateGroupPostV2(ctx context.Context, actorID, groupID uuid.UUID, params CreateGroupPostV2Params) (*store.GroupPostV2, error) {
	// Before any read: a flood should not cost a database round trip each.
	if !s.rateLimiter.Allow(ctx, fmt.Sprintf("rl:group_post:%s", actorID), 30, time.Hour) {
		return nil, fmt.Errorf("rate_limited: you are posting too quickly")
	}

	outcome, post, err := s.createOnePost(ctx, actorID, groupID, params, nil)
	if err != nil {
		return nil, err
	}
	if post == nil {
		return nil, outcomeAsError(outcome)
	}
	return post, nil
}

/*
	outcomeAsError turns a per-target outcome into the error a single-group
	caller gets.

	The single path can be specific where a batch cannot: naming the reason
	here tells the author something about a group they explicitly chose, with
	nobody else's privacy involved. The one exception is kept lossy anyway —
	"unavailable" still refuses to distinguish a missing group from a private
	one, because that answer is a probe whether it arrives one at a time or
	five at a time.
*/
func outcomeAsError(outcome string) error {
	switch outcome {
	case OutcomeNotAMember:
		return fmt.Errorf("forbidden: only members can post in the group")
	case OutcomeBanned:
		return fmt.Errorf("forbidden: you are banned from this group")
	case OutcomeNotPermitted:
		return fmt.Errorf("forbidden: your role cannot post in this group")
	case OutcomeAnonNotAllowed:
		return fmt.Errorf("forbidden: this group does not allow anonymous posts")
	case OutcomeBlockedContent:
		return fmt.Errorf("blocked_content: that post contains a word this group does not allow")
	default:
		return fmt.Errorf("group not found")
	}
}

func (s *Service) GetGroupFeedV2(ctx context.Context, actorID, groupID uuid.UUID, channelID *uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	// The group-read rule, by its shared name: post search calls the same one
	// (group_post_search.go), and checkGroupAccess is the access gate in read
	// mode (access.go).
	g, err := s.store.GetGroupByID(ctx, groupID)
	if err != nil {
		return nil, err
	}
	if g == nil {
		return nil, ErrGroupNotFound
	}
	if err := s.checkGroupAccess(ctx, g, actorID); err != nil {
		return nil, err
	}

	return s.store.ListGroupPostsV2(ctx, groupID, channelID, viewerKey(actorID), limit, offset)
}

func (s *Service) GetGroupPostV2(ctx context.Context, actorID, groupID, postID uuid.UUID) (*store.GroupPostV2, error) {
	if _, err := s.authorize(ctx, groupID, actorID, accessRead); err != nil {
		return nil, err
	}
	p, err := s.store.GetGroupPostV2ForViewer(ctx, postID, viewerKey(actorID))
	if err != nil {
		return nil, err
	}
	if p.GroupID != groupID {
		return nil, fmt.Errorf("not found: post not found in this group")
	}
	return p, nil
}

func (s *Service) DeleteGroupPostV2(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	acc, err := s.authorize(ctx, groupID, actorID, accessRead)
	if err != nil {
		return err
	}
	p, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if p == nil || p.GroupID != groupID {
		return fmt.Errorf("not found: post not found in this group")
	}

	// Author can delete own post, the group's moderators and above any.
	if p.AuthorID != actorID.String() && acc.rank < rankModerator {
		return fmt.Errorf("forbidden: only post author, admins, or moderators can delete posts")
	}

	unlock, err := s.lockMediaHolder(ctx, postID)
	if err != nil {
		return err
	}
	err = s.store.DeleteGroupPostV2(ctx, groupID, postID)
	if err == nil {
		// A deleted post is never shown again: release its attachments.
		s.releasePostMedia(ctx, postID, p.Attachments)
	}
	unlock()
	if err != nil {
		return err
	}
	// Named as the post knows the actor: the alias when the anonymous
	// author deletes their own post.
	publicID := s.publicActorID(p, actorID)
	s.publishEvent(func() error {
		return s.producer.PublishGroupPostDeleted(ctx, groupID, postID, publicID)
	})
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

// ── V2 Engagement ────────────────────────────────────────────

// SparkGroupPost is the legacy heart; since reactions landed it is a 'like'.
// It now passes engagementGate (group access, ban, post∈group) like every
// other engagement write — see service/reactions.go.
func (s *Service) SparkGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID, isSupernova bool) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	if err := s.store.SparkGroupPost(ctx, postID, actorID.String(), isSupernova); err != nil {
		return err
	}
	s.store.IncrementMemberSparks(ctx, groupID, actorID, 1)
	return nil
}

// UnsparkGroupPost removes the viewer's reaction whatever it is; a legacy
// client that hearted and then reacted Love from the web still unhearts.
func (s *Service) UnsparkGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	return s.store.UnsparkGroupPost(ctx, postID, actorID.String())
}

// Stash, view and echo pass engagementGate like spark and react (G-M4): they
// used to check only that the post was in the path group, so a stranger, or a
// banned member, could stash, echo or count a view on a private group's post.

func (s *Service) StashGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	return s.store.StashGroupPost(ctx, postID, actorID.String())
}

func (s *Service) UnstashGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	return s.store.UnstashGroupPost(ctx, postID, actorID.String())
}

func (s *Service) RecordGroupPostView(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	return s.store.RecordGroupPostView(ctx, postID, actorID.String())
}

func (s *Service) EchoGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID, echoType string) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	if echoType == "" {
		echoType = "share"
	}
	return s.store.EchoGroupPost(ctx, postID, actorID.String(), echoType)
}

func (s *Service) UnechoGroupPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if _, err := s.engagementGate(ctx, actorID, groupID, postID); err != nil {
		return err
	}
	return s.store.UnechoGroupPost(ctx, postID, actorID.String())
}

// GetMyGroupsFeed returns the aggregated reverse-chronological feed of
// published posts across all groups the actor belongs to. Access control is
// the membership join itself — only groups the actor is a member of
// contribute posts.
func (s *Service) GetMyGroupsFeed(ctx context.Context, actorID uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	return s.store.ListMyGroupsFeed(ctx, actorID, limit, offset)
}
