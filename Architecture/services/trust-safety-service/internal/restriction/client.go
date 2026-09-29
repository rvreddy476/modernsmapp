// Package restriction is trust-safety's side of case-specific post holds
// (Copyright Match plan, sections 6.4 "restriction commands", 9.2, 9.5).
//
// client.go signs and sends ONE restriction command and reads post-service's
// restriction rows back for reconciliation; dispatcher.go drains the
// trust.restriction_commands queue through it. Nothing here mints a
// decision id: that happens once, in the service, before the command row
// is committed, so every send of a row (first try, retry, re-send after a
// crash, re-send by the reconciliation sweep) carries the same decision_id
// and the same claims digest and differs only in the capability's time
// bounds and signature. post-service replays the stored outcome for a
// digest it has seen.
//
// # Credentials
//
//   - The command: X-Internal-Service-Key plus a post_restriction capability
//     (moderationcap.RestrictionClaims signed under POST_RESTRICTION_HMAC_KEY,
//     TTL ≤ 15 min). post-service judges the command by the capability
//     alone.
//   - The read: X-Internal-Service-Key plus X-Service-Authorization: Bearer
//     <Ed25519 service token>, iss trust-safety-service, aud post, scope
//     post:restrictions.read (TRUST_SAFETY_SERVICE_TOKEN_KEY / _KID), the
//     way post-service's standing client mints its own.
package restriction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

const (
	// CommandPath is post-service's command and reconciliation route.
	CommandPath = "/v1/posts/internal/restrictions"

	// Audience, Issuer and OpRestrictionsRead are the service-token claims
	// post-service verifies on the reconciliation read.
	Audience           = "post"
	Issuer             = moderationcap.IssuerTrustSafety
	OpRestrictionsRead = "post:restrictions.read"
	// TokenTTL is the lifetime of each minted read token (≤ servicetoken.MaxTTL).
	TokenTTL = 2 * time.Minute

	// CapabilityTTL is the lifetime of each signed command capability. The
	// dispatcher signs at send time, so a short life costs nothing.
	CapabilityTTL = 5 * time.Minute

	// EnvPostServiceURL, EnvRestrictionKey, EnvTokenKey and EnvTokenKID are
	// the environment names main reads.
	EnvPostServiceURL = "POST_SERVICE_URL"
	EnvRestrictionKey = "POST_RESTRICTION_HMAC_KEY"
	EnvTokenKey       = "TRUST_SAFETY_SERVICE_TOKEN_KEY"
	EnvTokenKID       = "TRUST_SAFETY_SERVICE_TOKEN_KID"
	DefaultPostURL    = "http://post-service:8084"

	HeaderInternalServiceKey   = "X-Internal-Service-Key"
	HeaderServiceAuthorization = "X-Service-Authorization"

	// RequestTimeout bounds one command or read attempt.
	RequestTimeout = 5 * time.Second
	// MaxCaseIDsPerRead is post-service's limit on ?case_ids=.
	MaxCaseIDsPerRead = 100

	maxBody = 256 << 10
)

// Post-service error codes the outcome table names (post_restrictions_handler.go).
const (
	CodeStaleCaseRevision = "STALE_CASE_REVISION"
	CodeDecisionConflict  = "DECISION_CONFLICT"
	CodeSubjectMismatch   = "SUBJECT_MISMATCH"
	CodeStateMismatch     = "STATE_MISMATCH"
	CodeSubjectNotFound   = "SUBJECT_NOT_FOUND"
	CodeInvalidClaims     = "INVALID_CLAIMS"
	CodeInvalidCapability = "INVALID_CAPABILITY"
	CodeSourceNotEnabled  = "SOURCE_NOT_ENABLED"
)

// Command is what the dispatcher hands the client: the immutable part of
// a trust.restriction_commands row.
type Command struct {
	DecisionID      uuid.UUID
	CaseID          uuid.UUID
	CaseRevision    int64
	Action          string
	Source          string
	SubjectPostID   uuid.UUID
	SubjectAuthorID uuid.UUID
	ExpectedState   string
	ReasonCode      string
	PolicyVersion   string
	ActorID         uuid.UUID
}

