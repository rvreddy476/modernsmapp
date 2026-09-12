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

// Tube channels (2026-09-05, widened 2026-09-12): every surface funnels
// through enrichRenderData, so proving it here proves the video feed, watch,
// home, reels and the category pages all carry `channel` on ANY row whose
// author has a channel, whatever the post kind. The reels overlay offers
// Subscribe (follow + notify) instead of Follow when a row carries one, so
// a flick by a channel owner must carry it exactly like that owner's long
// video does; a row by an author with no channel keeps the field absent.
func TestEnrichRenderDataAttachesChannelWhereverAuthorHasOne(t *testing.T) {
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
		// One batch per page, every distinct author on the page exactly
		// once: the channel is no longer gated on the post kind, so the
		// plain post's author is looked up too (and found to have none).
		got := map[string]int{}
		for _, id := range ids {
			got[id]++
		}
		if len(ids) != 2 || got[channelAuthor.String()] != 1 || got[plainAuthor.String()] != 1 {
			t.Errorf("channels user_ids=%v want one of each of [%s %s]", ids, channelAuthor, plainAuthor)
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
		{ID: uuid.New(), AuthorID: channelAuthor, ContentType: "reel"},
		{ID: uuid.New(), AuthorID: channelAuthor, ContentType: "post"},
		{ID: uuid.New(), AuthorID: plainAuthor, ContentType: "flick"},
		{ID: uuid.New(), AuthorID: plainAuthor, ContentType: "post"},
	}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}
	if channelCalls != 1 {
		t.Fatalf("channels batch called %d times, want 1", channelCalls)
	}
	// Long videos are unchanged by the widening, and the short-form and
	// plain rows by the same author now carry the identical card.
	for _, i := range []int{0, 1, 2, 3, 4} {
		ch := posts[i].Channel
		if ch == nil || ch.UserID != channelAuthor || ch.Name != "Call B Studio" || ch.Handle != "call.b" || ch.AvatarURL == nil || *ch.AvatarURL != "/v1/media/abc/small_480" {
			t.Fatalf("post %d (%s): channel=%+v", i, posts[i].ContentType, ch)
		}
	}
	if posts[5].Channel != nil {
		t.Fatalf("flick by an author without a channel must not carry one: %+v", posts[5].Channel)
	}
	if posts[6].Channel != nil {
		t.Fatalf("plain post by an author without a channel must not carry one: %+v", posts[6].Channel)
	}

	// Wire shape: `channel` present on the video and on the channel
	// owner's flick, absent on the flick whose author has none.
	want := `"channel":{"user_id":"` + channelAuthor.String() + `","name":"Call B Studio","handle":"call.b","avatar_url":"/v1/media/abc/small_480"}`
	for _, i := range []int{0, 2} {
		out, _ := json.Marshal(posts[i])
		if !strings.Contains(string(out), want) {
			t.Fatalf("post %d (%s) channel not serialized as expected: %s", i, posts[i].ContentType, out)
		}
	}
	out, _ := json.Marshal(posts[5])
	if strings.Contains(string(out), `"channel"`) {
		t.Fatalf("flick by an author without a channel serialized one: %s", out)
	}
}

// A row by an author with no channel, and a channel outage, both leave the
// page intact: the card is decoration, not a gate. Now that every row is
// eligible, a flick and a long video are asserted side by side so the
// outage path is proven for the reels feed as well as the video feed.
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
	posts := []HydratedPost{
		{ID: uuid.New(), AuthorID: author, ContentType: "long_video"},
		{ID: uuid.New(), AuthorID: author, ContentType: "flick"},
	}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("enrichRenderData: %v", err)
	}
	for i := range posts {
		if posts[i].Channel != nil {
			t.Fatalf("post %d (%s): author without a channel got one: %+v", i, posts[i].ContentType, posts[i].Channel)
		}
	}

	// 2. post-service down for channels: the page still renders.
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusBadGateway)
	}))
	defer down.Close()
	svc = &Service{profileServiceURL: profileServer.URL, postServiceURL: down.URL, profileClient: profileServer.Client(), postClient: down.Client()}
	posts = []HydratedPost{
		{ID: uuid.New(), AuthorID: author, ContentType: "long_video"},
		{ID: uuid.New(), AuthorID: author, ContentType: "flick"},
	}
	if err := svc.enrichRenderData(context.Background(), posts, viewerID); err != nil {
		t.Fatalf("channel outage must not fail the page: %v", err)
	}
	for i := range posts {
		if posts[i].Channel != nil || posts[i].Author.DisplayName != "Call A" {
			t.Fatalf("post %d (%s) degraded unexpectedly: %+v", i, posts[i].ContentType, posts[i])
		}
	}
}
