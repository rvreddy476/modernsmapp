package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The writers of post-service's non-FK media columns, each under the claim
// protocol (media_claims.go). One function per holder row, so the protocol is
// in one place and each route only says what the row will hold.

// writeCreatedPostClaimed is writeCreatedPost with the post's cover claimed
// first (kind post_cover, ref = the new post's id). A post without a cover
// claims nothing and takes no lock.
func (s *Service) writeCreatedPostClaimed(ctx context.Context, p *postgres.Post, eventType string, payload interface{}, idem *postgres.CreateIdempotency) error {
	write := func() error { return s.writeCreatedPost(ctx, p, eventType, payload, idem) }
	if p.CoverMediaID == nil || *p.CoverMediaID == uuid.Nil {
		return write()
	}
	return s.setPostCoverClaimed(ctx, p.ID, *p.CoverMediaID, write)
}

// setPostCoverClaimed runs write, which makes post postID's cover `cover`,
// under the protocol: the cover is claimed before, the one it replaces is
// released after. A claim that finds the asset gone is a refusal of the
// COVER (CoverMediaError), like the authority check before it.
func (s *Service) setPostCoverClaimed(ctx context.Context, postID, cover uuid.UUID, write func() error) error {
	err := s.holderWrite(ctx, postgres.HolderPostCover, postID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return []uuid.UUID{cover}, nil },
		write)
	var gone *ClaimMediaGoneError
	if errors.As(err, &gone) {
		return &CoverMediaError{Err: err}
	}
	return err
}

// createReelDraftClaimed writes a new reel draft with its source asset
// claimed first (kind reel_draft, ref = the draft id). The asset must be the
// author's own: a claim pins it against its owner's delete.
func (s *Service) createReelDraftClaimed(ctx context.Context, draft *postgres.ReelDraft) error {
	write := func() error { return s.pgStore.CreateDraft(ctx, draft) }
	want := holdingOf(draft.MediaID, draft.CoverMediaID)
	if len(want) == 0 {
		return write()
	}
	if err := s.verifyOwnMedia(ctx, draft.AuthorID, want...); err != nil {
		return err
	}
	return s.holderWrite(ctx, postgres.HolderReelDraft, draft.ID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write)
}

// updateReelDraftCoverClaimed runs write, which sets reel draft draftID's
// cover, under the protocol. What the draft will hold is decided from the
// row re-read under the holder lock: its source asset plus the new cover. A
// draft that is not the author's, or will never be shown again (deleted,
// published), changes no claim: the write does not touch the first, and the
// second holds nothing.
func (s *Service) updateReelDraftCoverClaimed(ctx context.Context, draftID, authorID, cover uuid.UUID, write func() error) error {
	if err := s.verifyOwnMedia(ctx, authorID, cover); err != nil {
		return err
	}
	return s.holderWrite(ctx, postgres.HolderReelDraft, draftID,
		func(prev postgres.MediaHolding) ([]uuid.UUID, error) {
			if !prev.Live {
				return nil, nil
			}
			d, err := s.pgStore.GetDraft(ctx, draftID)
			if err != nil {
				return nil, fmt.Errorf("update draft: %w", err)
			}
			if d.AuthorID != authorID {
				return prev.MediaIDs, nil
			}
			return holdingOf(d.MediaID, &cover), nil
		},
		write)
}

// markReelDraftPublished marks the draft published and releases its claims:
// the post now holds the media (post_media's foreign key, the post's own
// cover claim), and a published draft is never shown again.
func (s *Service) markReelDraftPublished(ctx context.Context, draftID, postID uuid.UUID) error {
	if err := s.pgStore.MarkDraftPublished(ctx, draftID, postID); err != nil {
		return err
	}
	s.settleMediaHolderBestEffort(ctx, postgres.HolderReelDraft, draftID)
	return nil
}

// createEventClaimed writes a new event with its cover claimed first (kind
// event_cover, ref = the event id, minted here so the claim can name it).
func (s *Service) createEventClaimed(ctx context.Context, e *postgres.Event) (*postgres.Event, error) {
	if e.ID == uuid.Nil {
		e.ID = uuid.New()
	}
	want := optionalID(e.CoverMediaID)
	if len(want) == 0 {
		return s.pgStore.CreateEvent(ctx, e)
	}
	if err := s.verifyOwnMedia(ctx, e.CreatorID, want...); err != nil {
		return nil, err
	}
	var out *postgres.Event
	err := s.holderWrite(ctx, postgres.HolderEventCover, e.ID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		func() error {
			var werr error
			out, werr = s.pgStore.CreateEvent(ctx, e)
			return werr
		})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// holdingOf is the set of the optional ids given, in order.
func holdingOf(ids ...*uuid.UUID) []uuid.UUID {
	var all []uuid.UUID
	for _, id := range ids {
		all = append(all, optionalID(id)...)
	}
	out, _ := mediaDiff(nil, all)
	return out
}
