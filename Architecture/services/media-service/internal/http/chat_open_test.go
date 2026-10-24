package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Hidden chats on the real router: `chat_open` on a serve route leaves the
// URL before anything logs it, and reaches the delivery decision in the
// request context with the viewer's session id (or device id).

type chatOpenHeads struct {
	opens []delivery.ChatOpen
	seen  int
}

func (f *chatOpenHeads) HeadForViewer(ctx context.Context, _, _ uuid.UUID, _ string) (*service.ServeHead, error) {
	f.seen++
	if open, ok := delivery.ChatOpenFrom(ctx); ok {
		f.opens = append(f.opens, open)
	}
	return nil, delivery.ErrDeliveryDenied
}

const chatOpenSecret = "chat-open-test-secret"

type chatOpenRig struct {
	router *gin.Engine
	heads  *chatOpenHeads
	// logged is the request URL as an access logger running around the
	// whole chain sees it once the handler has run.
	logged []string
}

func newChatOpenRig(t *testing.T, withKeys bool) *chatOpenRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rig := &chatOpenRig{heads: &chatOpenHeads{}}
	h := &Handler{heads: rig.heads}
	if withKeys {
		h.WithSessionKeys(JWTKeySet{ActiveKID: "v1", ActiveSecret: chatOpenSecret})
	}
	rig.router = gin.New()
	rig.router.Use(func(c *gin.Context) {
		c.Next()
		rig.logged = append(rig.logged, c.Request.URL.String()+" "+c.Request.RequestURI)
	})
	noop := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(rig.router, noop, noop)
	return rig
}

func chatToken(t *testing.T, sub, sid string) string {
	t.Helper()
	claims := map[string]any{"sub": sub, "exp": time.Now().Add(time.Hour).Unix()}
	if sid != "" {
		claims["sid"] = sid
	}
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"}, claims, chatOpenSecret)
}

func (rig *chatOpenRig) head(t *testing.T, query string, headers map[string]string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodHead, "/v1/media/"+uuid.NewString()+"/serve"+query, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rig.router.ServeHTTP(httptest.NewRecorder(), req)
}

func TestChatOpenLeavesTheURLAndCarriesTheSession(t *testing.T) {
	rig := newChatOpenRig(t, true)
	viewer := uuid.NewString()
	rig.head(t, "?chat_open=open-ok&w=480", map[string]string{
		"X-User-Id":     viewer,
		"Authorization": "Bearer " + chatToken(t, viewer, "sid-1"),
		"X-Device-Id":   "dev-9",
	})
	if len(rig.heads.opens) != 1 {
		t.Fatalf("decision saw %d open tokens, want 1", len(rig.heads.opens))
	}
	if got := rig.heads.opens[0]; got.Token != "open-ok" || got.SessionID != "sid-1" || got.DeviceID != "" {
		t.Fatalf("open = %+v", got)
	}
	for _, line := range rig.logged {
		if strings.Contains(line, "open-ok") || strings.Contains(line, ChatOpenQueryParam) {
			t.Fatalf("the open token is still on the logged URL: %s", line)
		}
		if !strings.Contains(line, "w=480") {
			t.Fatalf("other query parameters were lost: %s", line)
		}
	}
}

func TestChatOpenBindsTheDeviceWhenTheTokenHasNoSession(t *testing.T) {
	viewer := uuid.NewString()
	for name, headers := range map[string]map[string]string{
		"token without sid": {"X-User-Id": viewer, "Authorization": "Bearer " + chatToken(t, viewer, ""), "X-Device-Id": "dev-9"},
		// Someone else's token cannot lend its session to this viewer.
		"another user's token": {"X-User-Id": viewer, "Authorization": "Bearer " + chatToken(t, uuid.NewString(), "sid-x"), "X-Device-Id": "dev-9"},
		"forged token":         {"X-User-Id": viewer, "Authorization": "Bearer " + chatToken(t, viewer, "sid-1") + "x", "X-Device-Id": "dev-9"},
		"no token":             {"X-User-Id": viewer, "X-Device-Id": "dev-9"},
	} {
		rig := newChatOpenRig(t, true)
		rig.head(t, "?chat_open=open-ok", headers)
		if len(rig.heads.opens) != 1 {
			t.Fatalf("%s: %d open tokens", name, len(rig.heads.opens))
		}
		if got := rig.heads.opens[0]; got.SessionID != "" || got.DeviceID != "dev-9" {
			t.Fatalf("%s: open = %+v, want the device binding only", name, got)
		}
	}
	// Without session keys configured there is no sid to read.
	rig := newChatOpenRig(t, false)
	rig.head(t, "?chat_open=open-ok", map[string]string{"X-User-Id": viewer, "Authorization": "Bearer " + chatToken(t, viewer, "sid-1"), "X-Device-Id": "dev-9"})
	if got := rig.heads.opens[0]; got.SessionID != "" || got.DeviceID != "dev-9" {
		t.Fatalf("no keys: open = %+v", got)
	}
}

