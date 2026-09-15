package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Dating plan lane D6 — dating photo safety, dating side.
//
// Attach (POST /v1/dating/photos):
//  1. media-service owner-status: the media must be the caller's (else 404
//     PHOTO_MEDIA_NOT_FOUND) and a ready, moderation-passed image (else 409
//     PHOTO_MEDIA_NOT_READY).
//  2. The per-profile limit and the duplicate check (409).
//  3. media-service prepare: EXIF/GPS stripped with orientation applied, the
//     blurred variant rendered, the asset scoped to dating (media's public
//     reads refuse it to anyone but the owner); faces counted on a primary.
//  4. ClassifyPhoto on the scanner labels media-service stored at upload:
//     explicit → rejected; borderline, unscanned, or a primary with no face
//     → pending_review; otherwise approved. The row is inserted with that
//     decision and the profile status follows through the writer.
//
// WHY A READ AT ATTACH TIME, NOT A CONSUMER
//
// media-service decides an image's moderation synchronously inside
// /v1/media/confirm and publishes no event for images (its outbox carries
// transcode work only). By the time a client can name a media id, the verdict
// and every label are stored, so reading them at attach time is complete and
// needs no new topic. What an event would add — a verdict that changes later
// (a takedown, an asset removed underneath the photo) — is covered by the
// recheck sweeper (photo_recheck.go), which re-reads media-service for photos
// not confirmed within the recheck interval and rejects those whose media is
// gone or no longer passed.
//
// SERVING
//
// dating never hands out a media id or a storage URL for a photo. A photo's
// URL is GET /v1/dating/photos/:id/full or /blurred; that route re-decides the
// viewer's audience on every fetch (PhotoVariantFor) and redirects to a
// short-lived URL media-service signs for that one variant.

var (
	// ErrPhotoMediaNotFound: media-service does not know the media as the
	// caller's (missing or someone else's — never distinguished).
	ErrPhotoMediaNotFound = errors.New("photo media not found")
	// ErrPhotoMediaNotReady: not an image, not processed, not
	// moderation-passed, or (for delivery) not prepared.
	ErrPhotoMediaNotReady = errors.New("photo media is not a ready, moderation-passed image")
	// ErrPhotoMediaUnsupported: media-service cannot decode the image.
	ErrPhotoMediaUnsupported = errors.New("photo media cannot be prepared")
	// ErrPhotoMediaUnavailable: anything that is not an answer. Attach,
	// delete and image delivery refuse (503) rather than proceed unchecked.
	ErrPhotoMediaUnavailable = errors.New("media-service is unavailable for dating photos")
)

// Automated moderation reason codes (dating_photos.moderation_reason).
const (
	PhotoReasonExplicit         = "EXPLICIT_CONTENT"
	PhotoReasonBorderline       = "BORDERLINE_CONTENT"
	PhotoReasonNotScanned       = "NOT_SCANNED"
	PhotoReasonNoFace           = "NO_FACE"
	PhotoReasonMediaUnavailable = "MEDIA_UNAVAILABLE"
)

// PhotoSafetyConfig is the lane D6 bars (http.ResolvePhotoSafetyConfig).
type PhotoSafetyConfig struct {
	// MaxPhotos is the per-profile limit; rejected photos do not count.
	MaxPhotos int
	// ExplicitLabels auto-reject at ExplicitMinConfidence or above. A label
	// matches on its name or its parent category, case-insensitively.
	ExplicitLabels        []string
	ExplicitMinConfidence float64
	// ReviewLabels send the photo to pending_review.
	ReviewLabels        []string
	ReviewMinConfidence float64
	// RequireFaceOnPrimary sends a primary photo with zero faces to review
	// (NO_FACE). An unanswered face count never blocks.
	RequireFaceOnPrimary bool
	// RecheckEnabled starts the media recheck sweeper; RecheckInterval is how
	// stale a photo's last media confirmation may get; RecheckBatch bounds
	// one pass.
	RecheckEnabled  bool
	RecheckInterval time.Duration
	RecheckBatch    int
}

