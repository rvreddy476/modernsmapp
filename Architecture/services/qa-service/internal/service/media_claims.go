package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/qa-service/internal/claimsync"
	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/atpost/qa-service/internal/store"
	"github.com/google/uuid"
)

// Question media authority and claims.
//
// question_media.media_id carries no foreign key into media-service. Before a
// question names an asset, the gate checks it is the author's own finished
// upload and media-service records a claim on it, so the uploader cannot
// delete it while the question holds it; a deleted or hidden question's or
// answer's media is released afterwards (ReleaseRemovedMedia). The rules are
// internal/mediaclaims; the backfill and the repair are cmd/media-claims.
//
// answer_media.media_id has no writer in this service (no route attaches
// media to an answer); cmd/media-claims still claims what legacy rows hold.
//
// Fail closed: without a media-service client, a question that carries media
// is refused with 503 MEDIA_UNAVAILABLE. Questions without media never call
// out.

// WithMedia installs the attach gate and claim client.
func (s *Service) WithMedia(g *mediaclaims.Gate) *Service {
	s.media = g
	return s
}

// parseQuestionMedia reads the request's media ids. A malformed id is refused
// rather than skipped: skipping it would store a question without media the
// author believes is attached. Duplicates collapse.
func parseQuestionMedia(raw []string) ([]uuid.UUID, error) {
	ids := make([]uuid.UUID, 0, len(raw))
	for _, r := range raw {
		id, err := uuid.Parse(r)
		if err != nil {
			return nil, fmt.Errorf("invalid: media id %q is not a media id", r)
		}
		ids = append(ids, id)
	}
	return mediaclaims.Unique(ids), nil
}

func mediaIDStrings(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

// questionMediaChanges is what creating a question does to its media.
func questionMediaChanges(questionID uuid.UUID, ids []uuid.UUID) []mediaclaims.Change {
	return []mediaclaims.Change{{Kind: mediaclient.KindQuestionMedia, Ref: questionID, Add: mediaclaims.Unique(ids)}}
}

// MediaHolders is what releasing a removed row's media needs from the store
// (*store.Store): which of the removed rows hold media, and the writers' lock
// and re-read the reconcile tool uses.
type MediaHolders interface {
	MediaHoldersAmong(ctx context.Context, questionIDs, answerIDs []uuid.UUID) ([]store.MediaHolder, error)
	LockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error)
	MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (claimsync.Row, error)
}

// WithMediaHolders replaces the store ReleaseRemovedMedia reads (tests).
func (s *Service) WithMediaHolders(h MediaHolders) *Service {
	s.holders = h
	return s
}

func (s *Service) mediaHolders() MediaHolders {
	if s.holders != nil {
		return s.holders
	}
	if s.store != nil {
		return s.store
	}
	return nil
}

// ReleaseRemovedMedia releases the media claims of questions and answers a
// removal has just deleted: a moderator's or an admin's hide, an account
// erasure, an author's delete. An answer under a removed question dies with
// it and is released too. Call it AFTER the removal has committed.
//
// Best effort, never a reason to fail the removal: every failure is logged
// and left to the reconcile tool (cmd/media-claims -apply), which releases
// what dead rows held; the rows are soft-deleted, so nothing is lost. The
// first failed release stops the pass (media-service is not answering: one
// timeout, not one per row) and the log names how many rows are left.
//
// Each row is locked with the writers' own lock and re-read first, and a row
// that is still LIVE is never released: a removal whose UPDATE did not land
// must not leave a shown row's media unclaimed.
func (s *Service) ReleaseRemovedMedia(ctx context.Context, questionIDs, answerIDs []uuid.UUID) {
	if len(questionIDs) == 0 && len(answerIDs) == 0 {
		return
	}
	hs := s.mediaHolders()
	if hs == nil {
		return // no rows here (unit tests of other paths)
	}
	ctx = context.WithoutCancel(ctx) // the removal committed; a hang-up must not skip this
	holders, err := hs.MediaHoldersAmong(ctx, questionIDs, answerIDs)
	if err != nil {
		slog.Warn("qa-service: removed content's media claims not released; run the reconcile tool (media-claims -apply)",
			"questions", len(questionIDs), "answers", len(answerIDs), "error", err)
		return
	}
	for i, h := range holders {
		if !s.releaseDeadHolder(ctx, hs, h) {
			slog.Warn("qa-service: removed content's media claims left for the reconcile tool (media-claims -apply)",
				"kind", h.Kind, "ref", h.Ref, "not_released", len(holders)-i)
			return
		}
	}
}

// releaseDeadHolder releases everything one dead row holds, under its lock.
// It reports false only when media-service (or the store) failed; a row that
// is live or holds nothing is skipped and reported true.
func (s *Service) releaseDeadHolder(ctx context.Context, hs MediaHolders, h store.MediaHolder) bool {
	unlock, err := hs.LockMediaHolder(ctx, h.Ref)
	if err != nil {
		return false
	}
	defer unlock()
	row, err := hs.MediaClaimRowFor(ctx, h.Kind, h.Ref)
	if err != nil {
		return false
	}
	if row.Live {
		slog.Warn("qa-service: removal did not take; its media stays claimed", "kind", h.Kind, "ref", h.Ref)
		return true
	}
	if len(row.MediaIDs) == 0 {
		return true // held nothing
	}
	return s.media.Release(ctx, h.Kind, h.Ref, nil)
}

// ReleaseHiddenMedia is ReleaseRemovedMedia for one moderation target that a
// hide has just removed, on either moderation path (the moderator routes and
// the admin-service token routes). Comments hold no media.
func (s *Service) ReleaseHiddenMedia(ctx context.Context, targetType string, id uuid.UUID) {
	switch targetType {
	case "question":
		s.ReleaseRemovedMedia(ctx, []uuid.UUID{id}, nil)
	case "answer":
		s.ReleaseRemovedMedia(ctx, nil, []uuid.UUID{id})
	}
}

// lockMediaHolder takes the store's per-row media lock. A Service without a
// store (unit tests of the pure protocol) has no rows to race over.
func (s *Service) lockMediaHolder(ctx context.Context, ref uuid.UUID) (func(), error) {
	if s.store == nil {
		return func() {}, nil
	}
	unlock, err := s.store.LockMediaHolder(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("failed to lock media holder: %w", err)
	}
	return unlock, nil
}
