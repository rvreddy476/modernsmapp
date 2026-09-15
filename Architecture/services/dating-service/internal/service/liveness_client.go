package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Lane D5 — dating → media-service blink liveness.
//
// The call goes to media-service's internal liveness route with the internal
// service key and NO end-user identity headers: media-service refuses any
// request carrying X-User-Id / X-Verified-User-Id / X-Scopes / X-Admin-Role
// there. The user is named in the body (requester_user_id); media-service
// checks the video and the reference photo are both theirs.

// MediaLivenessPath is media-service's internal liveness route.
const MediaLivenessPath = "/internal/v1/media/faces/liveness"

// LivenessRequest names the blink video, the reference (approved primary
// photo) and the user who must own both.
type LivenessRequest struct {
	VideoMediaID     uuid.UUID
	ReferenceMediaID uuid.UUID
	RequesterUserID  uuid.UUID
}

// LivenessResult is media-service's answer.
type LivenessResult struct {
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

// LivenessClient runs the check. ErrSelfieMediaNotFound when media-service
// does not know both media as the requester's ready video/image;
// ErrSelfieVideoUnsupported when the video cannot be analysed;
// ErrFaceCompareUnavailable for anything that is not an answer.
type LivenessClient interface {
	CheckLiveness(ctx context.Context, req LivenessRequest) (*LivenessResult, error)
}

// SetLivenessClient wires the media-service liveness client.
func (s *Service) SetLivenessClient(c LivenessClient) {
	s.livenessClient = c
}

// HTTPLivenessClient is the production LivenessClient.
type HTTPLivenessClient struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPLivenessClient builds the client. httpClient nil → a 45s timeout
// (video read, ffmpeg sampling and up to MaxFrames+2 provider calls).
func NewHTTPLivenessClient(baseURL, internalKey string, httpClient *http.Client) *HTTPLivenessClient {
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 45 * time.Second}
	}
	return &HTTPLivenessClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		client:      httpClient,
	}
}

// CheckLiveness implements LivenessClient.
func (c *HTTPLivenessClient) CheckLiveness(ctx context.Context, req LivenessRequest) (*LivenessResult, error) {
	if req.VideoMediaID == uuid.Nil || req.ReferenceMediaID == uuid.Nil || req.RequesterUserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: liveness ids required")
	}
	body, err := json.Marshal(map[string]string{
		"video_media_id":     req.VideoMediaID.String(),
		"reference_media_id": req.ReferenceMediaID.String(),
		"requester_user_id":  req.RequesterUserID.String(),
	})
	if err != nil {
		return nil, fmt.Errorf("liveness: encode: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+MediaLivenessPath, bytes.NewReader(body))
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
		return nil, ErrSelfieMediaNotFound
	case http.StatusUnprocessableEntity:
		return nil, ErrSelfieVideoUnsupported
	default:
		return nil, fmt.Errorf("%w: media-service status %d", ErrFaceCompareUnavailable, resp.StatusCode)
	}
	var envelope struct {
		Data *LivenessResult `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil || envelope.Data == nil {
		return nil, fmt.Errorf("%w: undecodable media-service response", ErrFaceCompareUnavailable)
	}
	r := envelope.Data
	if r.Similarity < 0 || r.Similarity > 100 || r.BlinksDetected < 0 || r.FramesAnalysed < 0 || r.DurationMs < 0 {
		return nil, fmt.Errorf("%w: media-service response out of range", ErrFaceCompareUnavailable)
	}
	return r, nil
}
