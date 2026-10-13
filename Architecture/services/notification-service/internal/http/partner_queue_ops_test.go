package http

import (
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"net/http/httptest"
	"testing"
	"time"
)

func TestPartnerNotificationOpsRequireScopedAdminActor(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier("notifications")
	if err = v.RegisterBase64("admin-service", "test", pub, []string{"platform:audit.read", "platform:roles.manage"}, nil); err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64("admin-service", "test", priv)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil), nil).RegisterPartnerNotificationAdmin(r, v)
	for _, tc := range []struct {
		name, audience, scope, actor string
		want                         int
	}{
		{"no token", "", "", "", 401},
		{"wrong audience", "commerce", "platform:audit.read", uuid.NewString(), 403},
		{"wrong scope", "notifications", "platform:roles.manage", uuid.NewString(), 403},
		{"missing actor", "notifications", "platform:audit.read", "", 403},
		{"signed reader", "notifications", "platform:audit.read", uuid.NewString(), 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/v1/notifications/internal/admin/partner-jobs", nil)
			req.Header.Set("X-User-Id", uuid.NewString())
			req.Header.Set("X-Internal-Service-Key", "not-authority")
			if tc.audience != "" {
				tok, e := signer.Mint(tc.audience, "admin-console", []string{tc.scope}, nil, time.Minute, servicetoken.WithActor(tc.actor))
				if e != nil {
					t.Fatal(e)
				}
				req.Header.Set("X-Service-Authorization", "Bearer "+tok)
			}
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d", w.Code, tc.want)
			}
		})
	}
}
