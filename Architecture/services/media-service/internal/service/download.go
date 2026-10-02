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

// The one file-download route — GET /v1/media/:id/download (MTube,
// 2026-09-27; owner only since 2026-10-02).
//
// A viewer never receives a file. "Keep a copy" is an offline copy inside
// the app (post-service's /v1/posts/:id/offline, fetched through the
// ordinary /serve routes, which send no attachment disposition), so this
// route — the only one that marks a URL as an attachment — is for the
// creator to get their own upload back, and for a trusted service acting
// for an administrator. posts.allow_download no longer opens it to anyone:
// it now means "viewers may save this offline", and post-service is not
// asked.

// ErrDownloadNotAllowed is a resolved refusal: the caller is not the
// uploader. The handler answers it exactly like an asset that does not
// exist (404 NOT_FOUND), so the route cannot be used to learn which ids are
// real videos.
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

// downloadScopeDenies reports the assets that are never a download, for
// anyone: a dating photo has no audience outside dating-service's own URL,
// and an anonymous attachment's key names its uploader.
func downloadScopeDenies(media *postgres.MediaAsset, viewerID uuid.UUID) bool {
	return DatingScopeDenies(media, viewerID) || media.AccessScope == postgres.AccessScopeAnonymous
}

// downloadVerdict is the whole decision for a viewer: the uploader may
// download their own upload, and nobody else may. uuid.Nil (anonymous) is
// never an owner, not even of an asset whose uploader is the nil id.
//
// There is no authority to ask and so no unresolved answer: every refusal
// is delivery.ErrDeliveryDenied.
func downloadVerdict(media *postgres.MediaAsset, viewerID uuid.UUID) error {
	if media == nil {
		return delivery.ErrDeliveryDenied
	}
	if downloadScopeDenies(media, viewerID) {
		return delivery.ErrDeliveryDenied
	}
	if viewerID != uuid.Nil && viewerID == media.UploaderID {
		return nil
	}
	return delivery.ErrDeliveryDenied
}

// DownloadURL returns a signed attachment URL for the best MP4 of mediaID
// when viewerID uploaded it, or an error:
//
//	ErrAssetNotFound                    no such asset               → 404
//	ErrDownloadNotVideo                 not a video                 → 404
//	ErrDownloadNotAllowed               not the uploader            → 404
//	delivery.ErrDeliveryUnresolved      the record or the signer
//	                                    could not answer            → 503
func (s *Service) DownloadURL(ctx context.Context, viewerID, mediaID uuid.UUID) (string, error) {
	media, err := s.downloadAsset(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if err := downloadVerdict(media, viewerID); err != nil {
		return "", ErrDownloadNotAllowed
	}
	return s.signDownload(media)
}

// DownloadURLForService is DownloadURL for a caller holding the internal
// service key and acting for an administrator (the handler establishes
// that; this does not). No viewer is involved, so there is no owner test;
// the assets that are never a download stay refused.
func (s *Service) DownloadURLForService(ctx context.Context, mediaID uuid.UUID) (string, error) {
	media, err := s.downloadAsset(ctx, mediaID)
	if err != nil {
		return "", err
	}
	if downloadScopeDenies(media, uuid.Nil) {
		return "", ErrDownloadNotAllowed
	}
	return s.signDownload(media)
}

func (s *Service) downloadAsset(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrAssetNotFound
		}
		return nil, fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if media.FileType != "video" {
		return nil, ErrDownloadNotVideo
	}
	return media, nil
}

func (s *Service) signDownload(media *postgres.MediaAsset) (string, error) {
	if s.gate == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	key, _ := pickDownloadObject(media)
	return s.gate.SignDownload(key, media.ID.String()+".mp4")
}
