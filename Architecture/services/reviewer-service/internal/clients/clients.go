package clients

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// Clients wraps outbound calls to sibling services (graph for collusion checks,
// monetization for base-pay credit). All calls carry the internal service key.
type Clients struct {
	http            *http.Client
	graphURL        string
	monetizationURL string
	postURL         string
	walletURL       string
	internalKey     string
	// postTokens mints the service tokens post-service requires on its
	// internal review writes (WithPostTokens).
	postTokens *servicetoken.Signer
}

// Security sweep #11 (21 Oct 2026): post-service no longer takes the shared
// internal key as evidence of the caller on /v1/posts/internal WRITES (the
// gateway stamps that key on every proxied request). Its review-status and
// visibility writes admit only a reviewer-service token carrying the route's
// operation, audience "post".
const (
	// Issuer is the iss post-service pins on the review writes.
	Issuer = "reviewer-service"
	// OpReviewStatusWrite and OpVisibilityWrite are the scopes the two writes
	// require.
	OpReviewStatusWrite = "post:review-status.write"
	OpVisibilityWrite   = "post:visibility.write"
	postAudience        = "post"
	postTokenTTL        = time.Minute
	// ServiceAuthHeader carries the token.
	ServiceAuthHeader = "X-Service-Authorization"
)

// ErrPostTokensNotConfigured: no signer, so the write cannot be
// authenticated and is not sent (post-service would answer 401).
var ErrPostTokensNotConfigured = errors.New("reviewer-service has no service-token signer for post-service (REVIEWER_SERVICE_TOKEN_KEY)")

// WithPostTokens sets the signer the post-service writes mint with.
func (c *Clients) WithPostTokens(s *servicetoken.Signer) *Clients {
	c.postTokens = s
	return c
}

// postToken mints a token for op, or refuses without a signer.
func (c *Clients) postToken(op string) (string, error) {
	if c.postTokens == nil {
		return "", ErrPostTokensNotConfigured
	}
	return c.postTokens.Mint(postAudience, "review-write", []string{op}, nil, postTokenTTL)
}

func New(graphURL, monetizationURL, postURL, walletURL, internalKey string) *Clients {
	return &Clients{
		http:            &http.Client{Timeout: 4 * time.Second},
		graphURL:        graphURL,
		monetizationURL: monetizationURL,
		postURL:         postURL,
		walletURL:       walletURL,
		internalKey:     internalKey,
	}
}

type kycResp struct {
	Data struct {
		Tier       string  `json:"tier"`
		VerifiedAt *string `json:"verified_at"`
	} `json:"data"`
}

// IsKYCVerified reports whether the user has completed identity verification in
// wallet-service (tier full/enhanced). Fails CLOSED (false) on any error so an
// unverifiable user is never treated as verified.
func (c *Clients) IsKYCVerified(ctx context.Context, userID uuid.UUID) (bool, error) {
	if c.walletURL == "" {
		return false, fmt.Errorf("wallet url not configured")
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, c.walletURL+"/v1/wallet/kyc", nil)
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	req.Header.Set("X-User-Id", userID.String())
	resp, err := c.http.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("wallet kyc status %d", resp.StatusCode)
	}
	var k kycResp
	if err := json.NewDecoder(resp.Body).Decode(&k); err != nil {
		return false, err
	}
	tier := k.Data.Tier
	return tier == "full" || tier == "enhanced", nil
}

type relationshipResp struct {
	Data struct {
		Follows          bool   `json:"follows"`
		FollowedBy       bool   `json:"followed_by"`
		// On this single-pair route "blocked" means the creator blocked the
		// reviewer and "blocked_by" means the reviewer blocked the creator.
		// A block either way is a tie, so both are read.
		Blocked          bool   `json:"blocked"`
		BlockedBy        bool   `json:"blocked_by"`
		ConnectionStatus string `json:"connection_status"`
	} `json:"data"`
}

// IsRelated reports whether reviewer and creator have ANY social tie (follow in
// either direction, an accepted/pending connection, or a block) — anti-collusion.
// Fails CLOSED: on any error it returns related=true so an unverifiable pair is
// never assigned.
func (c *Clients) IsRelated(ctx context.Context, reviewerUserID, creatorID uuid.UUID) (bool, error) {
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s",
		c.graphURL, reviewerUserID, creatorID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return true, fmt.Errorf("graph relationship status %d", resp.StatusCode)
	}
	var r relationshipResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return true, err
	}
	d := r.Data
	related := d.Follows || d.FollowedBy || d.Blocked || d.BlockedBy ||
		(d.ConnectionStatus != "" && d.ConnectionStatus != "none")
	return related, nil
}

// CreditReviewer credits the reviewer's monetization ledger (platform-funded
// base pay) via the internal credit endpoint.
func (c *Clients) CreditReviewer(ctx context.Context, userID uuid.UUID, amountPaise int64, referenceID, description string) error {
	body, _ := json.Marshal(map[string]any{
		"to_user_id":     userID.String(),
		"amount_paise":   amountPaise,
		"reference_type": "reviewer_base",
		"reference_id":   referenceID,
		"description":    description,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.monetizationURL+"/v1/internal/credit", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("monetization credit status %d", resp.StatusCode)
	}
	return nil
}

// SetPostReviewStatus flips a flagged post to a terminal review_status via
// post-service's internal endpoint (used by the ML pre-filter's auto decisions).
func (c *Clients) SetPostReviewStatus(ctx context.Context, postID uuid.UUID, status string) error {
	body, _ := json.Marshal(map[string]any{
		"post_id": postID.String(),
		"status":  status,
	})
	tok, err := c.postToken(OpReviewStatusWrite)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.postURL+"/v1/posts/internal/review-status", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("post review-status update status %d", resp.StatusCode)
	}
	return nil
}

// SetPostVisibility promotes a staged post to a new visibility (Phase 4b
// promotion worker) via post-service's internal endpoint.
func (c *Clients) SetPostVisibility(ctx context.Context, postID uuid.UUID, visibility string) error {
	body, _ := json.Marshal(map[string]any{
		"post_id":    postID.String(),
		"visibility": visibility,
	})
	tok, err := c.postToken(OpVisibilityWrite)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost,
		c.postURL+"/v1/posts/internal/visibility", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	req.Header.Set(ServiceAuthHeader, "Bearer "+tok)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("post visibility update status %d", resp.StatusCode)
	}
	return nil
}
