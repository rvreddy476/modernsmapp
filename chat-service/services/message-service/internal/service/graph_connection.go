package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ensureGraphConnection asks graph-service to make sender and receiver
// connections after a message request is accepted.
//
// WHY CHAT DOES THIS. The founder retired the standalone friend-request
// flow: the way two people become connections is now that one messages the
// other and the other accepts. Connections still power group invites,
// close friends and presence disclosure, so the edge has to exist — chat
// is simply where it is minted now.
//
// Best-effort by design: the request is already accepted and the outbox
// event already queued when this runs, so a graph blip must not fail the
// accept. It is logged, and the edge is reconcilable from the event.
func (s *Service) ensureGraphConnection(ctx context.Context, a, b uuid.UUID) error {
	if s.graphServiceURL == "" {
		return fmt.Errorf("GRAPH_SERVICE_URL not configured")
	}
	body, err := json.Marshal(map[string]any{"user_a": a, "user_b": b})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.graphServiceURL+"/v1/internal/graph/connections/ensure", strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if s.internalServiceKey != "" {
		req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	}
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("ensure connection returned %d: %s", resp.StatusCode, string(respBody))
	}
	return nil
}
