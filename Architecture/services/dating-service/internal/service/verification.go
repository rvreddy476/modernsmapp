// Verification service — Aadhaar/DigiLocker + selfie face match.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
// Aadhaar number is NEVER stored or logged. The service touches only:
//   - DigiLocker assertion id (digilocker_ref)
//   - SHA-256 hash of the document-type label
//   - Verification timestamp
//
// Trust tiers step up phone -> selfie -> aadhaar; never demote.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/atpost/dating-service/internal/digilocker"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// AadhaarFlowStart is returned by StartAadhaarFlow to the mobile client.
type AadhaarFlowStart struct {
	DigiLockerAuthorizeURL string `json:"digilocker_authorize_url"`
	State                  string `json:"state"`
}

// AadhaarFlowResult is returned to the client after a successful callback.
type AadhaarFlowResult struct {
	Verified  bool      `json:"verified"`
	TrustTier string    `json:"trust_tier"`
	IssuedAt  time.Time `json:"issued_at"`
}

// SelfieFlowResult is returned by CompleteSelfieFlow.
type SelfieFlowResult struct {
	Passed     bool    `json:"passed"`
	Score      float64 `json:"score"`
	TrustTier  string  `json:"trust_tier"`
	Threshold  float64 `json:"threshold"`
}

// MediaServiceClient fetches the primary photo embedding for selfie match.
// Wire from main.go; tests inject a fake.
type MediaServiceClient interface {
	GetEmbedding(ctx context.Context, mediaID uuid.UUID) ([]float64, error)
}

// SetDigiLockerClient injects the partner client. main.go selects HTTP vs
// Mock via DIGILOCKER_MODE.
func (s *Service) SetDigiLockerClient(c digilocker.Client) {
	s.digilockerClient = c
}

// SetMediaServiceClient injects the media-service embedding fetcher.
func (s *Service) SetMediaServiceClient(c MediaServiceClient) {
	s.mediaClient = c
}

// SelfieMatchThreshold is the cosine-similarity bar. >= passes.
const SelfieMatchThreshold = 0.75

// digilockerStateTTL is the spec-required PKCE state lifetime (10 min).
const digilockerStateTTL = 10 * time.Minute

// stateKey returns the Redis key for a PKCE state nonce.
func stateKey(state string) string { return "dating:digilocker:state:" + state }

// generateState returns a 32-byte URL-safe random nonce.
func generateState() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// StartAadhaarFlow allocates a one-shot state nonce, stores it in Redis
// keyed by the user, and returns the DigiLocker authorize URL the mobile
// app should open.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
func (s *Service) StartAadhaarFlow(ctx context.Context, userID uuid.UUID) (*AadhaarFlowStart, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	clientID := os.Getenv("DIGILOCKER_CLIENT_ID")
	redirectURI := os.Getenv("DIGILOCKER_REDIRECT_URI")
	authorizeBase := os.Getenv("DIGILOCKER_AUTHORIZE_URL")
	if authorizeBase == "" {
		authorizeBase = "https://api.digitallocker.gov.in/public/oauth2/1/authorize"
	}
	state, err := generateState()
	if err != nil {
		return nil, err
	}
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, stateKey(state), userID.String(), digilockerStateTTL).Err(); err != nil {
			return nil, fmt.Errorf("persist state: %w", err)
		}
	}
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("scope", "aadhaar")
	authorizeURL := authorizeBase + "?" + q.Encode()

	if s.producer != nil {
		// Submission attempt (the user has clicked "verify with Aadhaar").
		if perr := s.producer.PublishVerificationSubmitted(ctx, userID, "aadhaar"); perr != nil {
			slog.Warn("publish verification.submitted failed", "error", perr)
		}
	}
	return &AadhaarFlowStart{DigiLockerAuthorizeURL: authorizeURL, State: state}, nil
}

// CompleteAadhaarFlow validates the PKCE state via Redis, exchanges the
// code with the partner, and persists the assertion. On success, trust
// tier is bumped to 'aadhaar' and dating.verification.completed is emitted.
//
// DPDP Act compliant — see PULSE_DATING_SPEC.md §15.8
func (s *Service) CompleteAadhaarFlow(ctx context.Context, userID uuid.UUID, code, state string) (*AadhaarFlowResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if code == "" || state == "" {
		return nil, fmt.Errorf("invalid: code and state required")
	}
	if s.rdb != nil {
		stored, err := s.rdb.Get(ctx, stateKey(state)).Result()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				return nil, fmt.Errorf("forbidden: state expired or invalid")
			}
			return nil, fmt.Errorf("lookup state: %w", err)
		}
		if stored != userID.String() {
			return nil, fmt.Errorf("forbidden: state does not match user")
		}
		// One-shot: remove regardless of outcome below.
		if err := s.rdb.Del(ctx, stateKey(state)).Err(); err != nil {
			slog.Warn("clear state key", "error", err)
		}
	}
	if s.digilockerClient == nil {
		return nil, fmt.Errorf("digilocker client not configured")
	}
	assertion, err := s.digilockerClient.ExchangeCode(ctx, code, state)
	if err != nil {
		return nil, fmt.Errorf("digilocker exchange: %w", err)
	}
	if assertion == nil || assertion.Reference == "" {
		return nil, fmt.Errorf("digilocker: empty assertion")
	}
	docHash := digilocker.HashDocumentType(assertion.DocumentType)
	if err := s.store.RecordAadhaarVerification(ctx, userID, assertion.Reference, docHash); err != nil {
		return nil, fmt.Errorf("persist verification: %w", err)
	}
	if err := s.store.UpdateTrustTier(ctx, userID, "aadhaar"); err != nil {
		return nil, fmt.Errorf("bump trust tier: %w", err)
	}
	if s.producer != nil {
		if perr := s.producer.PublishVerificationCompleted(ctx, userID, "aadhaar", "aadhaar"); perr != nil {
			slog.Warn("publish verification.completed failed", "error", perr)
		}
	}
	return &AadhaarFlowResult{
		Verified:  true,
		TrustTier: "aadhaar",
		IssuedAt:  assertion.IssuedAt,
	}, nil
}

