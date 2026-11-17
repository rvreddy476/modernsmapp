package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// reels-6: a read route believes X-User-Id only from the internal key or a
// verified access token. These run the real read routes (record_read
// fixture) behind TrustedViewerIdentity, the way cmd/server wires it.

const trustedIdentitySecret = "trusted-identity-test-secret"

func trustedIdentityKeys() JWTKeySet {
	return JWTKeySet{ActiveKID: "v1", ActiveSecret: trustedIdentitySecret}
}

// buildTrusted is recordFixture.build with the identity middleware in front.
func (f *recordFixture) buildTrusted(internalKey string) *gin.Engine {
	r := gin.New()
	r.Use(TrustedViewerIdentity(trustedIdentityKeys(), internalKey))
	h := &Handler{
		records: service.NewRecordReads(f.store, f.blobs, delivery.NewGate(nil, f.authz)),
		sounds:  service.NewSounds(f.store, f.blobs, f.extractor),
	}
	if internalKey != "" {
		h.WithInternalKey(internalKey)
	}
	h.RegisterRoutes(r, passthrough, passthrough)
	h.RegisterAudioRoutes(r, passthrough)
	return r
}

func userToken(t *testing.T, sub uuid.UUID) string {
	t.Helper()
	return signJWT(t, map[string]any{"alg": "HS256", "kid": "v1"},
		map[string]any{"sub": sub.String(), "exp": time.Now().Add(time.Hour).Unix()}, trustedIdentitySecret)
}

func trustedServe(r *gin.Engine, path string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

// The reproduction from the finding: a pod sends the author's id and nothing
// else, and used to get the private, unauthorised record as its owner.
func TestBareUserIDHeaderIsAnonymousOnReads(t *testing.T) {
	f := newRecordFixture(t)
	r := f.buildTrusted(recordInternalKey)
	path := "/v1/media/" + f.privateID.String()

	forged := trustedServe(r, path, "X-User-Id", f.owner.String())
	missing := trustedServe(r, "/v1/media/"+uuid.New().String(), "X-User-Id", f.owner.String())
	wantDeniedLikeMissing(t, "bare X-User-Id of the owner", forged, missing)

	wrongKey := trustedServe(r, path, "X-User-Id", f.owner.String(), internalKeyHeader, "not-the-key")
	wantDeniedLikeMissing(t, "X-User-Id with a wrong key", wrongKey, missing)

	audio := trustedServe(r, "/v1/audio/"+f.trackPrivate.String()+"/url", "X-User-Id", f.owner.String())
	if audio.Code != http.StatusNotFound {
		t.Fatalf("audio url with a bare owner header: got %d %s, want 404", audio.Code, audio.Body.String())
	}
}

// In-cluster callers (post, feed, search, commerce, chat) send the key with
// the viewer; that keeps working.
func TestInternalKeyVouchesForUserIDHeader(t *testing.T) {
	f := newRecordFixture(t)
	r := f.buildTrusted(recordInternalKey)
	rec := trustedServe(r, "/v1/media/"+f.privateID.String(), "X-User-Id", f.owner.String(), internalKeyHeader, recordInternalKey)
	if rec.Code != http.StatusOK {
		t.Fatalf("key-holding caller for the owner: got %d %s, want 200", rec.Code, rec.Body.String())
	}
}

// Through the gateway the user's token arrives with the request; its subject
// is the viewer, whatever X-User-Id says.
func TestVerifiedTokenIsTheViewer(t *testing.T) {
	f := newRecordFixture(t)
	r := f.buildTrusted(recordInternalKey)
	path := "/v1/media/" + f.privateID.String()

	if rec := trustedServe(r, path, "Authorization", "Bearer "+userToken(t, f.owner)); rec.Code != http.StatusOK {
		t.Fatalf("owner's bearer token: got %d %s, want 200", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(&http.Cookie{Name: "access_token", Value: userToken(t, f.owner)})
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("owner's cookie token: got %d %s, want 200", rec.Code, rec.Body.String())
	}

	// A stranger's token with the owner's id in the header: the token wins.
	missing := trustedServe(r, "/v1/media/"+uuid.New().String(), "Authorization", "Bearer "+userToken(t, f.stranger))
	spoof := trustedServe(r, path, "Authorization", "Bearer "+userToken(t, f.stranger), "X-User-Id", f.owner.String())
	wantDeniedLikeMissing(t, "stranger's token with the owner's header", spoof, missing)

	// A token that does not verify is no identity at all.
	bad := userToken(t, f.owner)
	bad = bad[:len(bad)-2] + "xx"
	forged := trustedServe(r, path, "Authorization", "Bearer "+bad, "X-User-Id", f.owner.String())
	wantDeniedLikeMissing(t, "bad token with the owner's header", forged, missing)
}

// Without an internal key configured (local dev) nothing can vouch for the
// header, so a bare header is anonymous there too.
func TestNoConfiguredKeyTrustsNoHeader(t *testing.T) {
	f := newRecordFixture(t)
	r := f.buildTrusted("")
	missing := trustedServe(r, "/v1/media/"+uuid.New().String(), "X-User-Id", f.owner.String(), internalKeyHeader, "")
	forged := trustedServe(r, "/v1/media/"+f.privateID.String(), "X-User-Id", f.owner.String(), internalKeyHeader, "")
	wantDeniedLikeMissing(t, "bare header with no key configured", forged, missing)
}

// The wiring: cmd/server installs the middleware on the engine before any
// media route is registered, with the service's key set and internal key.
func TestServerInstallsTrustedViewerIdentityBeforeRoutes(t *testing.T) {
	src, err := os.ReadFile("../../cmd/server/main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	main := string(src)
	use := strings.Index(main, "r.Use(mediaHttp.TrustedViewerIdentity(jwtKeys, internalServiceKey))")
	routes := strings.Index(main, "mediaHandler.RegisterRoutes(r,")
	if use < 0 {
		t.Fatal("cmd/server/main.go does not install TrustedViewerIdentity")
	}
	if routes < 0 || use > routes {
		t.Fatal("TrustedViewerIdentity must be installed before the media routes are registered")
	}
}
