package service

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
)

// HydratedPost is decoded from post-service's batch response by field name, so
// any field it does not declare is silently dropped between post-service and
// the feed item Android reads. This pins the per-reel controls, the category,
// the tagged people and the location all the way through: decode what
// post-service emits, re-encode what the feed emits, and check the keys are
// still there with the values post-service sent.
func TestHydratedPostPassesReelControlsThrough(t *testing.T) {
	postID := uuid.New()
	author := uuid.New()
	tagged := uuid.New()
	cover := uuid.New()

	// Shaped like one entry of POST /v1/posts/batch's `data` map, including
	// fields the feed does not care about, which must not break decoding.
	upstream := map[string]any{
		"id": postID, "author_id": author, "text": "cat test #fun",
		"visibility": "public", "content_type": "flick", "post_type": "video",
		"no_comments":     true,
		"hide_share":      true,
		"allow_download":  false,
		"remix_setting":   "disallow",
		"category":        "comedy",
		"tags":            []string{"cats"},
		"hashtags":        []string{"fun"},
		"cover_media_id":  cover,
		"tagged_user_ids": []uuid.UUID{tagged},
		"location_name":   "Hyderabad",
		"location_lat":    17.385,
		"location_lng":    78.4867,
		"title":           "",
		"seo_title":       "ignored by the feed",
		"created_at":      "2026-09-04T10:00:00Z",
		"updated_at":      "2026-09-04T10:00:00Z",
	}
	raw, err := json.Marshal(upstream)
	if err != nil {
		t.Fatalf("marshal upstream: %v", err)
	}

	var hp HydratedPost
	if err := json.Unmarshal(raw, &hp); err != nil {
		t.Fatalf("decode as HydratedPost: %v", err)
	}

	out, err := json.Marshal(hp)
	if err != nil {
		t.Fatalf("marshal feed item: %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(out, &item); err != nil {
		t.Fatalf("decode feed item: %v", err)
	}

	want := map[string]any{
		"no_comments":    true,
		"hide_share":     true,
		"allow_download": false,
		"remix_setting":  "disallow",
		"category":       "comedy",
		"cover_media_id": cover.String(),
		"location_name":  "Hyderabad",
		"location_lat":   17.385,
		"location_lng":   78.4867,
	}
	for key, val := range want {
		got, ok := item[key]
		if !ok {
			t.Errorf("feed item is missing %q", key)
			continue
		}
		if got != val {
			t.Errorf("%s = %v, want %v", key, got, val)
		}
	}
	for key, wantOne := range map[string]string{
		"tags":            "cats",
		"hashtags":        "fun",
		"tagged_user_ids": tagged.String(),
	} {
		list, ok := item[key].([]any)
		if !ok || len(list) != 1 || list[0] != wantOne {
			t.Errorf("%s = %v, want [%s]", key, item[key], wantOne)
		}
	}
	if _, leaked := item["seo_title"]; leaked {
		t.Error("seo_title is not part of the feed contract and must not leak through")
	}
}

// The defaults every pre-existing post carries must come out as explicit
// values, never omitted: a client cannot tell "downloads allowed" from
// "field missing" if a false/true switch is omitempty.
func TestHydratedPostAlwaysEmitsSwitches(t *testing.T) {
	out, err := json.Marshal(HydratedPost{ID: uuid.New(), AllowDownload: true})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var item map[string]any
	if err := json.Unmarshal(out, &item); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, key := range []string{"no_comments", "hide_share", "allow_download"} {
		if _, ok := item[key]; !ok {
			t.Errorf("%q must always be present on a feed item", key)
		}
	}
	if item["allow_download"] != true || item["hide_share"] != false {
		t.Errorf("defaults wrong: allow_download=%v hide_share=%v", item["allow_download"], item["hide_share"])
	}
}
