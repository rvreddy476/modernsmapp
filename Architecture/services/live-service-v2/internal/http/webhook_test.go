package http

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/service"
	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

const (
	whKey    = "APIwebhookkey"
	whSecret = "webhooksecret0000000000000000000000"
)

var whNow = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

// signLiveKit signs body the way LiveKit's URLNotifier.send does
// (github.com/livekit/protocol webhook/url_notifier.go):
// auth.NewAccessToken(key, secret).SetValidFor(5m).SetSha256(b64).ToJWT() —
// a go-jose HS256 JWT, typ JWT, claims iss/nbf/exp plus the grant's
// "sha256" = base64.StdEncoding(sha256(body)).
func signLiveKit(t *testing.T, key, secret string, body []byte, now time.Time, mutate func(map[string]any)) string {
	t.Helper()
	sum := sha256.Sum256(body)
	claims := map[string]any{
		"iss":    key,
		"nbf":    now.Unix(),
		"exp":    now.Add(5 * time.Minute).Unix(),
		"sha256": base64.StdEncoding.EncodeToString(sum[:]),
	}
	header := map[string]any{"alg": "HS256", "typ": "JWT"}
	if mutate != nil {
		mutate(claims)
		if alg, ok := claims["__alg"]; ok {
			header["alg"] = alg
			delete(claims, "__alg")
		}
	}
	hb, _ := json.Marshal(header)
	cb, _ := json.Marshal(claims)
	enc := base64.RawURLEncoding
	in := enc.EncodeToString(hb) + "." + enc.EncodeToString(cb)
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(in))
	return in + "." + enc.EncodeToString(mac.Sum(nil))
}

// A protojson-shaped WebhookEvent body (lowerCamelCase, int64 as strings).
func trackPublishedBody(room, identity string) []byte {
	return []byte(`{"event":"track_published","room":{"sid":"RM_x","name":"` + room + `","creationTime":"1790000000"},` +
		`"participant":{"sid":"PA_x","identity":"` + identity + `","state":"ACTIVE","tracks":[{"sid":"TR_v","type":"VIDEO"}]},` +
		`"track":{"sid":"TR_v","type":"VIDEO","source":"CAMERA"},"id":"EV_` + uuid.NewString() + `","createdAt":"1790000000"}`)
}

func TestVerifyLiveKitWebhook(t *testing.T) {
	body := trackPublishedBody("stream_x", "host")
	good := signLiveKit(t, whKey, whSecret, body, whNow, nil)
	cases := []struct {
		name   string
		header string
		body   []byte
		key    string
		secret string
		want   error
	}{
		{"valid", good, body, whKey, whSecret, nil},
		{"valid with Bearer prefix", "Bearer " + good, body, whKey, whSecret, nil},
		{"wrong secret", signLiveKit(t, whKey, "another-secret", body, whNow, nil), body, whKey, whSecret, ErrWebhookSignature},
		{"body tampered", good, append(append([]byte{}, body...), ' '), whKey, whSecret, ErrWebhookChecksum},
		{"missing header", "", body, whKey, whSecret, ErrWebhookNoAuth},
		{"unset secret", good, body, whKey, "", ErrWebhookNotConfigured},
		{"unset key", good, body, "", whSecret, ErrWebhookNotConfigured},
		{"other issuer", signLiveKit(t, "otherkey", whSecret, body, whNow, nil), body, whKey, whSecret, ErrWebhookIssuer},
		{"expired", signLiveKit(t, whKey, whSecret, body, whNow.Add(-7*time.Minute), nil), body, whKey, whSecret, ErrWebhookExpired},
		{"not yet valid", signLiveKit(t, whKey, whSecret, body, whNow.Add(3*time.Minute), nil), body, whKey, whSecret, ErrWebhookExpired},
		{"no exp", signLiveKit(t, whKey, whSecret, body, whNow, func(c map[string]any) { delete(c, "exp") }), body, whKey, whSecret, ErrWebhookExpired},
		{"no sha256", signLiveKit(t, whKey, whSecret, body, whNow, func(c map[string]any) { delete(c, "sha256") }), body, whKey, whSecret, ErrWebhookChecksum},
		{"alg none", signLiveKit(t, whKey, whSecret, body, whNow, func(c map[string]any) { c["__alg"] = "none" }), body, whKey, whSecret, ErrWebhookAlg},
		{"old X-LiveKit-Signature style hex", "deadbeef", body, whKey, whSecret, ErrWebhookMalformed},
	}
	for _, tc := range cases {
		err := VerifyLiveKitWebhook(tc.header, tc.body, tc.key, tc.secret, whNow)
		if !errors.Is(err, tc.want) {
			t.Errorf("%s: got %v want %v", tc.name, err, tc.want)
		}
	}
}

type whRig struct {
	r     *gin.Engine
	store *storetest.MemStore
	host  uuid.UUID
	st    *postgres.LiveStream
}

