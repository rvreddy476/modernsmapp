package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Record reads: the asset's METADATA, answered to the same audience as its
// bytes (2026-09-29, found on dev right after the anonymous-playback deploy).
//
// THE HOLE THIS CLOSES
//
// GET /v1/media/:id answered 200 to a signed-out caller for an asset whose
// only post was PRIVATE: uploader_id, storage bucket and key, width, height,
// duration, blurhash, the HLS master key and every variant's object key. The
// bytes one route over were correctly refused by the delivery Gate. And
// GET /v1/audio/:audioId/url took no viewer at all and presigned the audio of
// the source video for anyone holding the track id — a piece of protected
// post media, bounded by its TTL and nothing else.
//
// THE RULE
//
// A record is answered to exactly the audience that may have the bytes, plus
// the uploader. In order:
//
//  1. The uploader always sees their own record — including while the asset
//     is still processing or pending moderation, which is what the upload
//     studio polls for, and before any post references it, when no content
//     authority could yet say yes.
//  2. A dating photo's record and an anonymous group attachment's record are
//     the uploader's ALONE (dating_photo.go, anonymous_scope.go): both carry
//     user-keyed storage keys, so nobody else gets the row, whatever a post
//     says. Unchanged from before.
//  3. Everyone else is the content authority's question, asked through the
//     delivery Gate over the same key set GetMediaURL signs — one decision,
//     one implementation. Anonymous is a real viewer here since founder
//     decision 2 (public videos play signed-out): post-service decides.
//
// Fail-closed is the Gate's: an unreachable authority is
// ErrDeliveryUnresolved (503), never a served record. A resolved denial is
// ErrDeliveryDenied, which the handler answers with the SAME body as a
// missing asset, so the route cannot confirm that an id exists.
//
// A service calling with the internal key (post-service's moderation check
// before publish, commerce-service's ownership check before a product or KYC
// document may reference an asset) is not a viewer and asks no audience
// question: the *ForService reads answer it as before. The gateway strips
// the key from client requests and does not stamp it on /v1/media
// (api-gateway routepolicy.StampPolicy), so only in-cluster callers hold it.

// recordReadStore is the store slice these reads need. An interface so the
// handler tests drive every status path against the real decision without
// PostgreSQL.
type recordReadStore interface {
	GetMediaWithVariants(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetTranscodingJobs(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.TranscodingJob, error)
	GetAudioTrack(ctx context.Context, id uuid.UUID) (*postgres.AudioTrack, error)
	// The sound lists and the usage counter (audio_reads.go).
	GetTrendingAudioTracks(ctx context.Context, limit, offset int) ([]postgres.AudioTrack, error)
	SearchAudioTracks(ctx context.Context, query string, limit, offset int) ([]postgres.AudioTrack, error)
	IncrementAudioUsageCount(ctx context.Context, id uuid.UUID) error
}

// recordPresigner signs the one URL a record read still hands out: the
// extracted audio of a source video.
type recordPresigner interface {
	GeneratePresignedGetURL(ctx context.Context, objectKey string, expiry time.Duration) (*url.URL, error)
}

// RecordReads answers the metadata routes for a viewer or for a service.
type RecordReads struct {
	store recordReadStore
	blobs recordPresigner
	gate  *delivery.Gate
}

// NewRecordReads builds the reads over explicit dependencies. The handler
// tests use it with fakes; production goes through Service.RecordReads.
func NewRecordReads(store recordReadStore, blobs recordPresigner, gate *delivery.Gate) *RecordReads {
	return &RecordReads{store: store, blobs: blobs, gate: gate}
}

// RecordReads exposes the record reads over the service's own store, blob
// store and gate. Built per call so a gate wired after New (WithDeliveryGate,
// main.go) is the one used.
func (s *Service) RecordReads() *RecordReads {
	return NewRecordReads(s.pgStore, s.blobStore, s.gate)
}

// authorizeMediaRecord is the whole decision with the store call already
// made, so it runs against a real delivery.Gate and a real asset row without
// a database. It mirrors the bytes path (GetMediaURL) exactly, plus the
// uploader short-circuit the studio needs.
func authorizeMediaRecord(ctx context.Context, gate *delivery.Gate, media *postgres.MediaAsset, viewerID uuid.UUID) error {
	if media == nil {
		return delivery.ErrDeliveryDenied
	}
	if viewerID != uuid.Nil && viewerID == media.UploaderID {
		return nil
	}
	if DatingScopeDenies(media, viewerID) || AnonymousScopeDenies(media, viewerID) {
		return delivery.ErrDeliveryDenied
	}
	if gate == nil {
		return fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return gate.AuthorizeAsset(ctx, viewerID.String(), media.ID.String(), assetDeliveryKeys(media))
}

// loadMedia reads the row. A missing row is a resolved denial (the same
// answer a denied viewer gets); a store fault is unresolved, so an outage
// answers 503 rather than a cacheable 404.
func (r *RecordReads) loadMedia(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: record store not configured", delivery.ErrDeliveryUnresolved)
	}
	media, err := r.store.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, delivery.ErrDeliveryDenied
		}
		return nil, fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if media == nil {
		return nil, delivery.ErrDeliveryDenied
	}
	return media, nil
}

