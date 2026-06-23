//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestE2E_Social_PostFollowFeedLike walks the core social loop through the
// gateway, asserting the Kafka-driven side effects with Eventually:
//
//	B follows A → A posts → post fans out to B's feed → B likes → count rises
//
// Follow happens before the post so fan-out-on-write reaches B. Everything goes
// through the api-gateway, so this exercises the real edge (JWT verify → header
// injection → routing) too.
func TestE2E_Social_PostFollowFeedLike(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.APIGateway)

	aID := uuid.New()
	bID := uuid.New()
	a := NewHTTPClient(urls.APIGateway, aID)
	b := NewHTTPClient(urls.APIGateway, bID)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// B follows A.
	b.MustOK(t, ctx, "POST", "/v1/graph/follow", map[string]any{"user_id": aID.String()})

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

	// Fan-out to B's feed (post created → social.events → feed consumer).
	Eventually(t, 30*time.Second, "post fans out to follower feed", func() bool {
		env, err := b.Do(ctx, "GET", "/v1/feed", nil)
		return err == nil && env.Status == 200 && strings.Contains(string(env.Data), post.ID)
	})

	// B likes the post; the like count increments (sharded counter, eventually).
	b.MustOK(t, ctx, "POST", "/v1/posts/"+post.ID+"/like", nil)
	Eventually(t, 20*time.Second, "like count increments to >=1", func() bool {
		env, err := a.Do(ctx, "GET", "/v1/posts/"+post.ID, nil)
		return err == nil && env.Status == 200 && likeCount(env.Data) >= 1
	})
}

// likeCount pulls no_likes from a GetPost response, tolerating both the flat
// shape and the PostDetail { post: {...} } wrapper.
func likeCount(raw json.RawMessage) int {
	var flat struct {
		NoLikes int `json:"no_likes"`
		Post    *struct {
			NoLikes int `json:"no_likes"`
		} `json:"post,omitempty"`
	}
	if err := json.Unmarshal(raw, &flat); err != nil {
		return 0
	}
	if flat.Post != nil {
		return flat.Post.NoLikes
	}
	return flat.NoLikes
}