func newWHRig(t *testing.T, key, secret string) *whRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	store := storetest.New()
	host := uuid.New()
	st := store.AddStreamStatus(host, postgres.StatusStarting)
	svc := service.New(store, nil, nil, nil, service.Config{PilotUserIDs: []uuid.UUID{host}})
	h := New(svc).WithWebhookCredentials(key, secret)
	h.clock = func() time.Time { return whNow }
	r := gin.New()
	h.RegisterRoutes(r)
	return &whRig{r: r, store: store, host: host, st: st}
}

func (w *whRig) post(header string, body []byte) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, WebhookPath, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/webhook+json")
	if header != "" {
		req.Header.Set("Authorization", header)
	}
	w.r.ServeHTTP(rec, req)
	return rec
}

// TestWebhookRouteEndToEnd: a correctly signed host track_published makes
// the stream live; anything unsigned or mis-signed is 401 and changes
// nothing; with no credentials configured even a "valid" one is refused.
func TestWebhookRouteEndToEnd(t *testing.T) {
	w := newWHRig(t, whKey, whSecret)
	body := trackPublishedBody(w.st.LiveKitRoom, w.host.String())

	if rec := w.post("", body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unsigned: %d", rec.Code)
	}
	if rec := w.post(signLiveKit(t, whKey, "wrong", body, whNow, nil), body); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong secret: %d", rec.Code)
	}
	if got := w.store.Stream(w.st.ID).Status; got != postgres.StatusStarting {
		t.Fatalf("a refused webhook changed the stream: %s", got)
	}
	if rec := w.post(signLiveKit(t, whKey, whSecret, body, whNow, nil), body); rec.Code != http.StatusOK {
		t.Fatalf("valid: %d %s", rec.Code, rec.Body.String())
	}
	if got := w.store.Stream(w.st.ID).Status; got != postgres.StatusLive {
		t.Fatalf("status = %s, want live", got)
	}

	off := newWHRig(t, "", "")
	b2 := trackPublishedBody(off.st.LiveKitRoom, off.host.String())
	if rec := off.post(signLiveKit(t, "", "", b2, whNow, nil), b2); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unset credentials accepted a webhook: %d", rec.Code)
	}
	if got := off.store.Stream(off.st.ID).Status; got != postgres.StatusStarting {
		t.Fatalf("unset credentials changed the stream: %s", got)
	}
	// The old unauthenticated path is gone.
	rec := httptest.NewRecorder()
	w.r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/v1/livestream/egress/webhook", bytes.NewReader(body)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("old egress webhook path answered %d", rec.Code)
	}
}

func TestParseLiveKitWebhookProtojsonAndSnake(t *testing.T) {
	camel := []byte(`{"event":"egress_ended","id":"EV_1","egressInfo":{"egressId":"EG_1","roomName":"stream_r","status":"EGRESS_COMPLETE",` +
		`"fileResults":[{"filename":"recordings/a.mp4","location":"https://s3/a.mp4","duration":"90000000000"}]}}`)
	snake := []byte(`{"event":"egress_ended","id":"EV_1","egress_info":{"egress_id":"EG_1","room_name":"stream_r","status":"EGRESS_COMPLETE",` +
		`"file":{"filename":"recordings/a.mp4","location":"https://s3/a.mp4","duration":90000000000}}}`)
	for name, b := range map[string][]byte{"protojson": camel, "snake": snake} {
		ev, err := ParseLiveKitWebhook(b)
		if err != nil || ev.Egress == nil || ev.Egress.RoomName != "stream_r" || ev.Egress.Status != "EGRESS_COMPLETE" ||
			ev.Egress.Location != "https://s3/a.mp4" || ev.Egress.Filename != "recordings/a.mp4" || ev.Egress.DurationNs != 90_000_000_000 {
			t.Fatalf("%s: %+v %v", name, ev.Egress, err)
		}
	}
	ev, err := ParseLiveKitWebhook(trackPublishedBody("stream_r", "u1"))
	if err != nil || ev.Event != "track_published" || ev.Room != "stream_r" || ev.ParticipantIdentity != "u1" ||
		ev.TrackSID != "TR_v" || len(ev.ParticipantTrackSIDs) != 1 {
		t.Fatalf("track_published: %+v %v", ev, err)
	}
}

func TestUnverifiedIssuerIsDiagnosticOnly(t *testing.T) {
	body := []byte(`{"event":"room_started","id":"EV_diag"}`)
	tok := signLiveKit(t, "APIother", "some-other-secret", body, time.Now(), nil)
	if got := unverifiedIssuer(tok); got != "APIother" {
		t.Fatalf("unverifiedIssuer = %q, want APIother", got)
	}
	if got := unverifiedIssuer("not-a-jwt"); got != "" {
		t.Fatalf("unverifiedIssuer(garbage) = %q, want empty", got)
	}
	if got := keyPrefix("APIabcdef"); got != "APIab" {
		t.Fatalf("keyPrefix = %q", got)
	}
}