// MediaForViewer is GET /v1/media/:id for viewerID (uuid.Nil is signed-out).
func (r *RecordReads) MediaForViewer(ctx context.Context, viewerID, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	media, err := r.loadMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if err := authorizeMediaRecord(ctx, r.gate, media, viewerID); err != nil {
		return nil, err
	}
	return media, nil
}

// MediaForService is GET /v1/media/:id for a caller holding the internal
// service key. No audience question; the uploader-only scopes still hold,
// exactly as they did for a keyed caller before this change.
func (r *RecordReads) MediaForService(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	media, err := r.loadMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if DatingScopeDenies(media, uuid.Nil) || AnonymousScopeDenies(media, uuid.Nil) {
		return nil, delivery.ErrDeliveryDenied
	}
	return media, nil
}

// StatusForViewer is GET /v1/media/:id/status for viewerID. The status
// carries width, height, duration and — for video — every transcoding job's
// output key, which names the uploader: the same record, the same audience.
func (r *RecordReads) StatusForViewer(ctx context.Context, viewerID, mediaID uuid.UUID) (*MediaStatusResponse, error) {
	media, err := r.MediaForViewer(ctx, viewerID, mediaID)
	if err != nil {
		return nil, err
	}
	return r.status(ctx, media), nil
}

// StatusForService is GET /v1/media/:id/status for a keyed service caller.
func (r *RecordReads) StatusForService(ctx context.Context, mediaID uuid.UUID) (*MediaStatusResponse, error) {
	media, err := r.MediaForService(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return r.status(ctx, media), nil
}

func (r *RecordReads) status(ctx context.Context, media *postgres.MediaAsset) *MediaStatusResponse {
	return mediaStatusResponse(ctx, media, r.store.GetTranscodingJobs)
}

// mediaStatusResponse shapes the status DTO. Shared with Service.GetMediaStatus
// so the two cannot drift.
func mediaStatusResponse(ctx context.Context, media *postgres.MediaAsset,
	jobsOf func(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.TranscodingJob, error)) *MediaStatusResponse {
	resp := &MediaStatusResponse{
		MediaID:          media.ID,
		ProcessingStatus: media.ProcessingStatus,
		ModerationStatus: media.ModerationStatus,
		FileType:         media.FileType,
		Width:            media.Width,
		Height:           media.Height,
		DurationSeconds:  media.DurationSeconds,
		DurationMs:       media.DurationMsValue(),
	}
	// Include transcoding jobs for videos
	if media.FileType == "video" && jobsOf != nil {
		jobs, err := jobsOf(ctx, media.ID)
		if err != nil {
			slog.Warn("Failed to fetch transcoding jobs", "media_id", media.ID, "error", err)
		} else {
			resp.TranscodingJobs = jobs
		}
	}
	return resp
}

// AudioTrackURLForViewer is GET /v1/audio/:audioId/url for viewerID: a
// presigned URL for the track's audio, once the viewer is admitted to the
// SOURCE video's record. The URL lives defaultURLExpiry — the gate's cap.
//
// A track with no source asset has nobody to answer for it, so it is refused:
// every row audio_tracks holds today came from ExtractAudioFromMedia, which
// always records its source.
func (r *RecordReads) AudioTrackURLForViewer(ctx context.Context, viewerID, audioID uuid.UUID) (string, error) {
	track, err := r.AudioTrackForViewer(ctx, viewerID, audioID)
	if err != nil {
		return "", err
	}
	if r.blobs == nil {
		return "", fmt.Errorf("%w: blob store not configured", delivery.ErrDeliveryUnresolved)
	}
	u, err := r.blobs.GeneratePresignedGetURL(ctx, track.AudioKey, defaultURLExpiry)
	if err != nil {
		return "", fmt.Errorf("%w: sign audio url: %v", delivery.ErrDeliveryUnresolved, err)
	}
	return u.String(), nil
}
