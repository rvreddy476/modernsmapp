package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// MTube download (2026-09-27) — GET /v1/media/:id/download.

// ErrDownloadNotAllowed is a resolved refusal: the viewer may not save this
// video. The handler answers 403 DOWNLOAD_NOT_ALLOWED. It is deliberately
// NOT the 404 the playback path gives a denial: the client already knows
// the video exists (it is watching it) and needs to tell "the author turned
// downloads off" apart from "gone".
var ErrDownloadNotAllowed = errors.New("download not allowed")

// ErrDownloadNotVideo means the asset has no MP4 to hand out.
var ErrDownloadNotVideo = errors.New("asset is not a video")

// downloadVariantOrder is the preference order for the file a download
// gets: the best broadly-playable MP4 rendition first, the author's
// original only when the pipeline produced neither.
var downloadVariantOrder = []string{"720p", "480p"}

// pickDownloadObject returns the object key to hand out for a download and
// the variant name it came from ("original" when no rendition applies).
func pickDownloadObject(media *postgres.MediaAsset) (key, variant string) {
	for _, want := range downloadVariantOrder {
		for _, v := range media.Variants {
			if v.Name == want && v.ObjectKey != "" {
				return v.ObjectKey, v.Name
			}
		}
	}
	return media.StorageKey, "original"
}

// downloadVerdict is the store-free half of the decision: the owner may
// always download their own upload; everyone else needs the download
// authority's yes. uuid.Nil (anonymous) is never an owner.
//
// Errors are the delivery package's: a resolved denial becomes
// ErrDownloadNotAllowed at the caller; an unresolved authority stays a
// retryable delivery.ErrDeliveryUnresolved.
func downloadVerdict(ctx context.Context, gate *delivery.Gate, media *postgres.MediaAsset, viewerID uuid.UUID) error {
	if media == nil {
		return delivery.ErrDeliveryDenied
	}
	if DatingScopeDenies(media, viewerID) || media.AccessScope == postgres.AccessScopeAnonymous {
		// A dating photo and an anonymous attachment are never downloads:
		// the first has no audience outside dating-service's own URL, the
		// second's key names the uploader.
		return delivery.ErrDeliveryDenied
	}
	if viewerID != uuid.Nil && viewerID == media.UploaderID {
		return nil
	}
	if gate == nil {
		return fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return gate.AuthorizeDownload(ctx, viewerID.String(), media.ID.String())
}

// DownloadURL returns a signed attachment URL for the best MP4 of mediaID,
// or an error:
//
//	pgx.ErrNoRows / ErrAssetNotFound   no such asset               → 404
//	ErrDownloadNotVideo                 not a video                 → 404
//	ErrDownloadNotAllowed               owner-or-authority said no  → 403
//	delivery.ErrDeliveryUnresolved      authority unreachable       → 503
func (s *Service) DownloadURL(ctx context.Context, viewerID, mediaID uuid.UUID) (string, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrAssetNotFound
		}
		return "", fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if media.FileType != "video" {
		return "", ErrDownloadNotVideo
	}
	if err := downloadVerdict(ctx, s.gate, media, viewerID); err != nil {
		if errors.Is(err, delivery.ErrDeliveryDenied) {
			return "", ErrDownloadNotAllowed
		}
		return "", err
	}
	if s.gate == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	key, _ := pickDownloadObject(media)
	return s.gate.SignDownload(key, mediaID.String()+".mp4")
}