// DefaultExplicitPhotoLabels covers both Rekognition moderation taxonomies
// (v6 "Explicit Nudity", v7 "Explicit").
var DefaultExplicitPhotoLabels = []string{
	"Explicit Nudity", "Explicit", "Explicit Sexual Activity", "Sexual Activity",
	"Graphic Male Nudity", "Graphic Female Nudity", "Exposed Male Genitalia",
	"Exposed Female Genitalia", "Exposed Buttocks or Anus", "Exposed Female Nipple",
	"Illustrated Explicit Nudity",
}

// DefaultReviewPhotoLabels are the borderline categories a moderator decides.
var DefaultReviewPhotoLabels = []string{
	"Suggestive", "Swimwear or Underwear", "Non-Explicit Nudity",
	"Non-Explicit Nudity of Intimate parts and Kissing", "Violence", "Graphic Violence",
	"Visually Disturbing", "Drugs", "Drugs & Tobacco", "Drug Use", "Hate Symbols",
	"Rude Gestures", "Weapons",
}

// DefaultPhotoSafetyConfig is the zero-config bars.
func DefaultPhotoSafetyConfig() PhotoSafetyConfig {
	return PhotoSafetyConfig{
		MaxPhotos:             6,
		ExplicitLabels:        append([]string(nil), DefaultExplicitPhotoLabels...),
		ExplicitMinConfidence: 80,
		ReviewLabels:          append([]string(nil), DefaultReviewPhotoLabels...),
		ReviewMinConfidence:   80,
		RequireFaceOnPrimary:  true,
		RecheckEnabled:        true,
		RecheckInterval:       24 * time.Hour,
		RecheckBatch:          50,
	}
}

// SetPhotoSafetyConfig sets the bars.
func (s *Service) SetPhotoSafetyConfig(cfg PhotoSafetyConfig) {
	s.photoCfg = cfg
	s.photoCfgSet = true
}

// PhotoSafety returns the bars in effect.
func (s *Service) PhotoSafety() PhotoSafetyConfig {
	if !s.photoCfgSet {
		return DefaultPhotoSafetyConfig()
	}
	return s.photoCfg
}

// SetMediaPhotoClient wires media-service's dating photo routes.
func (s *Service) SetMediaPhotoClient(c MediaPhotoClient) {
	s.mediaPhotos = c
}

// MediaPhotoLabel is one scanner finding.
type MediaPhotoLabel struct {
	Name       string  `json:"name"`
	Parent     string  `json:"parent,omitempty"`
	Confidence float64 `json:"confidence"`
}

// MediaPhotoStatus is media-service's owner-status / prepare answer.
type MediaPhotoStatus struct {
	MediaID           uuid.UUID         `json:"media_id"`
	OwnerMatches      bool              `json:"owner_matches"`
	Kind              string            `json:"kind"`
	Status            string            `json:"status"`
	ModerationStatus  string            `json:"moderation_status"`
	ContentType       string            `json:"content_type"`
	Width             *int              `json:"width,omitempty"`
	Height            *int              `json:"height,omitempty"`
	ModerationScanned bool              `json:"moderation_scanned"`
	ModerationScanner string            `json:"moderation_scanner,omitempty"`
	ModerationLabels  []MediaPhotoLabel `json:"moderation_labels"`
	Prepared          bool              `json:"prepared"`
	FaceCount         *int              `json:"face_count,omitempty"`
}

// Usable reports an owned, ready, moderation-passed image.
func (m *MediaPhotoStatus) Usable() bool {
	return m != nil && m.OwnerMatches && m.Kind == "image" && m.Status == "ready" && m.ModerationStatus == "passed"
}

