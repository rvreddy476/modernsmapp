package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/qa-service/internal/mediaclaims"
	"github.com/atpost/qa-service/internal/mediaclient"
	"github.com/google/uuid"
)

// Question media authority and claims.
//
// question_media.media_id carries no foreign key into media-service. Before a
// question names an asset, the gate checks it is the author's own finished
// upload and media-service records a claim on it, so the uploader cannot
// delete it while the question holds it; a deleted question's media is
// released afterwards. The rules are internal/mediaclaims; the backfill is
// cmd/media-claims.
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

// releaseQuestionMedia releases what a deleted question held. Best effort;
// whatever is left is the reconcile tool's.
func (s *Service) releaseQuestionMedia(ctx context.Context, questionID uuid.UUID) {
	if s.store != nil {
		row, err := s.store.MediaClaimRowFor(context.WithoutCancel(ctx), mediaclient.KindQuestionMedia, questionID)
		if err != nil {
			slog.Warn("qa-service: deleted question's media claims not released; run the reconcile tool",
				"question_id", questionID, "error", err)
			return
		}
		if len(row.MediaIDs) == 0 {
			return // held nothing
		}
	}
	s.media.Release(ctx, mediaclient.KindQuestionMedia, questionID, nil)
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
