// Package facecompare is the server-side selfie check: the professional's
// selfie (their own upload, media_id) is compared with the reference face
// from their DigiLocker Aadhaar result through media-service's internal
// face-compare route.
//
// Copied from rider-service internal/service/facecompare.go (the same route
// dating-service uses). The call carries the internal service key and NO
// end-user identity headers; the professional is named in the body
// (requester_user_id) and media-service refuses media they do not own.
//
// The verdict for onboarding: similarity >= DOORSTEP_SELFIE_MIN_SIMILARITY
// (default 80) passes; below it, or media-service unavailable, the selfie
// stays pending. It is never passed on an error.
package facecompare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MediaFaceComparePath is media-service's internal compare route.
const MediaFaceComparePath = "/internal/v1/media/faces/compare"

// Modes (DOORSTEP_FACE_COMPARE_MODE).
const (
	ModeHTTP = "http"
	ModeMock = "mock"
)

// Request names the selfie, the reference and the professional who must own
// both.
type Request struct {
	SourceMediaID   uuid.UUID
	TargetMediaID   uuid.UUID
	RequesterUserID uuid.UUID
}

// Result is media-service's answer (similarity 0-100).
type Result struct {
	Similarity      float64 `json:"similarity"`
	FaceCountSource int     `json:"face_count_source"`
	FaceCountTarget int     `json:"face_count_target"`
	Match           bool    `json:"match"`
	Reason          string  `json:"reason,omitempty"`
	Provider        string  `json:"provider"`
}

var (
	// ErrUnavailable: no answer. Never a pass, never a fail.
	ErrUnavailable = errors.New("face comparison is unavailable")
	// ErrMediaNotFound: media-service does not know both media as the
	// professional's ready images.
	ErrMediaNotFound = errors.New("face compare media not found")
	// ErrImageUnsupported: an image cannot be compared.
	ErrImageUnsupported = errors.New("face compare image unsupported")
)

// Comparer runs the check.
type Comparer interface {
	CompareFaces(ctx context.Context, req Request) (*Result, error)
	// NeedsReference is false only for the mock, which answers without
	// looking at the reference image.
	NeedsReference() bool
}

// New selects the comparer. mock is refused in production (it would pass
// every selfie); any other unknown mode is an error.
func New(mode string, production bool, mediaURL, internalKey string) (Comparer, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case ModeMock:
		if production {
			return nil, fmt.Errorf("DOORSTEP_FACE_COMPARE_MODE=mock is refused in production")
		}
		return &Mock{Similarity: 96}, nil
	case ModeHTTP, "":
		if strings.TrimSpace(mediaURL) == "" {
			return nil, fmt.Errorf("MEDIA_SERVICE_URL is required for the selfie face compare")
		}
		return NewHTTP(mediaURL, internalKey, nil), nil
	}
	return nil, fmt.Errorf("unknown DOORSTEP_FACE_COMPARE_MODE %q (want http or mock)", mode)
}

// HTTP is the production comparer.
type HTTP struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTP builds the client. httpClient nil → a 30 s timeout.
func NewHTTP(baseURL, internalKey string, httpClient *http.Client) *HTTP {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTP{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: httpClient}
}

// NeedsReference implements Comparer.
func (c *HTTP) NeedsReference() bool { return true }

// CompareFaces implements Comparer.
func (c *HTTP) CompareFaces(ctx context.Context, req Request) (*Result, error) {
	if req.SourceMediaID == uuid.Nil || req.TargetMediaID == uuid.Nil || req.RequesterUserID == uuid.Nil {
		return nil, fmt.Errorf("%w: face compare ids required", ErrMediaNotFound)
	}
	body, err := json.Marshal(map[string]string{
		"source_media_id":   req.SourceMediaID.String(),
		"target_media_id":   req.TargetMediaID.String(),
		"requester_user_id": req.RequesterUserID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("%w: encode", ErrUnavailable)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+MediaFaceComparePath, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("%w: build request", ErrUnavailable)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		httpReq.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("%w: media-service unreachable", ErrUnavailable)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return nil, ErrMediaNotFound
	case http.StatusUnprocessableEntity, http.StatusBadRequest:
		return nil, ErrImageUnsupported
	default:
		return nil, fmt.Errorf("%w: media-service status %d", ErrUnavailable, resp.StatusCode)
	}
	var envelope struct {
		Data *Result `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		return nil, fmt.Errorf("%w: undecodable media-service response", ErrUnavailable)
	}
	if envelope.Data.Similarity < 0 || envelope.Data.Similarity > 100 {
		return nil, fmt.Errorf("%w: similarity out of range", ErrUnavailable)
	}
	return envelope.Data, nil
}

// Mock answers a fixed similarity (development only).
type Mock struct {
	Similarity float64
	Err        error
}

// NeedsReference implements Comparer.
func (m *Mock) NeedsReference() bool { return false }

// CompareFaces implements Comparer.
func (m *Mock) CompareFaces(_ context.Context, _ Request) (*Result, error) {
	if m.Err != nil {
		return nil, m.Err
	}
	return &Result{Similarity: m.Similarity, FaceCountSource: 1, FaceCountTarget: 1, Match: m.Similarity >= 80, Provider: "mock"}, nil
}
