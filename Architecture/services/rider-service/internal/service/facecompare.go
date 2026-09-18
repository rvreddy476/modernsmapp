package service

// Server-side selfie check: the captain's selfie (profile_photo upload,
// media_id) is compared with the driving-licence photo DigiLocker returned
// (photo_media_id) through media-service's internal face-compare route, the
// same one dating-service uses (copied from its liveness client). The call
// carries the internal service key and NO end-user identity headers; the
// captain is named in the body (requester_user_id).
//
// The verdict is binary for onboarding: similarity >= the threshold
// (MOPEDU_SELFIE_MIN_SIMILARITY, default 80) verifies the selfie
// automatically; anything else — below threshold, no face, media-service
// unavailable — leaves it pending for a human. It is never auto-verified on
// an error.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MediaFaceComparePath is media-service's internal compare route.
const MediaFaceComparePath = "/internal/v1/media/faces/compare"

// Selfie check environment.
const (
	EnvSelfieMinSimilarity     = "MOPEDU_SELFIE_MIN_SIMILARITY"
	EnvFaceCompareMode         = "MOPEDU_FACE_COMPARE_MODE"
	EnvMediaServiceURL         = "MEDIA_SERVICE_URL"
	DefaultSelfieMinSimilarity = 80.0
	DefaultMediaServiceURL     = "http://media-service:8087"
)

// FaceCompareRequest names the selfie, the reference (DL photo) and the
// captain who must own the selfie.
type FaceCompareRequest struct {
	SourceMediaID   uuid.UUID
	TargetMediaID   uuid.UUID
	RequesterUserID uuid.UUID
}

// FaceCompareResult is media-service's answer (similarity 0-100).
type FaceCompareResult struct {
	Similarity      float64 `json:"similarity"`
	FaceCountSource int     `json:"face_count_source"`
	FaceCountTarget int     `json:"face_count_target"`
	Match           bool    `json:"match"`
	Reason          string  `json:"reason,omitempty"`
	Provider        string  `json:"provider"`
}

var (
	// ErrFaceCompareUnavailable: no answer (media-service or its provider
	// unavailable). Never a pass, never a fail: the selfie stays pending.
	ErrFaceCompareUnavailable = errors.New("face comparison is unavailable")
	// ErrFaceMediaNotFound: media-service does not know both media as the
	// captain's ready images.
	ErrFaceMediaNotFound = errors.New("face compare media not found")
	// ErrFaceImageUnsupported: an image cannot be compared.
	ErrFaceImageUnsupported = errors.New("face compare image unsupported")
)

// FaceComparer runs the check.
type FaceComparer interface {
	CompareFaces(ctx context.Context, req FaceCompareRequest) (*FaceCompareResult, error)
}

// SetFaceComparer wires the media-service face comparer and the threshold.
func (s *Service) SetFaceComparer(c FaceComparer, minSimilarity float64) {
	s.faceCompare = c
	if minSimilarity <= 0 || minSimilarity > 100 {
		minSimilarity = DefaultSelfieMinSimilarity
	}
	s.selfieMinSimilarity = minSimilarity
}

// HTTPFaceComparer is the production comparer.
type HTTPFaceComparer struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPFaceComparer builds the client. httpClient nil → a 30s timeout.
func NewHTTPFaceComparer(baseURL, internalKey string, httpClient *http.Client) *HTTPFaceComparer {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPFaceComparer{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: httpClient}
}

// CompareFaces implements FaceComparer.
func (c *HTTPFaceComparer) CompareFaces(ctx context.Context, req FaceCompareRequest) (*FaceCompareResult, error) {
	if req.SourceMediaID == uuid.Nil || req.TargetMediaID == uuid.Nil || req.RequesterUserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: face compare ids required")
	}
	body, err := json.Marshal(map[string]string{
		"source_media_id":   req.SourceMediaID.String(),
		"target_media_id":   req.TargetMediaID.String(),
		"requester_user_id": req.RequesterUserID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("face compare: encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+MediaFaceComparePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build request: %v", ErrFaceCompareUnavailable, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		httpReq.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: media-service unreachable: %v", ErrFaceCompareUnavailable, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrFaceMediaNotFound
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return nil, ErrFaceImageUnsupported
	default:
		return nil, fmt.Errorf("%w: media-service status %d", ErrFaceCompareUnavailable, resp.StatusCode)
	}
	var envelope struct {
		Data *FaceCompareResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		return nil, fmt.Errorf("%w: undecodable media-service response", ErrFaceCompareUnavailable)
	}
	r := envelope.Data
	if r.Similarity < 0 || r.Similarity > 100 {
		return nil, fmt.Errorf("%w: media-service similarity out of range", ErrFaceCompareUnavailable)
	}
	return r, nil
}

// MockFaceComparer answers a fixed similarity. Dev only: FaceCompareFromEnv
// refuses it in production, where it would verify every selfie.
type MockFaceComparer struct {
	Similarity float64
	Err        error
}

// CompareFaces implements FaceComparer.
func (m *MockFaceComparer) CompareFaces(_ context.Context, _ FaceCompareRequest) (*FaceCompareResult, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &FaceCompareResult{Similarity: m.Similarity, FaceCountSource: 1, FaceCountTarget: 1, Match: m.Similarity >= DefaultSelfieMinSimilarity, Provider: "mock"}, nil
}

// FaceCompareFromEnv selects the comparer:
//
//	MOPEDU_FACE_COMPARE_MODE   http (default) | mock (dev only; refused in
//	                           production)
//	MEDIA_SERVICE_URL          the media-service base URL; required in
//	                           production (boot is refused without it),
//	                           http://media-service:8087 elsewhere
//	MOPEDU_SELFIE_MIN_SIMILARITY  0-100, default 80
func FaceCompareFromEnv(getenv func(string) string, production bool, internalKey string) (FaceComparer, float64, error) {
	min := DefaultSelfieMinSimilarity
	if raw := strings.TrimSpace(getenv(EnvSelfieMinSimilarity)); raw != "" {
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 || v > 100 {
			return nil, 0, fmt.Errorf("%s must be a number between 1 and 100", EnvSelfieMinSimilarity)
		}
		min = v
	}
	mode := strings.ToLower(strings.TrimSpace(getenv(EnvFaceCompareMode)))
	switch mode {
	case "mock":
		if production {
			return nil, 0, fmt.Errorf("%s=mock is refused in production; set it to http and MEDIA_SERVICE_URL", EnvFaceCompareMode)
		}
		return &MockFaceComparer{Similarity: 96}, min, nil
	case "", "http":
		url := strings.TrimSpace(getenv(EnvMediaServiceURL))
		if url == "" {
			if production {
				return nil, 0, fmt.Errorf("%s is required in production: the selfie check needs media-service", EnvMediaServiceURL)
			}
			url = DefaultMediaServiceURL
		}
		return NewHTTPFaceComparer(url, internalKey, nil), min, nil
	}
	return nil, 0, fmt.Errorf("unknown %s %q (want http or mock)", EnvFaceCompareMode, mode)
}