// CompleteSelfieFlow scores the user's submitted selfie embedding against
// the primary profile photo embedding (looked up via media-service). On
// pass, trust_tier moves phone->selfie (or stays at aadhaar if already
// there). dating.verification.completed is emitted on pass only.
func (s *Service) CompleteSelfieFlow(ctx context.Context, userID uuid.UUID, embedding []float64) (*SelfieFlowResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if len(embedding) == 0 {
		return nil, fmt.Errorf("invalid: embedding required")
	}
	photos, err := s.store.ListPhotos(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("load photos: %w", err)
	}
	var primaryMedia uuid.UUID
	for _, p := range photos {
		if p.IsPrimary {
			primaryMedia = p.MediaID
			break
		}
	}
	if primaryMedia == uuid.Nil {
		return nil, fmt.Errorf("invalid: no primary photo to compare against")
	}
	if s.mediaClient == nil {
		return nil, fmt.Errorf("media client not configured")
	}
	stored, err := s.mediaClient.GetEmbedding(ctx, primaryMedia)
	if err != nil {
		return nil, fmt.Errorf("media embedding: %w", err)
	}
	if len(stored) == 0 {
		return nil, fmt.Errorf("not_found: no embedding stored for primary photo")
	}
	score := cosineSimilarity(embedding, stored)
	passed := score >= SelfieMatchThreshold
	status := "failed"
	if passed {
		status = "passed"
	}
	if err := s.store.RecordSelfieAttempt(ctx, userID, score, status); err != nil {
		return nil, fmt.Errorf("persist selfie attempt: %w", err)
	}
	tier := "phone"
	if passed {
		// Bump to selfie unless the user is already aadhaar.
		v, _ := s.store.GetVerification(ctx, userID)
		if v != nil && v.AadhaarStatus != nil && *v.AadhaarStatus == "verified" {
			tier = "aadhaar"
		} else {
			if err := s.store.UpdateTrustTier(ctx, userID, "selfie"); err != nil {
				return nil, fmt.Errorf("bump trust tier: %w", err)
			}
			tier = "selfie"
		}
		if s.producer != nil {
			if perr := s.producer.PublishVerificationCompleted(ctx, userID, "selfie", tier); perr != nil {
				slog.Warn("publish verification.completed failed", "error", perr)
			}
		}
	}
	return &SelfieFlowResult{
		Passed:    passed,
		Score:     score,
		TrustTier: tier,
		Threshold: SelfieMatchThreshold,
	}, nil
}

// cosineSimilarity computes the cosine of two equal-length float64 vectors.
// If lengths differ we use the shared prefix length; if either is all-zero
// we return 0.
func cosineSimilarity(a, b []float64) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	if n == 0 {
		return 0
	}
	var dot, normA, normB float64
	for i := 0; i < n; i++ {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	denom := math.Sqrt(normA) * math.Sqrt(normB)
	if denom == 0 {
		return 0
	}
	return dot / denom
}

// httpMediaClient is the production MediaServiceClient.
type httpMediaClient struct {
	baseURL string
	client  *http.Client
	apiKey  string
}

// NewHTTPMediaClient configures from MEDIA_SERVICE_URL + INTERNAL_SERVICE_KEY.
func NewHTTPMediaClient() MediaServiceClient {
	base := os.Getenv("MEDIA_SERVICE_URL")
	if base == "" {
		base = "http://media-service:8095"
	}
	return &httpMediaClient{
		baseURL: base,
		client:  &http.Client{Timeout: 4 * time.Second},
		apiKey:  os.Getenv("INTERNAL_SERVICE_KEY"),
	}
}

// GetEmbedding calls media-service GET /v1/media/embedding/:mediaId.
func (c *httpMediaClient) GetEmbedding(ctx context.Context, mediaID uuid.UUID) ([]float64, error) {
	if mediaID == uuid.Nil {
		return nil, fmt.Errorf("invalid: media id required")
	}
	url := fmt.Sprintf("%s/v1/media/embedding/%s", c.baseURL, mediaID.String())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.apiKey != "" {
		req.Header.Set("X-Internal-Key", c.apiKey)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("media-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("not_found: embedding")
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("media-service status %d", resp.StatusCode)
	}
	var envelope struct {
		Data struct {
			Embedding []float64 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode embedding: %w", err)
	}
	return envelope.Data.Embedding, nil
}

