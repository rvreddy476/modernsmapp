// Hide from people I know (mechanic M16, DATING_HIDE_KNOWN_ENABLED).
//
//	GET /v1/dating/hide-known   {enabled, hidden_count, refreshed_at?}
//	PUT /v1/dating/hide-known   {enabled}
//
// "People I know" are the user's accepted Momentum connections
// (graph-service). Turning it on takes a snapshot of them; from then on the
// user and those people never see each other in a deck or in picks, either
// way. The snapshot refreshes daily; a failed refresh keeps the old one.
// Turning it on fails rather than pretend when graph-service cannot be read
// (503 HIDE_KNOWN_UNAVAILABLE). Phone contacts are not part of it.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

const (
	// hideKnownRefreshAfter is how old a snapshot gets before the sweeper
	// refreshes it.
	hideKnownRefreshAfter = 24 * time.Hour
	// maxKnownConnections caps one snapshot.
	maxKnownConnections = 5000
	connectionsPageSize = 500
)

// ErrHideKnownUnavailable maps to 503 HIDE_KNOWN_UNAVAILABLE.
var ErrHideKnownUnavailable = errors.New("unavailable: your connections cannot be read right now")

// ConnectionLister lists a user's accepted connections.
type ConnectionLister interface {
	AcceptedConnections(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error)
}

// SetConnectionLister wires graph-service for the mechanic.
func (s *Service) SetConnectionLister(c ConnectionLister) { s.connectionLister = c }

// HideKnownView is GET/PUT /hide-known.
type HideKnownView struct {
	Enabled     bool       `json:"enabled"`
	HiddenCount int        `json:"hidden_count"`
	RefreshedAt *time.Time `json:"refreshed_at,omitempty"`
}

// GetHideKnown returns the caller's setting.
func (s *Service) GetHideKnown(ctx context.Context, userID uuid.UUID) (*HideKnownView, error) {
	if !s.mechanics.HideKnown {
		return nil, ErrMechanicDisabled
	}
	st, err := s.store.GetHideKnown(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := &HideKnownView{Enabled: st.Enabled, HiddenCount: st.Connections}
	if st.RefreshedAt != nil {
		t := st.RefreshedAt.UTC()
		out.RefreshedAt = &t
	}
	return out, nil
}

// PutHideKnown switches the setting.
func (s *Service) PutHideKnown(ctx context.Context, userID uuid.UUID, enabled bool) (*HideKnownView, error) {
	if !s.mechanics.HideKnown {
		return nil, ErrMechanicDisabled
	}
	if enabled {
		if err := s.snapshotConnections(ctx, userID); err != nil {
			return nil, err
		}
	} else if err := s.store.DisableHideKnown(ctx, userID); err != nil {
		return nil, err
	}
	// Both sides' cached decks may hold the other.
	s.InvalidatePulseCache(ctx, userID)
	s.InvalidateDecksForCandidate(ctx, userID)
	return s.GetHideKnown(ctx, userID)
}

func (s *Service) snapshotConnections(ctx context.Context, userID uuid.UUID) error {
	if s.connectionLister == nil {
		return ErrHideKnownUnavailable
	}
	others, err := s.connectionLister.AcceptedConnections(ctx, userID)
	if err != nil {
		slog.Warn("hide known: connections unreadable", "user_id", userID, "error", err)
		return ErrHideKnownUnavailable
	}
	return s.store.ReplaceKnownPeople(ctx, userID, others)
}

// RefreshHideKnown refreshes up to limit stale snapshots (the sweeper). A
// failed refresh keeps the old snapshot.
func (s *Service) RefreshHideKnown(ctx context.Context, limit int) (int, error) {
	if !s.mechanics.HideKnown || s.connectionLister == nil {
		return 0, nil
	}
	due, err := s.store.HideKnownDueForRefresh(ctx, hideKnownRefreshAfter, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range due {
		if err := s.snapshotConnections(ctx, id); err != nil {
			_ = s.store.TouchHideKnown(ctx, id)
			continue
		}
		s.InvalidatePulseCache(ctx, id)
		s.InvalidateDecksForCandidate(ctx, id)
		n++
	}
	return n, nil
}

type httpConnectionLister struct {
	baseURL string
	key     string
	client  *http.Client
}

// NewHTTPConnectionLister reads graph-service's connections list (internal
// key; no user identity), page by page up to maxKnownConnections.
func NewHTTPConnectionLister(baseURL, internalKey string, c *http.Client) ConnectionLister {
	if c == nil {
		c = &http.Client{Timeout: 3 * time.Second}
	}
	return &httpConnectionLister{baseURL: strings.TrimRight(baseURL, "/"), key: internalKey, client: c}
}

func (c *httpConnectionLister) AcceptedConnections(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for offset := 0; offset < maxKnownConnections; offset += connectionsPageSize {
		q := url.Values{"limit": {strconv.Itoa(connectionsPageSize)}, "offset": {strconv.Itoa(offset)}}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet,
			c.baseURL+"/v1/graph/connections/"+url.PathEscape(userID.String())+"?"+q.Encode(), nil)
		if err != nil {
			return nil, err
		}
		if c.key != "" {
			req.Header.Set(internalKeyHeader, c.key)
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("graph-service unreachable: %w", err)
		}
		var env struct {
			Data []uuid.UUID `json:"data"`
		}
		status := resp.StatusCode
		derr := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&env)
		resp.Body.Close()
		if status < 200 || status >= 300 {
			return nil, fmt.Errorf("graph-service connections status %d", status)
		}
		if derr != nil {
			return nil, fmt.Errorf("decode connections: %w", derr)
		}
		out = append(out, env.Data...)
		if len(env.Data) < connectionsPageSize {
			break
		}
	}
	return out, nil
}
