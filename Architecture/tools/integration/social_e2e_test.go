//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// homeFeedPath is the home feed as the gateway serves it: the /v1/feed prefix
// goes to feed-service, whose route is GET /v1/feed/home (feed-service
// internal/http/handler.go). This flow used to poll GET /v1/feed, which is not
// a route: every poll was a 404, so the flow could never have passed
// (finding feed-13). chronological is the mode that needs no ranking inputs.
const homeFeedPath = "/v1/feed/home?feed_mode=chronological&limit=50"

// TestE2E_Social_PostFollowFeedLike walks the core social loop through the
// gateway, asserting the Kafka-driven side effects with Eventually:
//
//	B follows A → A posts → the post reaches B's home feed → B likes → count rises
//
// Follow happens before the post so fan-out-on-write reaches B. Everything goes
// through the api-gateway, so this exercises the real edge (JWT verify → header
// injection → routing) too.
//
// A and B must be real rows in user-service's users table: graph-service
// answers 400 WRONG_ENTITY_TYPE to a follow of an id it has never heard of.
// The e2e workflow seeds two and passes their ids (E2EUsers).
func TestE2E_Social_PostFollowFeedLike(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)
	aID, bID := E2EUsers(t)

	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// B follows A. A public account answers "followed"; anything else
	// ("requested") means no edge exists and the post would never fan out.
	var follow struct {
		Status string `json:"status"`
	}
	followRaw := b.MustOK(t, ctx, "POST", "/v1/graph/follow", map[string]any{"user_id": aID.String()})
	if err := json.Unmarshal(followRaw, &follow); err != nil || follow.Status != "followed" {
		t.Fatalf("follow: status %q, want \"followed\" (err=%v raw=%s)", follow.Status, err, string(followRaw))
	}

	// A creates a public post.
	postRaw := a.MustOK(t, ctx, "POST", "/v1/posts", map[string]any{
		"text":         "E2E social flow " + uuid.NewString(),
		"visibility":   "public",
		"content_type": "post",
	})
	var post struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(postRaw, &post); err != nil || post.ID == "" {
		t.Fatalf("create post: id missing (err=%v raw=%s)", err, string(postRaw))
	}

	// The post reaches B's home feed. The last answer is kept so a timeout
	// says what the feed actually returned instead of only "not satisfied".
	lastFeed := "no request completed"
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("last GET %s: %s", homeFeedPath, lastFeed)
		}
	})
	Eventually(t, 45*time.Second, "post reaches the follower's home feed", func() bool {
		env, err := b.Do(ctx, "GET", homeFeedPath, nil)
		if err != nil {
			lastFeed = err.Error()
			return false
		}
		ids, err := feedPostIDs(env.Data)
		lastFeed = fmt.Sprintf("status %d, %d items, decode error %v, error %s", env.Status, len(ids), err, envelopeError(env))
		if env.Status != 200 || err != nil {
			return false
		}
		for _, id := range ids {
			if id == post.ID {
				return true
			}
		}
		return false
	})

	// B likes the post; the like count increments (sharded counter, eventually).
	b.MustOK(t, ctx, "POST", "/v1/posts/"+post.ID+"/like", nil)
	Eventually(t, 20*time.Second, "like count increments to >=1", func() bool {
		env, err := a.Do(ctx, "GET", "/v1/posts/"+post.ID, nil)
		return err == nil && env.Status == 200 && likeCount(env.Data) >= 1
	})
}

// feedPostIDs reads the ids out of a home feed page. The page is the envelope's
// data: a bare JSON array of hydrated posts, each with "id" (feed-service
// service.HydratedPost); the cursor travels in meta.
func feedPostIDs(raw json.RawMessage) ([]string, error) {
	var items []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	return ids, nil
}

func envelopeError(env *Envelope) string {
	if env == nil || env.Error == nil {
		return "none"
	}
	return env.Error.Code + ": " + env.Error.Message
}

// likeCount pulls the like count from a GetPost response: counts.likes on
// post-service's PostDetail. It used to read no_likes, which is the author's
// "hide likes" switch (a boolean), so it always answered 0.
func likeCount(raw json.RawMessage) int64 {
	var detail struct {
		Counts *struct {
			Likes int64 `json:"likes"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(raw, &detail); err != nil || detail.Counts == nil {
		return 0
	}
	return detail.Counts.Likes
}
