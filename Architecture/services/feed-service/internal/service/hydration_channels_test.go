package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Tube channels (2026-09-05): every surface funnels through enrichRenderData,
// so proving it here proves the video feed, watch, home and the category
// pages all carry `channel` on a long video whose author has one — and
// nothing else does.
func TestEnrichRenderDataAttachesChannelToLongVideos(t *testing.T) {
	t.Setenv("INTERNAL_SERVICE_KEY", "test-internal")
	viewerID := uuid.New()
	channelAuthor := uuid.New()
	plainAuthor := uuid.New()

	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			channelAuthor.String(): map[string]any{"user_id": channelAuthor, "display_name": "Call B"},
			plainAuthor.String():   map[string]any{"user_id": plainAuthor, "display_name": "Call A"},
		})
	}))
	defer profileServer.Close()

	channelCalls := 0
	postServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/v1/channels/batch") {
			t.Errorf("unexpected post-service call %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		channelCalls++
		if got := r.Header.Get("X-User-Id"); got != viewerID.String() {
			t.Errorf("channels X-User-Id=%q want %q", got, viewerID)
		}
		if got := r.Header.Get("X-Internal-Service-Key"); got != "test-internal" {
			t.Errorf("channels internal key=%q", got)
		}
		ids := strings.Split(r.URL.Query().Get("user_ids"), ",")
		// One batch per page, containing ONLY long-video authors: the plain
		// post's author must not be looked up.
		if len(ids) != 1 || ids[0] != channelAuthor.String() {
			t.Errorf("channels user_ids=%v want [%s]", ids, channelAuthor)
		}
		avatar := "/v1/media/abc/small_480"
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
			channelAuthor.String(): map[string]any{
				"user_id": channelAuthor, "name": "Call B Studio", "handle": "call.b", "avatar_url": avatar,
			},
		}})
	}))
	defer postServer.Close()

	svc := &Service{
		profileServiceURL: profileServer.URL,
		postServiceURL:    postServer.URL,
		profileClient:     profileServer.Client(),
		postClient:        postServer.Client(),
	}
	posts := []HydratedPost{
		{ID: uuid.New(), AuthorID: channelAuthor, ContentType: "long_video"},
		{ID: uuid.New(), AuthorID: channelAuthor, ContentType: "video"}, // legacy spelling
		{ID: uuid.New(), AuthorID: channelAuthor, ContentType: "flick"},
		{ID: uuid.New(), AuthorID: plainAuthor, ContentType: "post"},
	}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}
	if channelCalls != 1 {
		t.Fatalf("channels batch called %d times, want 1", channelCalls)
	}
	for _, i := range []int{0, 1} {
		ch := posts[i].Channel
		if ch == nil || ch.UserID != channelAuthor || ch.Name != "Call B Studio" || ch.Handle != "call.b" || ch.AvatarURL == nil || *ch.AvatarURL != "/v1/media/abc/small_480" {
			t.Fatalf("post %d (%s): channel=%+v", i, posts[i].ContentType, ch)
		}
	}
	if posts[2].Channel != nil {
		t.Fatalf("flick must not carry a channel: %+v", posts[2].Channel)
	}
	if posts[3].Channel != nil {
		t.Fatalf("plain post must not carry a channel: %+v", posts[3].Channel)
	}

	// Wire shape: `channel` present on the video, absent on the flick.
	out, _ := json.Marshal(posts[0])
	if !strings.Contains(string(out), `"channel":{"user_id":"`+channelAuthor.String()+`","name":"Call B Studio","handle":"call.b","avatar_url":"/v1/media/abc/small_480"}`) {
		t.Fatalf("channel not serialized as expected: %s", out)
	}
	out, _ = json.Marshal(posts[2])
	if strings.Contains(string(out), `"channel"`) {
		t.Fatalf("flick serialized a channel: %s", out)
	}
}

// A long video by an author with no channel, and a channel outage, both
// leave the page intact: the card is decoration, not a gate.
func TestEnrichRenderDataChannelIsBestEffort(t *testing.T) {
	viewerID := uuid.New()
	author := uuid.New()
	profileServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{author.String(): map[string]any{"user_id": author, "display_name": "Call A"}})
	}))
	defer profileServer.Close()

	// 1. No channel for the author: empty batch.
	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{}})
	}))
	defer empty.Close()
	svc := &Service{profileServiceURL: profileServer.URL, postServiceURL: empty.URL, profileClient: profileServer.Client(), postClient: empty.Client()}
	posts := []HydratedPost{{ID: uuid.New(), AuthorID: author, ContentType: "long_video"}}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}
	if posts[0].Channel != nil {
		t.Fatalf("author without a channel got one: %+v", posts[0].Channel)
	}

	// 2. post-service down for channels: the page still renders.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer down.Close()
	svc = &Service{profileServiceURL: profileServer.URL, postServiceURL: down.URL, profileClient: profileServer.Client(), postClient: down.Client()}
	posts = []HydratedPost{{ID: uuid.New(), AuthorID: author, ContentType: "long_video"}}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("channel outage must not fail the page: %v", err)
	}
	if posts[0].Channel != nil || posts[0].Author.DisplayName != "Call A" {
		t.Fatalf("page degraded unexpectedly: %+v", posts[0])
	}
}
