package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrNotVideo means the asset has no transcode pipeline to re-run.
var ErrNotVideo = errors.New("asset is not a video")

// ErrBadRotation means the override is not a quarter turn.
var ErrBadRotation = errors.New("rotate_degrees must be a multiple of 90")

// ErrTranscodeInFlight re-exports the store's refusal for the handler.
var ErrTranscodeInFlight = postgres.ErrTranscodeInFlight

// ReprocessResult says what was queued.
type ReprocessResult struct {
	MediaID       uuid.UUID `json:"media_id"`
	EventID       string    `json:"event_id"`
	RotateDegrees int       `json:"rotate_degrees,omitempty"`
	Status        string    `json:"status"`
}

// ReprocessVideo queues a video asset for the transcode worker again —
// thumbnail, MP4 renditions, HLS ladder, measured size and the content scan
// all redone from the original object. Service-to-service only; the
// handler sits behind the internal key.
//
// rotateDegrees (counter-clockwise, quarter turns) is an operator override
// for an original whose pixels are sideways with no rotation metadata: the
// worker stamps that display rotation onto the original (lossless remux)
// before processing, so every path — including a player opening the
// original directly — sees an upright picture. 0 re-runs with the file's
// own metadata, which is what a normal phone recording needs now that
// ProbeVideo reports the display size.
func (s *Service) ReprocessVideo(ctx context.Context, mediaID uuid.UUID, rotateDegrees int) (*ReprocessResult, error) {
	if !processing.ValidRotationOverride(rotateDegrees) {
		return nil, ErrBadRotation
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssetNotFound
		}
		return nil, fmt.Errorf("load media %s: %w", mediaID, err)
	}
	if media == nil {
		return nil, ErrAssetNotFound
	}
	if media.FileType != "video" {
		return nil, ErrNotVideo
	}
	eventID, err := s.pgStore.RequeueTranscode(ctx, media, rotateDegrees)
	if err != nil {
		return nil, err
	}
	return &ReprocessResult{
		MediaID:       mediaID,
		EventID:       eventID,
		RotateDegrees: rotateDegrees,
		Status:        "queued",
	}, nil
}