// Claims builds the unsigned claims for cmd: the authority claims the
// signer would stamp (so Digest() here equals the digest of the signed
// claims post-service stores) and, for everything else, the row's values.
// The signer fills the time bounds, which the digest ignores.
func Claims(cmd Command) moderationcap.RestrictionClaims {
	return moderationcap.RestrictionClaims{
		Version: moderationcap.RestrictionClaimsVersion, Issuer: moderationcap.IssuerTrustSafety,
		Purpose: moderationcap.PurposePostRestriction, Audience: moderationcap.AudiencePostService,
		Action: cmd.Action, Source: cmd.Source,
		CaseID: cmd.CaseID.String(), CaseRevision: cmd.CaseRevision,
		SubjectID: cmd.SubjectPostID.String(), SubjectAuthorID: cmd.SubjectAuthorID.String(),
		ExpectedState: cmd.ExpectedState,
		DecisionID:    cmd.DecisionID.String(), PolicyVersion: cmd.PolicyVersion,
		ReasonCode: cmd.ReasonCode, ActorID: cmd.ActorID.String(),
	}
}

// Digest is the claims digest a command row stores (and a retry reproduces).
func Digest(cmd Command) []byte { return Claims(cmd).Digest() }

// Disposition is what the dispatcher does with an outcome (plan 9.2).
type Disposition int

const (
	// DispositionAcked: 2xx, including a replay. The case advances.
	DispositionAcked Disposition = iota
	// DispositionRetry: 5xx, 429 or a transport failure. Backoff and retry
	// with the same decision id.
	DispositionRetry
	// DispositionSuperseded: 409 STALE_CASE_REVISION. Superseded if a newer
	// command exists for the case; otherwise parked and escalated.
	DispositionSuperseded
	// DispositionParked: 403, 404, 409 DECISION_CONFLICT / SUBJECT_MISMATCH
	// / STATE_MISMATCH, 4xx of any other kind. Never retried blindly.
	DispositionParked
)

func (d Disposition) String() string {
	switch d {
	case DispositionAcked:
		return "acked"
	case DispositionRetry:
		return "retry"
	case DispositionSuperseded:
		return "superseded"
	case DispositionParked:
		return "parked"
	}
	return fmt.Sprintf("disposition(%d)", int(d))
}

// Classify is the outcome table. statusCode 0 means the request never got
// an answer (transport failure or timeout).
func Classify(statusCode int, errorCode string) Disposition {
	switch {
	case statusCode >= 200 && statusCode < 300:
		return DispositionAcked
	case statusCode == 0, statusCode >= 500, statusCode == http.StatusTooManyRequests, statusCode == http.StatusRequestTimeout:
		return DispositionRetry
	case statusCode == http.StatusConflict && errorCode == CodeStaleCaseRevision:
		return DispositionSuperseded
	default:
		// 400, 401, 403 (INVALID_CAPABILITY, SOURCE_NOT_ENABLED), 404,
		// 409 DECISION_CONFLICT / SUBJECT_MISMATCH / STATE_MISMATCH, 422
		// INVALID_CLAIMS, and anything unexpected: a retry with the same
		// claims can only produce the same answer.
		return DispositionParked
	}
}

// Outcome is one send's result.
type Outcome struct {
	Disposition Disposition
	StatusCode  int
	ErrorCode   string
	// Replayed is true when post-service had already applied this decision.
	Replayed bool
	// Body is the response body (the RestrictionOutcome on an ack).
	Body json.RawMessage
	// Err is the transport error, or a description of the refusal.
	Err error
}

// Client sends commands and reads restriction rows.
type Client struct {
	baseURL     string
	internalKey string
	signer      *moderationcap.RestrictionSigner
	tokens      *servicetoken.Signer
	http        *http.Client
}