// MediaPhotoClient is media-service's internal dating photo routes. Errors:
// ErrPhotoMediaNotFound, ErrPhotoMediaNotReady, ErrPhotoMediaUnsupported,
// ErrPhotoMediaUnavailable.
type MediaPhotoClient interface {
	PhotoOwnerStatus(ctx context.Context, mediaID, requester uuid.UUID) (*MediaPhotoStatus, error)
	PreparePhoto(ctx context.Context, mediaID, requester uuid.UUID, detectFaces bool) (*MediaPhotoStatus, error)
	PhotoDeliveryURL(ctx context.Context, mediaID, owner uuid.UUID, variant string) (string, error)
	// DeletePhotoMedia removes the owner's asset. Already gone, not theirs,
	// or still referenced elsewhere are all nil: the dating row may go.
	DeletePhotoMedia(ctx context.Context, mediaID, owner uuid.UUID) error
}

// ClassifyPhoto is the automated moderation decision for one photo. Order:
// media not usable → rejected; explicit label → rejected; never scanned →
// review; borderline label → review; primary with zero faces → review;
// otherwise approved.
func ClassifyPhoto(cfg PhotoSafetyConfig, st *MediaPhotoStatus, isPrimary bool) store.PhotoDecision {
	d := store.PhotoDecision{Source: store.PhotoSourceAuto}
	if st != nil {
		d.FaceCount = st.FaceCount
		labels := st.ModerationLabels
		if labels == nil {
			labels = []MediaPhotoLabel{}
		}
		if raw, err := json.Marshal(labels); err == nil {
			d.Labels = raw
		}
	}
	switch {
	case !st.Usable():
		d.Status, d.Reason = store.PhotoStatusRejected, PhotoReasonMediaUnavailable
	case photoLabelHit(st.ModerationLabels, cfg.ExplicitLabels, cfg.ExplicitMinConfidence):
		d.Status, d.Reason = store.PhotoStatusRejected, PhotoReasonExplicit
	case !st.ModerationScanned:
		d.Status, d.Reason = store.PhotoStatusPendingReview, PhotoReasonNotScanned
	case photoLabelHit(st.ModerationLabels, cfg.ReviewLabels, cfg.ReviewMinConfidence):
		d.Status, d.Reason = store.PhotoStatusPendingReview, PhotoReasonBorderline
	case isPrimary && cfg.RequireFaceOnPrimary && st.FaceCount != nil && *st.FaceCount == 0:
		d.Status, d.Reason = store.PhotoStatusPendingReview, PhotoReasonNoFace
	default:
		d.Status = store.PhotoStatusApproved
	}
	return d
}

func photoLabelHit(labels []MediaPhotoLabel, names []string, minConfidence float64) bool {
	for _, l := range labels {
		if l.Confidence < minConfidence {
			continue
		}
		for _, n := range names {
			if strings.EqualFold(n, l.Name) || (l.Parent != "" && strings.EqualFold(n, l.Parent)) {
				return true
			}
		}
	}
	return false
}

// What a viewer may receive of one photo.
const (
	PhotoVariantFull    = "full"
	PhotoVariantBlurred = "blurred"
)

// PhotoViewer is the viewer's relationship to a photo's owner.
type PhotoViewer struct {
	IsOwner              bool
	Matched              bool
	OwnerSparkedViewer   bool
	OwnerBlursUntilMatch bool
}

// PhotoVariantFor is the visibility table. The owner and an open match see
// the full image. Otherwise the owner's blur-until-match setting blurs
// everything; public is full; sparked_only is full once the owner has
// sparked the viewer; match_only (and anything unknown) is blurred.
func PhotoVariantFor(visibility string, v PhotoViewer) string {
	if v.IsOwner || v.Matched {
		return PhotoVariantFull
	}
	if v.OwnerBlursUntilMatch {
		return PhotoVariantBlurred
	}
	switch visibility {
	case "public":
		return PhotoVariantFull
	case "sparked_only":
		if v.OwnerSparkedViewer {
			return PhotoVariantFull
		}
	}
	return PhotoVariantBlurred
}

// PhotoImagePath is the gateway-relative URL of a photo's variant.
func PhotoImagePath(photoID uuid.UUID, variant string) string {
	return "/v1/dating/photos/" + photoID.String() + "/" + variant
}