func TestChatOpenCarriesNothingWhenItCannotApply(t *testing.T) {
	viewer := uuid.NewString()
	for name, tc := range map[string]struct {
		query   string
		headers map[string]string
	}{
		"signed out":  {"?chat_open=open-ok", nil},
		"empty value": {"?chat_open=", map[string]string{"X-User-Id": viewer}},
		"oversized":   {"?chat_open=" + strings.Repeat("a", delivery.MaxChatOpenTokenLength+1), map[string]string{"X-User-Id": viewer}},
		"absent":      {"?w=480", map[string]string{"X-User-Id": viewer}},
	} {
		rig := newChatOpenRig(t, true)
		rig.head(t, tc.query, tc.headers)
		if rig.heads.seen != 1 {
			t.Fatalf("%s: the route did not run", name)
		}
		if len(rig.heads.opens) != 0 {
			t.Fatalf("%s: carried %+v", name, rig.heads.opens)
		}
		for _, line := range rig.logged {
			if strings.Contains(line, ChatOpenQueryParam) {
				t.Fatalf("%s: chat_open still on the URL: %s", name, line)
			}
		}
	}
}

func TestCarryChatOpenOnHLSMasterChildren(t *testing.T) {
	gin.SetMode(gin.TestMode)
	media := uuid.New()
	master := "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n/v1/media/" + media.String() + "/hls/720p.m3u8\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=2\n/v1/media/" + uuid.NewString() + "/hls/x.m3u8\nhttps://cdn.example/seg0.ts"
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if got := string(carryChatOpen(c, media, []byte(master))); got != master {
		t.Fatalf("without a token the playlist changed:\n%s", got)
	}
	c.Request = c.Request.WithContext(delivery.WithChatOpen(c.Request.Context(), delivery.ChatOpen{Token: "a b+c"}))
	got := string(carryChatOpen(c, media, []byte(master)))
	if !strings.Contains(got, "/v1/media/"+media.String()+"/hls/720p.m3u8?chat_open=a+b%2Bc\n") {
		t.Fatalf("child playlist lost the token:\n%s", got)
	}
	if strings.Count(got, "chat_open") != 1 {
		t.Fatalf("token added outside this asset's child playlists:\n%s", got)
	}
}

type chatOpenPlaylists struct {
	master string
	opens  []delivery.ChatOpen
}

func (f *chatOpenPlaylists) GetHLSPlaylist(ctx context.Context, _, _ uuid.UUID, _ string) ([]byte, error) {
	if open, ok := delivery.ChatOpenFrom(ctx); ok {
		f.opens = append(f.opens, open)
	}
	return []byte(f.master), nil
}

func TestHLSMasterRouteCarriesChatOpenToChildren(t *testing.T) {
	gin.SetMode(gin.TestMode)
	media := uuid.New()
	pl := &chatOpenPlaylists{master: "#EXTM3U\n/v1/media/" + media.String() + "/hls/720p.m3u8"}
	h := &Handler{heads: &chatOpenHeads{}, hlsPlaylists: pl}
	r := gin.New()
	noop := func(c *gin.Context) { c.Next() }
	h.RegisterRoutes(r, noop, noop)

	viewer := uuid.NewString()
	req := httptest.NewRequest(http.MethodGet, "/v1/media/"+media.String()+"/hls/master.m3u8?chat_open=open-ok", nil)
	req.Header.Set("X-User-Id", viewer)
	req.Header.Set("X-Device-Id", "dev-9")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "/hls/720p.m3u8?chat_open=open-ok") {
		t.Fatalf("master: %d %q", w.Code, w.Body.String())
	}
	if len(pl.opens) != 1 || pl.opens[0].DeviceID != "dev-9" {
		t.Fatalf("the playlist read did not see the open token: %+v", pl.opens)
	}

	// Without chat_open the master is served as stored.
	req = httptest.NewRequest(http.MethodGet, "/v1/media/"+media.String()+"/hls/master.m3u8", nil)
	req.Header.Set("X-User-Id", viewer)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if strings.Contains(w.Body.String(), "chat_open") {
		t.Fatalf("token added to a request without one: %q", w.Body.String())
	}
}
