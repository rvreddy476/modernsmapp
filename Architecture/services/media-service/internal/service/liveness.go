package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"

	"github.com/atpost/media-service/internal/processing"
	"github.com/google/uuid"
)

// Dating plan lane D5 — blink liveness + face match on one short video.
//
// The video is an ordinary video upload (reserve → upload → confirm, then the
// worker's transcode and moderation scan) owned by the requester; the
// reference is their ready, moderation-passed image (the approved primary
// photo). Only counts, flags, a similarity and a reason code leave here.

// ErrLivenessVideoUnsupported: the stored video cannot be analysed (not
// decodable, empty, or larger than MaxLivenessVideoBytes).
var ErrLivenessVideoUnsupported = errors.New("liveness: video cannot be analysed")

// MaxLivenessVideoBytes bounds the video read into memory. A 4-second phone
// recording is a few MB.
const MaxLivenessVideoBytes = 25 * 1024 * 1024

// LivenessOutcome is the liveness route's response body.
type LivenessOutcome struct {
	BlinksDetected       int     `json:"blinks_detected"`
	FramesAnalysed       int     `json:"frames_analysed"`
	DurationMs           int     `json:"duration_ms"`
	SingleFace           bool    `json:"single_face"`
	SameFaceAcrossFrames bool    `json:"same_face_across_frames"`
	Similarity           float64 `json:"similarity"`
	Match                bool    `json:"match"`
	Reason               string  `json:"reason,omitempty"`
	Provider             string  `json:"provider"`
}

// WithLiveness enables blink liveness on this service.
func (s *FaceCompareService) WithLiveness(analyzer processing.LivenessAnalyzer, cfg processing.LivenessConfig) *FaceCompareService {
	s.analyzer, s.livenessCfg = analyzer, cfg
	return s
}

// LivenessEnabled reports whether a liveness analyzer is wired.
func (s *FaceCompareService) LivenessEnabled() bool { return s != nil && s.analyzer != nil }

// CheckLiveness runs blink liveness on videoID and compares its face with
// referenceID. Both must belong to requester and be ready + moderation-passed
// (a video, an image); otherwise ErrFaceMediaNotFound.
func (s *FaceCompareService) CheckLiveness(ctx context.Context, requester, videoID, referenceID uuid.UUID) (*LivenessOutcome, error) {
	if requester == uuid.Nil || videoID == uuid.Nil || referenceID == uuid.Nil {
		return nil, ErrFaceCompareInvalid
	}
	if videoID == referenceID {
		return nil, ErrFaceSameMedia
	}
	if !s.LivenessEnabled() {
		return nil, fmt.Errorf("%w: liveness not configured", processing.ErrFaceCompareUnavailable)
	}
	video, err := s.eligibleMedia(ctx, requester, videoID, "video")
	if err != nil {
		return nil, err
	}
	reference, err := s.eligibleMedia(ctx, requester, referenceID, "image")
	if err != nil {
		return nil, err
	}
	out := &LivenessOutcome{Provider: s.analyzer.Name()}
	longest := s.livenessCfg.LongestAcceptedMs()

	// The worker's ffprobe duration is on the row: an over-long recording is
	// refused before its bytes are read or a provider is called.
	if video.DurationMs != nil && *video.DurationMs > longest {
		out.DurationMs, out.Reason = *video.DurationMs, processing.LivenessReasonVideoTooLong
		s.logLiveness(requester, videoID, referenceID, out)
		return out, nil
	}
	if video.FileSizeBytes > MaxLivenessVideoBytes {
		return nil, ErrLivenessVideoUnsupported
	}
	videoBytes, err := s.blobs.DownloadObject(ctx, video.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("%w: read video: %v", processing.ErrFaceCompareUnavailable, err)
	}
	if len(videoBytes) == 0 || len(videoBytes) > MaxLivenessVideoBytes {
		return nil, ErrLivenessVideoUnsupported
	}
	refBytes, err := s.readForCompare(ctx, reference)
	if err != nil {
		return nil, err
	}

	res, err := s.analyzer.AnalyzeLiveness(ctx, videoBytes, refBytes, s.livenessCfg)
	if err != nil {
		slog.Warn("media: liveness unavailable", "requester_user_id", requester, "video_media_id", videoID,
			"reference_media_id", referenceID, "provider", out.Provider, "error", err)
		switch {
		case errors.Is(err, processing.ErrLivenessVideoUnreadable):
			return nil, ErrLivenessVideoUnsupported
		case errors.Is(err, processing.ErrFaceCompareUnavailable):
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", processing.ErrFaceCompareUnavailable, err)
	}
	out.BlinksDetected = res.BlinksDetected
	out.FramesAnalysed = res.FramesAnalysed
	out.DurationMs = res.DurationMs
	out.SingleFace = res.SingleFace
	out.SameFaceAcrossFrames = res.SameFaceAcrossFrames
	out.Reason = res.Reason
	if out.Reason == "" {
		out.Similarity = math.Round(res.Similarity*100) / 100
	}
	required := s.livenessCfg.RequiredBlinks
	if required <= 0 {
		required = processing.DefaultLivenessConfig().RequiredBlinks
	}
	out.Match = out.Reason == "" && out.SingleFace && out.SameFaceAcrossFrames &&
		out.BlinksDetected >= required && out.Similarity >= s.threshold
	s.logLiveness(requester, videoID, referenceID, out)
	return out, nil
}

func (s *FaceCompareService) logLiveness(requester, videoID, referenceID uuid.UUID, out *LivenessOutcome) {
	slog.Info("media: face liveness",
		"requester_user_id", requester, "video_media_id", videoID, "reference_media_id", referenceID,
		"provider", out.Provider, "blinks_detected", out.BlinksDetected, "frames_analysed", out.FramesAnalysed,
		"duration_ms", out.DurationMs, "single_face", out.SingleFace, "same_face", out.SameFaceAcrossFrames,
		"similarity", out.Similarity, "match", out.Match, "reason", out.Reason)
}
