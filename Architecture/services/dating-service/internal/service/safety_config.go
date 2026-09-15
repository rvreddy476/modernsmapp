// Lane D8 safety configuration and the two outbound clients the safety
// paths need: trust-safety-service (a grievance per report) and
// graph-service (is a trusted contact an accepted connection).
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// SafetyConfig holds the lane D8 limits.
type SafetyConfig struct {
	// PanicDedupeWindow: a trigger this soon after the user's last
	// unresolved incident updates that incident instead of paging again.
	PanicDedupeWindow time.Duration
	// PanicDailyLimit: incidents per rolling 24h before a new one is
	// recorded as suspected abuse and not paged.
	PanicDailyLimit int
	// ReportDailyLimit: reports per reporter per rolling 24h.
	ReportDailyLimit int
	// LocationShareDefault / LocationShareMax bound a live location share.
	LocationShareDefault time.Duration
	LocationShareMax     time.Duration
	// PanicRepageMaxAge: the sweeper re-publishes a page that never reached
	// Kafka only while the incident is younger than this.
	PanicRepageMaxAge time.Duration
}

// DefaultSafetyConfig is the lane D8 default set.
func DefaultSafetyConfig() SafetyConfig {
	return SafetyConfig{
		PanicDedupeWindow:    store.DefaultPanicDedupeWindow,
		PanicDailyLimit:      store.DefaultPanicDailyLimit,
		ReportDailyLimit:     store.DefaultReportDailyLimit,
		LocationShareDefault: 60 * time.Minute,
		LocationShareMax:     120 * time.Minute,
		PanicRepageMaxAge:    time.Hour,
	}
}

// SetSafetyConfig installs the limits (zero fields keep their default).
func (s *Service) SetSafetyConfig(cfg SafetyConfig) {
	s.safetyCfg = cfg
	s.safetyCfgSet = true
}

func (s *Service) safetyConfig() SafetyConfig {
	def := DefaultSafetyConfig()
	if !s.safetyCfgSet {
		return def
	}
	cfg := s.safetyCfg
	if cfg.PanicDedupeWindow <= 0 {
		cfg.PanicDedupeWindow = def.PanicDedupeWindow
	}
	if cfg.PanicDailyLimit <= 0 {
		cfg.PanicDailyLimit = def.PanicDailyLimit
	}
	if cfg.ReportDailyLimit <= 0 {
		cfg.ReportDailyLimit = def.ReportDailyLimit
	}
	if cfg.LocationShareMax <= 0 {
		cfg.LocationShareMax = def.LocationShareMax
	}
	if cfg.LocationShareDefault <= 0 || cfg.LocationShareDefault > cfg.LocationShareMax {
		cfg.LocationShareDefault = minDuration(def.LocationShareDefault, cfg.LocationShareMax)
	}
	if cfg.PanicRepageMaxAge <= 0 {
		cfg.PanicRepageMaxAge = def.PanicRepageMaxAge
	}
	return cfg
}

func minDuration(a, b time.Duration) time.Duration {
	if a < b {
		return a
	}
	return b
}

// internalKeyHeader is the shared internal-service credential header
// (shared/middleware.RequireInternalKey).
const internalKeyHeader = "X-Internal-Service-Key"

// ── trust-safety grievance link ─────────────────────────────────────────────

// GrievanceLinkRequest is one dating report handed to trust-safety.
type GrievanceLinkRequest struct {
	ReportID   uuid.UUID `json:"report_id"`
	ReporterID uuid.UUID `json:"reporter_id"`
	TargetID   uuid.UUID `json:"target_id"`
	Reason     string    `json:"reason"`
	Details    string    `json:"details,omitempty"`
	ReportedAt time.Time `json:"reported_at"`
}

// TrustSafetyClient creates (or returns the existing) grievance for a
// dating report. Idempotent on report_id.
type TrustSafetyClient interface {
	LinkDatingReportGrievance(ctx context.Context, req GrievanceLinkRequest) (uuid.UUID, error)
}

// SetTrustSafetyClient wires the grievance link. Nil leaves every report
// pending for the retry worker.
func (s *Service) SetTrustSafetyClient(c TrustSafetyClient) { s.trustSafety = c }

