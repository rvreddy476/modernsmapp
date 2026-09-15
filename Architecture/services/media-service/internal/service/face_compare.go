package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dating plan lane D5 — face comparison for selfie verification.
//
// FaceCompareService decides which bytes may be compared and never lets any
// of them out: the response carries a similarity, two face counts, a match
// flag, a reason code and the provider name. No embedding, image bytes or
// storage key is returned or logged.

// FaceMediaStore is the slice of the asset store face comparison reads.
type FaceMediaStore interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaVariant, error)
}

// FaceBlobReader reads an object's bytes.
type FaceBlobReader interface {
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
}

var (
	// ErrFaceMediaNotFound covers a missing asset AND an asset the requester
	// may not compare (not theirs, not an image, not ready, not
	// moderation-passed). One error so existence is never revealed.
	ErrFaceMediaNotFound = errors.New("face compare: media not found")
	// ErrFaceSameMedia: comparing an asset with itself proves nothing.
	ErrFaceSameMedia = errors.New("face compare: source and target must be different media")
	// ErrFaceImageUnsupported: the stored image cannot be sent to the
	// provider (too large, empty).
	ErrFaceImageUnsupported = errors.New("face compare: image cannot be compared")
	// ErrFaceCompareInvalid: nil ids.
	ErrFaceCompareInvalid = errors.New("face compare: invalid request")
)

// FaceCompareVariant is the rendition compared when present: a 1080px JPEG,
// always under the provider's byte limit and in a format it accepts.
const FaceCompareVariant = "medium_1080"

// FaceCompareOutcome is the route's response body.
type FaceCompareOutcome struct {
	Similarity      float64 `json:"similarity"`
	FaceCountSource int     `json:"face_count_source"`
	FaceCountTarget int     `json:"face_count_target"`
	Match           bool    `json:"match"`
	Reason          string  `json:"reason,omitempty"`
	Provider        string  `json:"provider"`
}

// FaceCompareService compares the faces in two assets owned by one user.
type FaceCompareService struct {
	media     FaceMediaStore
	blobs     FaceBlobReader
	comparer  processing.FaceComparer
	threshold float64
	// Blink liveness (liveness.go). Nil analyzer = liveness route off.
	analyzer    processing.LivenessAnalyzer
	livenessCfg processing.LivenessConfig
}

// NewFaceCompareService wires the comparison. threshold (1-100) only sets
// the Match flag.
func NewFaceCompareService(media FaceMediaStore, blobs FaceBlobReader, comparer processing.FaceComparer, threshold float64) *FaceCompareService {
	if threshold <= 0 || threshold > 100 {
		threshold = processing.DefaultFaceMatchThreshold
	}
	return &FaceCompareService{media: media, blobs: blobs, comparer: comparer, threshold: threshold}
}

// Compare checks both assets belong to requester and are ready images, then
// asks the provider. See the error variables for the refusal cases.
func (s *FaceCompareService) Compare(ctx context.Context, requester, source, target uuid.UUID) (*FaceCompareOutcome, error) {
	if requester == uuid.Nil || source == uuid.Nil || target == uuid.Nil {
		return nil, ErrFaceCompareInvalid
	}
	if source == target {
		return nil, ErrFaceSameMedia
	}
	if s.comparer == nil {
		return nil, fmt.Errorf("%w: no provider configured", processing.ErrFaceCompareUnavailable)
	}
	src, err := s.eligibleMedia(ctx, requester, source, "image")
	if err != nil {
		return nil, err
	}
	tgt, err := s.eligibleMedia(ctx, requester, target, "image")
	if err != nil {
		return nil, err
	}
	srcBytes, err := s.readForCompare(ctx, src)
	if err != nil {
		return nil, err
	}
	tgtBytes, err := s.readForCompare(ctx, tgt)
	if err != nil {
		return nil, err
	}

	res, err := s.comparer.CompareFaces(ctx, srcBytes, tgtBytes)
	if err != nil {
		slog.Warn("media: face compare unavailable",
			"requester_user_id", requester, "source_media_id", source, "target_media_id", target,
			"provider", s.comparer.Name(), "error", err)
		if errors.Is(err, processing.ErrFaceCompareUnavailable) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", processing.ErrFaceCompareUnavailable, err)
	}

	out := &FaceCompareOutcome{
		Similarity:      math.Round(res.Similarity*100) / 100,
		FaceCountSource: res.FaceCountSource,
		FaceCountTarget: res.FaceCountTarget,
		Reason:          res.Reason,
		Provider:        s.comparer.Name(),
	}
	if out.Reason != "" {
		out.Similarity = 0
	}
	out.Match = out.Reason == "" && out.FaceCountSource == 1 && out.FaceCountTarget == 1 && out.Similarity >= s.threshold
	slog.Info("media: face compare",
		"requester_user_id", requester, "source_media_id", source, "target_media_id", target,
		"provider", out.Provider, "similarity", out.Similarity, "match", out.Match,
		"face_count_source", out.FaceCountSource, "face_count_target", out.FaceCountTarget, "reason", out.Reason)
	return out, nil
}

// eligibleMedia loads an asset and applies the ownership + readiness rule:
// owned by requester, of fileType, processing ready, moderation passed.
func (s *FaceCompareService) eligibleMedia(ctx context.Context, requester, id uuid.UUID, fileType string) (*postgres.MediaAsset, error) {
	m, err := s.media.GetMedia(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrFaceMediaNotFound
		}
		return nil, fmt.Errorf("face compare: load media: %w", err)
	}
	if m == nil ||
		m.UploaderID != requester ||
		m.FileType != fileType ||
		m.ProcessingStatus != "ready" ||
		m.ModerationStatus != "passed" {
		return nil, ErrFaceMediaNotFound
	}
	return m, nil
}

// readForCompare returns the bytes to compare: the medium_1080 rendition when
// one exists, else the original (an original smaller than the rendition size
// gets no rendition).
func (s *FaceCompareService) readForCompare(ctx context.Context, m *postgres.MediaAsset) ([]byte, error) {
	key := m.StorageKey
	variants, err := s.media.GetVariants(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("face compare: load renditions: %w", err)
	}
	for _, v := range variants {
		if v.Name == FaceCompareVariant && v.ObjectKey != "" {
			key = v.ObjectKey
			break
		}
	}
	data, err := s.blobs.DownloadObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: read image: %v", processing.ErrFaceCompareUnavailable, err)
	}
	if len(data) == 0 || len(data) > processing.MaxFaceImageBytes {
		return nil, ErrFaceImageUnsupported
	}
	return data, nil
}
