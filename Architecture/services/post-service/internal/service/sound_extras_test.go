package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

// The sound page's cover and trending badge (trending sounds, 2026-10-24):
// asked of media-service, best-effort. A fault is two nulls, never a failed
// page and never a made-up badge.

func TestSoundPageExtrasFromMediaService(t *testing.T) {
	sound := uuid.New()
	var path, key, user string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, key, user = r.URL.Path, r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-User-Id")
		_, _ = w.Write([]byte(`{"data":{"trending":{"rank":4,"rising":true},"cover_url":"/v1/media/m/serve/thumb_150"}}`))
	}))
	defer srv.Close()
	svc := &Service{mediaServiceURL: srv.URL + "/", internalServiceKey: "k", httpClient: http.DefaultClient}
	got := svc.soundPageExtras(context.Background(), sound)
	if path != "/v1/media/internal/sounds/"+sound.String()+"/extras" || key != "k" || user != "" {
		t.Fatalf("asked %s key %q user %q", path, key, user)
	}
	if got.Trending == nil || got.Trending.Rank != 4 || !got.Trending.Rising || got.CoverURL == nil || *got.CoverURL != "/v1/media/m/serve/thumb_150" {
		t.Fatalf("extras %+v", got)
	}
}

func TestSoundPageExtrasFaultsAreNulls(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"503":             {http.StatusServiceUnavailable, `{"error":{"code":"SOUND_EXTRAS_UNAVAILABLE"}}`},
		"503 with a body": {http.StatusServiceUnavailable, `{"data":{"trending":{"rank":1,"rising":true},"cover_url":"x"}}`},
		"404":             {http.StatusNotFound, `{"error":{"code":"NOT_FOUND"}}`},
		"no data":         {http.StatusOK, `{}`},
		"not json":        {http.StatusOK, `nope`},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			svc := &Service{mediaServiceURL: srv.URL, httpClient: http.DefaultClient}
			if got := svc.soundPageExtras(context.Background(), uuid.New()); got.Trending != nil || got.CoverURL != nil {
				t.Fatalf("extras %+v, want nulls", got)
			}
		})
	}
	if got := (&Service{}).soundPageExtras(context.Background(), uuid.New()); got.Trending != nil || got.CoverURL != nil {
		t.Fatalf("no media-service: %+v", got)
	}
}