// TrustSafetyGrievancePath is trust-safety-service's internal route.
const TrustSafetyGrievancePath = "/v1/internal/grievances/dating-reports"

type httpTrustSafetyClient struct {
	baseURL string
	key     string
	client  *http.Client
}

// NewHTTPTrustSafetyClient builds the grievance client. It sends the
// internal service key and never an end-user identity header (the route
// refuses a request carrying one).
func NewHTTPTrustSafetyClient(baseURL, internalKey string, c *http.Client) TrustSafetyClient {
	if c == nil {
		c = &http.Client{Timeout: 5 * time.Second}
	}
	return &httpTrustSafetyClient{baseURL: strings.TrimRight(baseURL, "/"), key: internalKey, client: c}
}

func (c *httpTrustSafetyClient) LinkDatingReportGrievance(ctx context.Context, req GrievanceLinkRequest) (uuid.UUID, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return uuid.Nil, fmt.Errorf("encode grievance link: %w", err)
	}
	hr, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+TrustSafetyGrievancePath, bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, fmt.Errorf("build grievance link: %w", err)
	}
	hr.Header.Set("Content-Type", "application/json")
	if c.key != "" {
		hr.Header.Set(internalKeyHeader, c.key)
	}
	resp, err := c.client.Do(hr)
	if err != nil {
		return uuid.Nil, fmt.Errorf("trust-safety unreachable: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return uuid.Nil, fmt.Errorf("trust-safety grievance link status %d", resp.StatusCode)
	}
	var env struct {
		Data *struct {
			GrievanceID uuid.UUID `json:"grievance_id"`
		} `json:"data"`
		GrievanceID uuid.UUID `json:"grievance_id"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return uuid.Nil, fmt.Errorf("decode grievance link: %w", err)
	}
	id := env.GrievanceID
	if env.Data != nil && env.Data.GrievanceID != uuid.Nil {
		id = env.Data.GrievanceID
	}
	if id == uuid.Nil {
		return uuid.Nil, fmt.Errorf("trust-safety grievance link returned no grievance_id")
	}
	return id, nil
}

// ── graph connection check ──────────────────────────────────────────────────

// ConnectionChecker answers whether two users are an accepted connection.
type ConnectionChecker interface {
	IsAcceptedConnection(ctx context.Context, a, b uuid.UUID) (bool, error)
}

// SetConnectionChecker wires graph-service. Nil means only current matches
// can be trusted contacts.
func (s *Service) SetConnectionChecker(c ConnectionChecker) { s.connections = c }

type httpConnectionChecker struct {
	baseURL string
	key     string
	client  *http.Client
}

// NewHTTPConnectionChecker reads graph-service's relationship route
// (internal key; no user identity).
func NewHTTPConnectionChecker(baseURL, internalKey string, c *http.Client) ConnectionChecker {
	if c == nil {
		c = &http.Client{Timeout: 3 * time.Second}
	}
	return &httpConnectionChecker{baseURL: strings.TrimRight(baseURL, "/"), key: internalKey, client: c}
}

func (c *httpConnectionChecker) IsAcceptedConnection(ctx context.Context, a, b uuid.UUID) (bool, error) {
	q := url.Values{"user_id": {a.String()}, "other_id": {b.String()}}
	hr, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/graph/relationship?"+q.Encode(), nil)
	if err != nil {
		return false, fmt.Errorf("build relationship request: %w", err)
	}
	if c.key != "" {
		hr.Header.Set(internalKeyHeader, c.key)
	}
	resp, err := c.client.Do(hr)
	if err != nil {
		return false, fmt.Errorf("graph-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("graph-service relationship status %d", resp.StatusCode)
	}
	var env struct {
		Data struct {
			ConnectionStatus string `json:"connection_status"`
			Blocked          bool   `json:"blocked"`
			BlockedBy        bool   `json:"blocked_by"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&env); err != nil {
		return false, fmt.Errorf("decode relationship: %w", err)
	}
	return env.Data.ConnectionStatus == "accepted" && !env.Data.Blocked && !env.Data.BlockedBy, nil
}
