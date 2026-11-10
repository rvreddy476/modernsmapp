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

// RenditionSpec defines a rendition to be created for a media asset.
type RenditionSpec struct {
	RenditionType string
	Quality       string
	MaxRetries    int
}

// DefaultVideoRenditions are the renditions created for every video upload.
var DefaultVideoRenditions = []RenditionSpec{
	{"thumbnail", "thumb_150", 3},
	{"thumbnail", "thumb_300", 3},
	{"video", "360p", 3},
	{"video", "480p", 3},
	{"video", "720p", 3},
	{"video", "1080p", 3},
	{"hls_variant", "master", 3},
	{"preview_gif", "preview", 2},
	{"audio", "audio_aac", 3},
}

// DefaultReelRenditions are the renditions created for reel (short-form) videos.
// No 1080p/4k — capped at 720p for faster processing.
var DefaultReelRenditions = []RenditionSpec{
	{"thumbnail", "thumb_150", 3},
	{"thumbnail", "thumb_300", 3},
	{"video", "360p", 3},
	{"video", "480p", 3},
	{"video", "720p", 3},
	{"hls_variant", "master", 3},
	{"preview_gif", "preview", 2},
	{"audio", "audio_aac", 3},
}

// CreateRenditionsForMedia initializes rendition tracking records for a media asset.
// Returns the list of created rendition records.
func (s *Service) CreateRenditionsForMedia(ctx context.Context, mediaID uuid.UUID, isReel bool) ([]postgres.MediaRendition, error) {
	specs := DefaultVideoRenditions
	if isReel {
		specs = DefaultReelRenditions
	}

	// Check source resolution to skip unnecessary renditions
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, fmt.Errorf("get media: %w", err)
	}

	var created []postgres.MediaRendition
	for _, spec := range specs {
		// Skip video renditions higher than source resolution
		if spec.RenditionType == "video" && media.Height != nil {
			switch spec.Quality {
			case "480p":
				if *media.Height < 480 {
					continue
				}
			case "720p":
				if *media.Height < 720 {
					continue
				}
			case "1080p":
				if *media.Height < 1080 {
					continue
				}
			}
		}

		r := &postgres.MediaRendition{
			MediaID:       mediaID,
			RenditionType: spec.RenditionType,
			Quality:       spec.Quality,
			Status:        "pending",
			MaxRetries:    spec.MaxRetries,
		}
		if err := s.pgStore.CreateRendition(ctx, r); err != nil {
			return nil, fmt.Errorf("create rendition %s/%s: %w", spec.RenditionType, spec.Quality, err)
		}
		created = append(created, *r)
	}

	return created, nil
}

// GetRenditions returns all rendition records for a media asset.
func (s *Service) GetRenditions(ctx context.Context, mediaID uuid.UUID) ([]postgres.MediaRendition, error) {
	return s.pgStore.GetRenditionsByMedia(ctx, mediaID)
}

// RenditionStatusResponse summarizes the rendition processing state for a media asset.
type RenditionStatusResponse struct {
	MediaID    uuid.UUID                 `json:"media_id"`
	AllReady   bool                      `json:"all_ready"`
	Renditions []postgres.MediaRendition `json:"renditions"`
}

// GetRenditionStatus returns the full rendition status for a media asset.
func (s *Service) GetRenditionStatus(ctx context.Context, mediaID uuid.UUID) (*RenditionStatusResponse, error) {
	renditions, err := s.pgStore.GetRenditionsByMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}

	allReady, err := s.pgStore.AreAllRenditionsReady(ctx, mediaID)
	if err != nil {
		return nil, err
	}

	return &RenditionStatusResponse{
		MediaID:    mediaID,
		AllReady:   allReady,
		Renditions: renditions,
	}, nil
}

// ViewerRenditionStatus is GET /v1/media/:mediaId/renditions: the rendition
// status of an asset, answered to the asset's audience.
//
// It goes through the same delivery gate as /url and the HLS routes
// (authorizeCaptionRead -> Gate.AuthorizeAsset), after the local rules
// (anonymous scope, dating scope, documents, moderation hold). A denial is
// delivery.ErrDeliveryDenied, which handlers answer as not-found. Rows carry
// object keys (which name the uploader) and raw processing errors, so only
// the uploader gets those; everyone else gets status and shape alone.
func (s *Service) ViewerRenditionStatus(ctx context.Context, viewerID, mediaID uuid.UUID) (*RenditionStatusResponse, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, delivery.ErrDeliveryDenied
		}
		return nil, fmt.Errorf("%w: load media: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if AnonymousScopeDenies(media, viewerID) {
		return nil, delivery.ErrDeliveryDenied
	}
	if err := authorizeCaptionRead(ctx, s.gate, media, viewerID); err != nil {
		return nil, err
	}
	resp, err := s.GetRenditionStatus(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return RedactRenditionStatus(resp, viewerID != uuid.Nil && viewerID == media.UploaderID), nil
}

// RedactRenditionStatus returns resp as owner may see it: the uploader gets
// it whole; anyone else gets a copy without object_key and error_message.
func RedactRenditionStatus(resp *RenditionStatusResponse, owner bool) *RenditionStatusResponse {
	if resp == nil || owner {
		return resp
	}
	out := *resp
	out.Renditions = make([]postgres.MediaRendition, len(resp.Renditions))
	for i, r := range resp.Renditions {
		r.ObjectKey = nil
		r.ErrorMessage = nil
		out.Renditions[i] = r
	}
	return &out
}