// Config builds a Client.
type Config struct {
	BaseURL     string
	InternalKey string
	// Signer signs commands (POST_RESTRICTION_HMAC_KEY). Required to send.
	Signer *moderationcap.RestrictionSigner
	// Tokens mints the read token (TRUST_SAFETY_SERVICE_TOKEN_KEY/KID).
	// Required to read.
	Tokens     *servicetoken.Signer
	HTTPClient *http.Client
}

// ErrNotConfigured: the signer needed for the call is missing.
var ErrNotConfigured = errors.New("restriction client is not configured for this call")

// New builds a client. Either signer may be nil; the calls needing it fail
// with ErrNotConfigured.
func New(cfg Config) *Client {
	base := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if base == "" {
		base = DefaultPostURL
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: RequestTimeout}
	}
	return &Client{baseURL: base, internalKey: cfg.InternalKey, signer: cfg.Signer, tokens: cfg.Tokens, http: hc}
}

// FromEnv builds the client from POST_SERVICE_URL, POST_RESTRICTION_HMAC_KEY,
// TRUST_SAFETY_SERVICE_TOKEN_KEY and TRUST_SAFETY_SERVICE_TOKEN_KID. A blank
// HMAC key or token key leaves that half unconfigured (reported, never
// fatal here: main decides); a present but unusable one is an error.
func FromEnv(getenv func(string) string, internalKey string) (*Client, error) {
	cfg := Config{BaseURL: getenv(EnvPostServiceURL), InternalKey: internalKey}
	if key := getenv(EnvRestrictionKey); strings.TrimSpace(key) != "" {
		s, err := moderationcap.NewRestrictionSigner([]byte(key), CapabilityTTL)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvRestrictionKey, err)
		}
		cfg.Signer = s
	}
	key, kid := strings.TrimSpace(getenv(EnvTokenKey)), strings.TrimSpace(getenv(EnvTokenKID))
	if key != "" {
		if kid == "" {
			return nil, fmt.Errorf("%s is required with %s", EnvTokenKID, EnvTokenKey)
		}
		s, err := servicetoken.NewSignerFromBase64(Issuer, kid, key)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", EnvTokenKey, err)
		}
		cfg.Tokens = s
	}
	return New(cfg), nil
}

// CanSend reports whether commands can be signed.
func (c *Client) CanSend() bool { return c != nil && c.signer != nil }

// CanRead reports whether the reconciliation read can be authenticated.
func (c *Client) CanRead() bool { return c != nil && c.tokens != nil }

