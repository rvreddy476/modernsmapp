//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestMembershipFlow walks the Tier 3c happy path end-to-end at the
// HTTP layer:
//
//  1. author creates a tier  — POST /v1/monetization/tiers
//  2. author creates a post  — POST /v1/posts
//  3. fan reads it           — GET  /v1/posts/:id        (body visible)
//  4. author gates it        — PUT  /v1/posts/:id/membership
//  5. fan reads again        — GET  /v1/posts/:id        (body redacted)
//  6. fan subscribes         — POST /v1/monetization/subscribe/:author
//  7. fan reads once more    — GET  /v1/posts/:id        (body un-redacted)
//
// Step 7 also exercises the Tier 1a entitlement cache + the Kafka
// invalidator: subscribing in step 6 publishes entitlement.changed,
// post-service consumes and drops the cached "denied" answer, so
// the very next read returns un-redacted. The eventual-consistency
// gap is bounded by Kafka delivery latency; we retry briefly to
// absorb that.
func TestMembershipFlow(t *testing.T) {
	SkipIfNotIntegration(t)
	urls := LoadServiceURLs()
	SkipIfDown(t, urls.Post, urls.Monetization)

	authorID := uuid.New()
	fanID := uuid.New()

	author := NewHTTPClient(urls.Monetization, authorID)
	authorPost := NewHTTPClient(urls.Post, authorID)
	fan := NewHTTPClient(urls.Post, fanID)
	fanMon := NewHTTPClient(urls.Monetization, fanID)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// 1. Author creates a tier.
	tierResp := author.MustOK(t, ctx, "POST", "/v1/monetization/tiers", map[string]interface{}{
		"name":         "Supporters",
		"price":        9.99,
		"currency":     "INR",
		"perks":        []string{"Members-only posts"},
	})
	var tier struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(tierResp, &tier); err != nil {
		t.Fatalf("decode tier: %v", err)
	}
	if tier.ID == "" {
		t.Fatalf("tier created without ID")
	}

	// 2. Author creates a post.
	postResp := authorPost.MustOK(t, ctx, "POST", "/v1/posts", map[string]interface{}{
		"text":         "Members-only behind-the-scenes",
		"visibility":   "public",
		"content_type": "long_video",
	})
	var post struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(postResp, &post); err != nil {
		t.Fatalf("decode post: %v", err)
	}
	if post.ID == "" {
		t.Fatalf("post created without ID")
	}

	// 3. Fan reads it — body should be visible.
	body := readPost(t, ctx, fan, post.ID)
	if body.BodyRedacted {
		t.Errorf("public post should not be redacted before gating")
	}
	if body.Text == "" {
		t.Errorf("public post body should not be empty before gating")
	}

	// 4. Author gates the post.
	authorPost.MustOK(t, ctx, "PUT", "/v1/posts/"+post.ID+"/membership", map[string]interface{}{
		"tier_required_id": tier.ID,
	})

	// 5. Fan reads — body should now be redacted.
	body = readPost(t, ctx, fan, post.ID)
	if !body.BodyRedacted {
		t.Errorf("gated post should be redacted for non-subscriber, got body_redacted=false (text=%q)", body.Text)
	}

	// 6. Fan subscribes.
	fanMon.MustOK(t, ctx, "POST", "/v1/monetization/subscribe/"+authorID.String(), map[string]interface{}{
		"tier_id": tier.ID,
	})

	// 7. Fan reads once more — body should un-redact within a few
	// seconds (Kafka delivery + cache invalidation).
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		body = readPost(t, ctx, fan, post.ID)
		if !body.BodyRedacted {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Errorf("after subscribing, body should un-redact within 15s; still redacted (cache invalidator wired?)")
}

type postBody struct {
	BodyRedacted   bool   `json:"body_redacted"`
	TierRequiredID string `json:"tier_required_id"`
	Text           string `json:"text"`
	AuthorID       string `json:"author_id"`
}

func readPost(t *testing.T, ctx context.Context, c *HTTPClient, postID string) postBody {
	t.Helper()
	raw := c.MustOK(t, ctx, "GET", "/v1/posts/"+postID, nil)
	// PostDetail wraps Post; both shapes are valid here. Try
	// flattening the embedded Post first.
	var pd struct {
		postBody
		Post *postBody `json:"post,omitempty"`
	}
	if err := json.Unmarshal(raw, &pd); err != nil {
		t.Fatalf("decode post: %v (raw: %s)", err, string(raw))
	}
	if pd.Post != nil && pd.Post.AuthorID != "" {
		return *pd.Post
	}
	return pd.postBody
}
