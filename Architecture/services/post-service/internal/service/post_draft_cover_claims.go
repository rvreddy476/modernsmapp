package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Composer draft covers under the claim protocol (media_claims.go).
//
// A composer draft (post_drafts) keeps its cover only inside its JSONB
// payload, as "cover_media_id". post_draft_media has a real foreign key for
// media_ids, but nothing guarded the cover: its owner could delete an asset a
// saved draft still showed, and the draft then published with a broken cover.
// Each writer below claims it (kind post_draft_cover, ref = the draft id).
//
//	save (POST)               claim the cover, then insert
//	update / autosave (PATCH) claim the new cover, write, release the old
//	schedule (publish + at)   same as an update; the payload is unchanged
//	delete                    soft-delete, then release (the row is dead)
//	publish (now and worker)  CreatePost claims the POST's cover under
//	                          post_cover (same id, other kind), then the draft
//	                          is marked published and its own claim released
//
// The two kinds never release each other: a claim is keyed by (claimant,
// kind, ref) in media-service, the holder lock is namespaced by kind, and the
// draft's release runs only after CreatePost's claim, so the asset is held by
// one of the two at every moment. A blocked or 'publishing' draft is live and
// keeps its claim.

// postDraftRows is the post_drafts writes the claimed writers make
// (*postgres.Store).
type postDraftRows interface {
	CreatePostDraft(ctx context.Context, d *postgres.PostDraft, mediaIDs []uuid.UUID) error
	UpdatePostDraft(ctx context.Context, id, authorID uuid.UUID, postType string,
		payload json.RawMessage, scheduleAt *time.Time, mediaIDs []uuid.UUID) (bool, error)
	GetPostDraft(ctx context.Context, id, authorID uuid.UUID) (*postgres.PostDraft, error)
	DeletePostDraft(ctx context.Context, id, authorID uuid.UUID) (bool, error)
	MarkPostDraftPublished(ctx context.Context, id, postID uuid.UUID, token *uuid.UUID) error
}

func (s *Service) draftRows() postDraftRows {
	if s.postDraftRows != nil {
		return s.postDraftRows
	}
	return s.pgStore
}

// payloadDraftCover is the cover a payload names, parsed exactly as the
// holder re-read parses the stored row (postgres.ParseDraftCoverID).
func payloadDraftCover(p *PostDraftPayload) *uuid.UUID {
	if p == nil {
		return nil
	}
	return postgres.ParseDraftCoverID(p.CoverMediaID)
}

// verifyDraftCover is a draft cover's authority: the post cover rule
// (checkCoverAuthority) — the author's own upload, confirmed, not refused,
// an image. A draft is published through CreatePost, which asks the same
// again; asking at save keeps a stranger's asset from ever being pinned.
func (s *Service) verifyDraftCover(ctx context.Context, authorID, coverID uuid.UUID) error {
	var reader mediaOwnershipReader = s.ownMedia
	if reader == nil && s.pgStore != nil {
		reader = s.pgStore
	}
	if reader == nil {
		return fmt.Errorf("verify draft cover: %w", ErrAuthoringStoreUnavailable)
	}
	ownership, err := reader.BatchGetMediaOwnership(ctx, []uuid.UUID{coverID})
	if err != nil {
		return fmt.Errorf("verify draft cover: %w", err) // fail closed
	}
	m, ok := ownership[coverID]
	if err := checkCoverAuthority(coverID, authorID, m, ok); err != nil {
		return &CoverMediaError{Err: err}
	}
	return nil
}

// draftCoverWrite is holderWrite for one draft, with a cover found gone at
// the claim answered as a refusal of the COVER (like setPostCoverClaimed).
func (s *Service) draftCoverWrite(ctx context.Context, draftID uuid.UUID,
	next func(prev postgres.MediaHolding) ([]uuid.UUID, error), write func() error) error {
	err := s.holderWrite(ctx, postgres.HolderPostDraftCover, draftID, next, write)
	var gone *ClaimMediaGoneError
	if errors.As(err, &gone) {
		return &CoverMediaError{Err: err}
	}
	return err
}

// createPostDraftClaimed inserts d with its cover claimed first. The draft
// id is minted here so the claim can name it. No cover: no lock, no claim.
func (s *Service) createPostDraftClaimed(ctx context.Context, d *postgres.PostDraft, mediaIDs []uuid.UUID, cover *uuid.UUID) error {
	write := func() error { return s.draftRows().CreatePostDraft(ctx, d, mediaIDs) }
	want := optionalID(cover)
	if len(want) == 0 {
		return write()
	}
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	if err := s.verifyDraftCover(ctx, d.AuthorID, *cover); err != nil {
		return err
	}
	return s.draftCoverWrite(ctx, d.ID,
		func(postgres.MediaHolding) ([]uuid.UUID, error) { return want, nil },
		write)
}

// updatePostDraftClaimed runs write, which replaces draft id's payload with
// one naming cover (nil: none), under the protocol. What the draft will hold
// is decided from the row re-read under the lock:
//
//   - a dead or missing draft holds nothing, and the write cannot land;
//   - a draft that is not the caller's, or is mid-publish, is not changed by
//     the write (it is owner- and state-scoped), so it keeps what it holds;
//   - otherwise it will hold cover. A cover it does not already hold is
//     verified before it is claimed; one it holds is not asked again, so an
//     autosave never fails over a cover it already had.
func (s *Service) updatePostDraftClaimed(ctx context.Context, id, authorID uuid.UUID, cover *uuid.UUID, write func() error) error {
	return s.draftCoverWrite(ctx, id, func(prev postgres.MediaHolding) ([]uuid.UUID, error) {
		if !prev.Live {
			return nil, nil
		}
		d, err := s.draftRows().GetPostDraft(ctx, id, authorID)
		if err != nil {
			return nil, fmt.Errorf("update draft: %w", err)
		}
		if d == nil || (d.Status != "draft" && d.Status != "blocked") {
			return prev.MediaIDs, nil
		}
		want := optionalID(cover)
		added, _ := mediaDiff(prev.MediaIDs, want)
		for _, m := range added {
			if err := s.verifyDraftCover(ctx, authorID, m); err != nil {
				return nil, err
			}
		}
		return want, nil
	}, write)
}

// finishPostDraftPublish marks the draft published and then releases the
// draft's own cover claim. The post already holds its cover under
// post_cover (CreatePost claimed it before the post was written), a
// different (kind, ref), so the release cannot touch it.
func (s *Service) finishPostDraftPublish(ctx context.Context, draftID, postID uuid.UUID, token *uuid.UUID) error {
	if err := s.draftRows().MarkPostDraftPublished(ctx, draftID, postID, token); err != nil {
		return err
	}
	s.settleMediaHolderBestEffort(ctx, postgres.HolderPostDraftCover, draftID)
	return nil
}
