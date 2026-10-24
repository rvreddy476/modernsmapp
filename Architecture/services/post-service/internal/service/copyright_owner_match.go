package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Owner matches (founder decision 24 Oct 2026: "Yes, block it with mail
// notification"; migration 075).
//
// ApplyOwnerMatch is THE hook a future fingerprint provider calls when a
// post matches content a rights owner registered. There is no provider yet;
// the catalogue's auto_block entries already use the same block path at
// publication (decideTitleHold → insertTitleReviewHoldTx → systemBlockTx).
//
//	block  blocked at once by the system (reason owner_registered), counted
//	       toward the repeat-uploader pause, the author told in-app and by
//	       email. With COPYRIGHT_AUTO_BLOCK_ENABLED=false it is held for a
//	       reviewer instead.
//	mute   held for a reviewer with reason mute_requested — there is no
//	       audio muting yet (reported as a gap); the author is told it is
//	       in review.
//	track  nothing is hidden and nobody is told; the post's earnings are
//	       excluded through the monetization eligibility projection.
//
// The uploader's own channel never matches its own title (the entry's
// owner_channel_ids), exactly as for the title rule.

// OwnerMatch names the registered owner's content a post matched.
type OwnerMatch struct {
	// TitleID is the rights owner's registered catalogue entry.
	TitleID uuid.UUID
	// Reference is the provider's match id (optional, ≤200 characters),
	// kept on the review's history row.
	Reference string
}

// OwnerPolicy is what the rights owner asked for: block, mute or track.
type OwnerPolicy = postgres.OwnerMatchPolicy

const (
	OwnerPolicyBlock = postgres.OwnerPolicyBlock
	OwnerPolicyMute  = postgres.OwnerPolicyMute
	OwnerPolicyTrack = postgres.OwnerPolicyTrack
)

// ApplyOwnerMatch applies one owner match to one post. The result's outcome
// says what happened (postgres.OwnerMatch*); an error means nothing was
// written and the caller may retry.
func (s *Service) ApplyOwnerMatch(ctx context.Context, postID uuid.UUID, owner OwnerMatch, policy OwnerPolicy) (*postgres.OwnerMatchResult, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	policy = OwnerPolicy(strings.ToLower(strings.TrimSpace(string(policy))))
	switch policy {
	case OwnerPolicyBlock, OwnerPolicyMute, OwnerPolicyTrack:
	default:
		return nil, fmt.Errorf("%w: policy must be block, mute or track", postgres.ErrTitleReviewInvalid)
	}
	if postID == uuid.Nil || owner.TitleID == uuid.Nil {
		return nil, fmt.Errorf("%w: owner match names no post or entry", postgres.ErrTitleReviewInvalid)
	}
	entry, err := store.GetCopyrightTitle(ctx, owner.TitleID)
	if err != nil {
		return nil, err
	}
	if !entry.Active {
		return &postgres.OwnerMatchResult{Outcome: postgres.OwnerMatchEntryInactive}, nil
	}
	if policy == OwnerPolicyBlock && (entry.RightsOwner == nil || strings.TrimSpace(*entry.RightsOwner) == "") {
		return nil, fmt.Errorf("%w: the entry names no rights owner to block for", postgres.ErrTitleReviewInvalid)
	}
	authorID, err := store.CopyrightMatchPostAuthor(ctx, postID)
	if err != nil {
		return nil, err
	}
	// The uploader's channel decides ownership; a lookup that fails writes
	// nothing (the caller retries) rather than guessing either way.
	if s.channels != nil {
		ch, err := s.channels.GetChannelByUserID(ctx, authorID)
		if err != nil {
			return nil, fmt.Errorf("owner match: author channel: %w", err)
		}
		if ch != nil {
			for _, id := range entry.OwnerChannelIDs {
				if id == ch.ID {
					return &postgres.OwnerMatchResult{Outcome: postgres.OwnerMatchOwnerUpload}, nil
				}
			}
		}
	}
	effective := policy
	if policy == OwnerPolicyBlock && s.autoBlockDisabled {
		effective = postgres.OwnerPolicyReview // the kill switch: a reviewer decides
	}
	res, err := store.ApplyOwnerMatch(ctx, postgres.OwnerMatchInput{
		PostID: postID, TitleID: entry.ID, MatchedTitle: entry.Title, RightsOwner: entry.RightsOwner,
		Reference: strings.TrimSpace(owner.Reference), Policy: effective,
	})
	if err != nil {
		return nil, err
	}
	switch res.Outcome {
	case postgres.OwnerMatchBlocked, postgres.OwnerMatchHeld, postgres.OwnerMatchTracked:
		s.InvalidatePostBodyCache(ctx, postID)
		s.NudgeOutbox()
	}
	slog.Info("copyright owner match", "post_id", postID, "title_id", entry.ID, "policy", policy,
		"applied", effective, "outcome", res.Outcome)
	return res, nil
}

// RemoveCopyrightOwnerTrack is an admin's removal of one tracked owner match
// (migration 076): the copyright_owner_tracks row goes, the post's
// eligibility is restated (earnings_excluded clears unless another owner
// still tracks it) and the removal is audited with the acting admin.
func (s *Service) RemoveCopyrightOwnerTrack(ctx context.Context, postID, titleID, actorID uuid.UUID, note string) (*postgres.OwnerTrackRemoval, error) {
	store := s.titleReviewStore()
	if store == nil {
		return nil, ErrCopyrightReviewUnavailable
	}
	if postID == uuid.Nil || titleID == uuid.Nil {
		return nil, fmt.Errorf("%w: a track names a post and an entry", postgres.ErrTitleReviewInvalid)
	}
	note = strings.TrimSpace(note)
	if utf8.RuneCountInString(note) > 2000 {
		return nil, fmt.Errorf("%w: note is longer than 2000 characters", postgres.ErrTitleReviewInvalid)
	}
	out, err := store.RemoveCopyrightOwnerTrack(ctx, postID, titleID, actorID, note)
	if err != nil {
		return nil, err
	}
	s.InvalidatePostBodyCache(ctx, postID)
	s.NudgeOutbox()
	slog.Info("copyright owner track removed", "post_id", postID, "title_id", titleID, "still_tracked", out.StillTracked)
	return out, nil
}