// Send signs cmd with fresh time bounds and POSTs it. It never returns an
// error for a refusal: the Outcome carries the disposition. Only a command
// the signer refuses (semantically invalid claims — an issuer bug) is a
// parked outcome with Err set and no request made.
func (c *Client) Send(ctx context.Context, cmd Command) Outcome {
	if !c.CanSend() {
		return Outcome{Disposition: DispositionRetry, Err: ErrNotConfigured}
	}
	claims, capability, err := c.signer.Sign(Claims(cmd))
	if err != nil {
		return Outcome{Disposition: DispositionParked, ErrorCode: "SIGNER_REFUSED", Err: err}
	}
	body, err := json.Marshal(map[string]any{"claims": claims, "capability": capability})
	if err != nil {
		return Outcome{Disposition: DispositionParked, ErrorCode: "ENCODE_FAILED", Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+CommandPath, bytes.NewReader(body))
	if err != nil {
		return Outcome{Disposition: DispositionParked, ErrorCode: "REQUEST_FAILED", Err: err}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.internalKey != "" {
		req.Header.Set(HeaderInternalServiceKey, c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return Outcome{Disposition: DispositionRetry, Err: err}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return Outcome{Disposition: DispositionRetry, StatusCode: resp.StatusCode, Err: err}
	}
	out := Outcome{StatusCode: resp.StatusCode}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		var env struct {
			Data struct {
				Replayed bool `json:"replayed"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &env) == nil {
			out.Replayed = env.Data.Replayed
		}
		out.Body = json.RawMessage(dataOf(raw))
		out.Disposition = DispositionAcked
		return out
	}
	out.ErrorCode = errorCodeOf(raw)
	out.Disposition = Classify(resp.StatusCode, out.ErrorCode)
	out.Err = fmt.Errorf("post-service answered %d %s", resp.StatusCode, out.ErrorCode)
	return out
}

// dataOf returns the "data" member of an api envelope, or the body itself.
func dataOf(raw []byte) []byte {
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &env) == nil && len(env.Data) > 0 {
		return env.Data
	}
	return raw
}

// errorCodeOf reads the error code from an api error envelope; "" otherwise.
func errorCodeOf(raw []byte) string {
	var env struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Error == nil {
		return ""
	}
	return strings.TrimSpace(env.Error.Code)
}

// Row is one post-service restriction row (its PostRestriction).
type Row struct {
	RestrictionID  uuid.UUID  `json:"restriction_id"`
	PostID         uuid.UUID  `json:"post_id"`
	Source         string     `json:"source"`
	CaseID         uuid.UUID  `json:"case_id"`
	Scope          string     `json:"scope"`
	State          string     `json:"state"`
	CaseRevision   int64      `json:"case_revision"`
	LastDecisionID uuid.UUID  `json:"last_decision_id"`
	PolicyVersion  string     `json:"policy_version"`
	ReasonCode     string     `json:"reason_code"`
	FirstPlacedAt  time.Time  `json:"first_placed_at"`
	PlacedAt       time.Time  `json:"placed_at"`
	ReleasedAt     *time.Time `json:"released_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// ErrReadRefused: post-service answered the read with a non-2xx status.
var ErrReadRefused = errors.New("restriction read refused")

// ListByCase reads every copyright restriction row for caseIDs (≤ 100),
// following next_cursor. A refusal is ErrReadRefused wrapped with the
// status; a transport failure is returned as is.
func (c *Client) ListByCase(ctx context.Context, caseIDs []uuid.UUID) ([]Row, error) {
	if !c.CanRead() {
		return nil, ErrNotConfigured
	}
	if len(caseIDs) == 0 {
		return nil, nil
	}
	if len(caseIDs) > MaxCaseIDsPerRead {
		return nil, fmt.Errorf("at most %d case ids per read", MaxCaseIDsPerRead)
	}
	ids := make([]string, len(caseIDs))
	for i, id := range caseIDs {
		ids[i] = id.String()
	}
	var out []Row
	cursor := ""
	for page := 0; page < 100; page++ {
		q := url.Values{"source": {"copyright"}, "case_ids": {strings.Join(ids, ",")}, "limit": {"500"}}
		if cursor != "" {
			q.Set("cursor", cursor)
		}
		items, next, err := c.readPage(ctx, q)
		if err != nil {
			return nil, err
		}
		out = append(out, items...)
		if next == "" || next == cursor {
			return out, nil
		}
		cursor = next
	}
	return out, fmt.Errorf("restriction read: too many pages")
}

func (c *Client) readPage(ctx context.Context, q url.Values) ([]Row, string, error) {
	tok, err := c.tokens.Mint(Audience, "restrictions-reconcile", []string{OpRestrictionsRead}, nil, TokenTTL)
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+CommandPath+"?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set(HeaderServiceAuthorization, "Bearer "+tok)
	if c.internalKey != "" {
		req.Header.Set(HeaderInternalServiceKey, c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		// The body could describe our credential problem; the code is enough.
		return nil, "", fmt.Errorf("%w: %d %s", ErrReadRefused, resp.StatusCode, errorCodeOf(raw))
	}
	var env struct {
		Data struct {
			Items      []Row  `json:"items"`
			NextCursor string `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, "", fmt.Errorf("restriction read: decode: %w", err)
	}
	return env.Data.Items, env.Data.NextCursor, nil
}
