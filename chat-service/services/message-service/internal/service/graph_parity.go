package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// Graph lookups for chat parity (Notes audience, §5). Both calls are
// internal-key gated reads graph-service already serves.

// graphRelationship is the slice of graph's batch Relationship chat reads.
type graphRelationship struct {
	Follows             bool `json:"follows"`
	FollowedBy          bool `json:"followed_by"`
	IsConnection        bool `json:"is_connection"`
	ViewerBlockedTarget bool `json:"viewer_blocked_target"`
	TargetBlockedViewer bool `json:"target_blocked_viewer"`
	Blocked             bool `json:"blocked"`
	BlockedBy           bool `json:"blocked_by"`
}

func (r graphRelationship) blocked() bool {
	return r.ViewerBlockedTarget || r.TargetBlockedViewer || r.Blocked || r.BlockedBy
}

// friends is the Notes "friends" audience: a mutual follow or a connection.
func (r graphRelationship) friends() bool {
	return r.IsConnection || (r.Follows && r.FollowedBy)
}

const graphRelationshipBatch = 100

var errGraphUnavailable = errors.New("graph-service unavailable")

func (s *Service) graphDo(ctx context.Context, method, path string, body any, out any) error {
	if s.graphServiceURL == "" || s.httpClient == nil {
		return errGraphUnavailable
	}
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	reqCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, s.graphServiceURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", errGraphUnavailable, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", errGraphUnavailable, resp.StatusCode)
	}
	return json.Unmarshal(raw, out)
}

// graphFollowingIDs is GET /v1/graph/:userId/following-ids (≤500).
func (s *Service) graphFollowingIDs(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	var env struct {
		Data struct {
			Items []uuid.UUID `json:"items"`
		} `json:"data"`
	}
	if err := s.graphDo(ctx, http.MethodGet, "/v1/graph/"+userID.String()+"/following-ids?limit=500", nil, &env); err != nil {
		return nil, err
	}
	return env.Data.Items, nil
}

// graphRelationships is POST /v1/graph/relationships/batch, chunked by 100.
// Any failure fails the whole lookup: callers treat that as "not visible".
func (s *Service) graphRelationships(ctx context.Context, viewerID uuid.UUID, targets []uuid.UUID) (map[uuid.UUID]graphRelationship, error) {
	out := map[uuid.UUID]graphRelationship{}
	for start := 0; start < len(targets); start += graphRelationshipBatch {
		end := start + graphRelationshipBatch
		if end > len(targets) {
			end = len(targets)
		}
		ids := make([]string, 0, end-start)
		for _, t := range targets[start:end] {
			ids = append(ids, t.String())
		}
		page := map[uuid.UUID]graphRelationship{}
		if err := s.graphDo(ctx, http.MethodPost, "/v1/graph/relationships/batch",
			map[string]any{"viewer_id": viewerID.String(), "target_ids": ids}, &page); err != nil {
			return nil, err
		}
		for k, v := range page {
			out[k] = v
		}
	}
	return out, nil
}
