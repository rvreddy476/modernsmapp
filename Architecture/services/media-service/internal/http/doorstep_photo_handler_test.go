package http

import (
	"context"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDoorstepPhotoCallerGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	doorstep, pub := mustSigner(t, IssuerDoorstepService, "ds")
	other, otherPub := mustSigner(t, IssuerCommerceService, "co")
	verifier := servicetoken.NewVerifier(AudienceMedia)
	for issuer, entry := range map[string]struct{ kid, pub string }{IssuerDoorstepService: {"ds", pub}, IssuerCommerceService: {"co", otherPub}} {
		if err := verifier.RegisterBase64(issuer, entry.kid, entry.pub, []string{OpDoorstepPhotoPrepare, OpImageBytesRead}, nil); err != nil {
			t.Fatal(err)
		}
	}
	h := (&Handler{}).WithImageBytesAuth(verifier, false)
	called := false
	r := gin.New()
	r.POST("/photo", h.requireDoorstepPhotoCaller(), func(c *gin.Context) { called = true; c.Status(204) })
	for _, tc := range []struct {
		name, token string
		want        int
	}{
		{"missing", "", 403},
		{"wrong issuer", mint(t, other, AudienceMedia, OpDoorstepPhotoPrepare), 403},
		{"wrong operation", mint(t, doorstep, AudienceMedia, OpImageBytesRead), 403},
		{"wrong audience", mint(t, doorstep, "payments", OpDoorstepPhotoPrepare), 403},
		{"accepted", mint(t, doorstep, AudienceMedia, OpDoorstepPhotoPrepare), 204},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called = false
			req := httptest.NewRequest("POST", "/photo", nil)
			req.Header.Set(ServiceAuthHeader, "Bearer "+tc.token)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want || called != (tc.want == 204) {
				t.Fatalf("status=%d store=%v", w.Code, called)
			}
		})
	}
}

func TestDoorstepPhotoShapeGuard(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id, owner := uuid.New(), uuid.New()
	for _, tc := range []struct {
		body  string
		owned bool
		want  int
	}{
		{`{"owner_id":"` + owner.String() + `"}`, true, 204},
		{`{"owner_id":"` + owner.String() + `"}`, false, 404},
		{`{"owner_id":"` + owner.String() + `","extra":1}`, true, 400},
		{`{"owner_id":"` + owner.String() + `"} {}`, true, 400},
		{`{}`, true, 400},
	} {
		calls := 0
		r := gin.New()
		r.POST("/:mediaId", func(c *gin.Context) {
			prepareDoorstepPhoto(c, func(_ context.Context, m, u uuid.UUID) (bool, error) {
				calls++
				if m != id || u != owner {
					t.Fatal("wrong identity")
				}
				return tc.owned, nil
			})
		})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest("POST", "/"+id.String(), strings.NewReader(tc.body)))
		if w.Code != tc.want || (tc.want == 400 && calls != 0) {
			t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
		}
		if w.Header().Get("Cache-Control") != "no-store, private" {
			t.Fatal("cacheable evidence")
		}
	}
}
