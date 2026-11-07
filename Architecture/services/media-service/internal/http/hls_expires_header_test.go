package http

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// A media playlist says when its signed segment URLs stop working, so a
// player still playing a long video then re-fetches it (2026-11-07: a VOD
// playlist is never reloaded, and segments 403'd after the old 2 h cap).
func TestHLSPlaylistRouteSendsExpiresAt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	media := uuid.New()
	expires := time.Date(2026, 11, 7, 21, 30, 15, 900_000_000, time.UTC)
	pl := &chatOpenPlaylists{master: "#EXTM3U\n#EXTINF:6,\nhttps://cdn/seg_000.ts", expiresAt: expires}
	h := &Handler{heads: &chatOpenHeads{}, hlsPlaylists: pl}
	r := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, noop, noop)

	get := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/v1/media/"+media.String()+"/hls/720p.m3u8", nil)
		req.Header.Set("X-User-Id", uuid.NewString())
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}

	w := get()
	if w.Code != http.StatusOK {
		t.Fatalf("%d %q", w.Code, w.Body.String())
	}
	// Whole seconds, rounded DOWN: a client never waits past the real expiry.
	if got := w.Header().Get(HLSExpiresAtHeader); got != "2026-11-07T21:30:15Z" {
		t.Fatalf("%s = %q, want 2026-11-07T21:30:15Z", HLSExpiresAtHeader, got)
	}
	if got := w.Header().Get("Access-Control-Expose-Headers"); got != HLSExpiresAtHeader {
		t.Fatalf("Access-Control-Expose-Headers = %q: a browser player could not read the expiry", got)
	}

	// Nothing in the playlist expires (master, anonymous): no header.
	pl.expiresAt = time.Time{}
	if got := get().Header().Get(HLSExpiresAtHeader); got != "" {
		t.Fatalf("%s = %q on a playlist with nothing to expire", HLSExpiresAtHeader, got)
	}
}
