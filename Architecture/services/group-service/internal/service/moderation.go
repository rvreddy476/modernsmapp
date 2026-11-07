package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

// ── Word Blocklist ───────────────────────────────────────────

var errBlocklistModerators = errors.New("forbidden: only admins or moderators can manage the word blocklist")

func (s *Service) AddWordToBlocklist(ctx context.Context, actorID, groupID uuid.UUID, word string) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator, errBlocklistModerators); err != nil {
		return err
	}
	word = strings.ToLower(strings.TrimSpace(word))
	if word == "" {
		return fmt.Errorf("word cannot be empty")
	}
	return s.store.AddWordToBlocklist(ctx, groupID, word, actorID)
}

func (s *Service) RemoveWordFromBlocklist(ctx context.Context, actorID, groupID uuid.UUID, word string) error {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator, errBlocklistModerators); err != nil {
		return err
	}
	return s.store.RemoveWordFromBlocklist(ctx, groupID, word)
}

func (s *Service) GetWordBlocklist(ctx context.Context, actorID, groupID uuid.UUID) ([]string, error) {
	if _, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can view the word blocklist")); err != nil {
		return nil, err
	}
	return s.store.GetWordBlocklist(ctx, groupID)
}

// ── Post Approval Queue ──────────────────────────────────────

/*
	The moderation queue, by post id.

	These read group_posts.status, which is where the truth has always been,
	and act by POST id. They replaced a queue (GetApprovalQueue and friends,
	on post_approval_queue) that nothing ever wrote; that queue and its routes
	were retired (R-L1) once no caller was left.
*/
func (s *Service) requirePostModerator(ctx context.Context, actorID, groupID uuid.UUID) error {
	_, err := s.authorizeRank(ctx, groupID, actorID, rankModerator,
		errors.New("forbidden: only admins or moderators can review posts"))
	return err
}

func (s *Service) ListPendingPosts(ctx context.Context, actorID, groupID uuid.UUID, limit, offset int) ([]store.GroupPostV2, error) {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return nil, err
	}
	return s.store.ListPendingGroupPostsV2(ctx, groupID, limit, offset)
}

func (s *Service) ApprovePendingPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return err
	}
	// The post must belong to THIS group: without the check, a moderator of
	// any group could approve a pending post in a group they cannot see, by
	// guessing or replaying an id.
	post, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if post == nil || post.GroupID != groupID {
		return fmt.Errorf("post not found")
	}
	if err := s.store.ApproveGroupPost(ctx, postID, actorID.String()); err != nil {
		return err
	}
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

func (s *Service) RejectPendingPost(ctx context.Context, actorID, groupID, postID uuid.UUID) error {
	if err := s.requirePostModerator(ctx, actorID, groupID); err != nil {
		return err
	}
	post, err := s.store.GetGroupPostV2(ctx, postID)
	if err != nil {
		return err
	}
	if post == nil || post.GroupID != groupID {
		return fmt.Errorf("post not found")
	}
	// No counter change: a pending post was never counted, so rejecting it
	// has nothing to undo.
	unlock, err := s.lockMediaHolder(ctx, postID)
	if err != nil {
		return err
	}
	defer unlock()
	if err := s.store.RejectGroupPost(ctx, postID); err != nil {
		return err
	}
	// A moderator's rejection is final (approval needs a pending post): the
	// post's attachments stop being held. Best effort, like every release.
	s.releasePostMedia(ctx, postID, post.Attachments)
	s.invalidateGroupCache(ctx, groupID)
	return nil
}

/*
	containsBlockedWord reports whether any of the group's blocked words appears
	in the text being posted, and which one.

	Whole-word matching on a lowercased haystack, not substring: a blocklist
	containing "ass" must not refuse "assignment" or "class". That is the
	difference between a moderation tool and a joke about the Scunthorpe
	problem.

	A failure to READ the blocklist does not block the post. This is the one
	place in this file that deliberately fails open, and the reason is that the
	alternative is worse: a Redis or Postgres blip would stop a whole group
	posting, which is a far bigger outage than a few words slipping past a
	filter that is advisory to begin with.
*/
func (s *Service) containsBlockedWord(ctx context.Context, groupID uuid.UUID, title, body string) (bool, string) {
	words, err := s.store.GetWordBlocklist(ctx, groupID)
	if err != nil || len(words) == 0 {
		return false, ""
	}
	haystack := strings.ToLower(title + " " + body)
	fields := strings.FieldsFunc(haystack, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	present := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		present[f] = struct{}{}
	}
	for _, w := range words {
		if _, hit := present[strings.ToLower(strings.TrimSpace(w))]; hit {
			return true, w
		}
	}
	return false, ""
}
