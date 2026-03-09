package service

import (
	"context"
	"fmt"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
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
	MediaID    uuid.UUID                `json:"media_id"`
	AllReady   bool                     `json:"all_ready"`
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
