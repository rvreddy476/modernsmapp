package mediaclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestThumbnailURLPrefersImageRenditionsOnly(t *testing.T) {
	video := Asset{Kind: "video", Variants: map[string]string{
		"original": "v-original", "360p": "v-360", "720p": "v-720", "thumb_150": "poster-150",
	}}
	if got := video.ThumbnailURL(); got != "poster-150" {
		t.Fatalf("video poster = %q, want the thumb rendition, never a video file", got)
	}
	noPoster := Asset{Kind: "video", Variants: map[string]string{"original": "v-original", "360p": "v-360"}}
	if got := noPoster.ThumbnailURL(); got != "" {
		t.Fatalf("video without a poster must yield no thumbnail, got %q", got)
	}
	image := Asset{Kind: "image", Variants: map[string]string{"original": "i-orig", "small_480": "i-480", "thumb_150": "i-150"}}
	if got := image.ThumbnailURL(); got != "i-480" {
		t.Fatalf("image thumbnail = %q, want small_480", got)
	}
}

func TestResolveChunksDedupesAndDegrades(t *testing.T) {
	var calls [][]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" || r.Header.Get("X-User-Id") != "viewer" {
			t.Errorf("missing headers: %v", r.Header)
		}
		var req struct {
			IDs []string `json:"ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls = append(calls, req.IDs)
		data := map[string]any{}
		for _, id := range req.IDs {
			data[id] = map[string]any{"media_id": id, "kind": "video", "duration_ms": 12000,
				"variants": map[string]string{"thumb_150": "t-" + id}, "playback_url": "/v1/media/" + id + "/hls/master.m3u8"}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	ids := make([]string, 0, 61)
	for i := 0; i < 60; i++ {
		ids = append(ids, "m"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	ids = append(ids, ids[0]) // duplicate
	got := New(srv.URL, "k").Resolve(context.Background(), "viewer", ids)
	if len(calls) != 2 || len(calls[0]) != 50 || len(calls[1]) != 10 {
		t.Fatalf("expected two chunks of 50+10 unique ids, got %d calls: %v", len(calls), len(calls[0]))
	}
	if len(got) != 60 || got[ids[0]].DurationMs != 12000 || got[ids[0]].ThumbnailURL() != "t-"+ids[0] {
		t.Fatalf("resolved = %d assets, first = %+v", len(got), got[ids[0]])
	}

	// Unreachable media-service: empty map, no error, no panic.
	dead := New("http://127.0.0.1:1", "k").Resolve(context.Background(), "", []string{"x"})
	if len(dead) != 0 {
		t.Fatalf("dead media-service must resolve nothing, got %v", dead)
	}
	var nilClient *Client
	if n := nilClient.Resolve(context.Background(), "", []string{"x"}); len(n) != 0 {
		t.Fatal("nil client must resolve nothing")
	}
}
