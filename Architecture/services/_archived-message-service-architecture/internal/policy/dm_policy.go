package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/atpost/shared/httpclient"
)

// DMPolicy checks whether two users are allowed to exchange DMs.
// Per the follow-circle spec, DMs require mutual circle membership.
type DMPolicy struct {
	graphServiceURL string
	httpClient      *http.Client
}

func NewDMPolicy(graphServiceURL string) *DMPolicy {
	return &DMPolicy{
		graphServiceURL: graphServiceURL,
		httpClient:      httpclient.NewWithBreaker(5*time.Second, "message->graph"),
	}
}

// CanDM returns true if userA and userB are mutual circle members (friends or mutual followers).
// Falls back to DENY if graph-service is unreachable (fail-closed for security).
func (p *DMPolicy) CanDM(ctx context.Context, userAID, userBID string) (bool, error) {
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", p.graphServiceURL, userAID, userBID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		slog.Warn("dm_policy: graph service unreachable, rejecting DM", "error", err)
		return false, nil
	}
	// graph-service gates /v1/graph/* behind the internal service key —
	// without it the call 401s and CanDM wrongly denies every DM.
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		slog.Warn("dm_policy: graph service unreachable, rejecting DM", "error", err)
		return false, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		slog.Warn("dm_policy: graph service unreachable, rejecting DM", "error", fmt.Errorf("status %d", resp.StatusCode))
		return false, nil
	}

	var result struct {
		IsFriend   bool `json:"is_friend"`
		Follows    bool `json:"follows"`
		FollowedBy bool `json:"followed_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Warn("dm_policy: graph service unreachable, rejecting DM", "error", err)
		return false, nil
	}

	// Circle membership = friends OR mutual follow (both follow each other)
	mutualFollow := result.Follows && result.FollowedBy
	return result.IsFriend || mutualFollow, nil
}
