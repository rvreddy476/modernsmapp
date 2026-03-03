package policy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
// Falls back to ALLOW if graph-service is unreachable (fail-open to avoid blocking legitimate users).
func (p *DMPolicy) CanDM(ctx context.Context, userAID, userBID string) (bool, error) {
	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", p.graphServiceURL, userAID, userBID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return true, nil // fail-open
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return true, nil // fail-open: graph-service unreachable
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return true, nil // fail-open
	}

	var result struct {
		IsFriend   bool `json:"is_friend"`
		Follows    bool `json:"follows"`
		FollowedBy bool `json:"followed_by"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return true, nil // fail-open
	}

	// Circle membership = friends OR mutual follow (both follow each other)
	mutualFollow := result.Follows && result.FollowedBy
	return result.IsFriend || mutualFollow, nil
}
