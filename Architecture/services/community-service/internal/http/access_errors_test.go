package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atpost/community-service/internal/service"
	"github.com/gin-gonic/gin"
)

// The gate's refusals reach the client with the right status: a private or
// archived community is a 404 exactly like a missing one (K-M1).
func TestGateRefusalsMapToStatuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cases := map[string]struct {
		err  error
		want int
	}{
		"missing / private / archived": {service.ErrCommunityNotFound, http.StatusNotFound},
		"wrapped not found":            {fmt.Errorf("x: %w", service.ErrCommunityNotFound), http.StatusNotFound},
		"space of another community":   {service.ErrSpaceNotFound, http.StatusNotFound},
		"post of another community":    {service.ErrPostNotFound, http.StatusNotFound},
		"wiki of another community":    {service.ErrWikiNotFound, http.StatusNotFound},
		"banned":                       {service.ErrMemberBanned, http.StatusForbidden},
		"not a member, write":          {service.ErrNotCommunityMember, http.StatusForbidden},
		"role too low":                 {service.ErrInsufficientRole, http.StatusForbidden},
		"quarantined":                  {service.ErrSpaceQuarantined, http.StatusForbidden},
		"banned cannot leave":          {service.ErrBannedCannotLeave, http.StatusForbidden},
		"ban placed above":             {service.ErrBanPlacedAbove, http.StatusForbidden},
		"already banned":               {service.ErrAlreadyBanned, http.StatusConflict},
		"role change on a ban":         {service.ErrTargetBanned, http.StatusUnprocessableEntity},
		"member directory off":         {service.ErrMemberDirectoryHidden, http.StatusForbidden},
		"private community open":       {service.ErrPrivateCommunityOpen, http.StatusUnprocessableEntity},
	}
	for name, tc := range cases {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		handleServiceError(c, tc.err)
		if w.Code != tc.want {
			t.Errorf("%s: status %d, want %d (%s)", name, w.Code, tc.want, w.Body.String())
		}
	}
}

func TestViewerFromHeader(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for header, want := range map[string]struct {
		ok, viewer bool
	}{
		"":                                     {true, false},
		"   ":                                  {true, false},
		"not-a-uuid":                           {false, false},
		"6f1c2b9e-1d2a-4c3b-9a8e-1234567890ab": {true, true},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			c.Request.Header.Set("X-User-Id", header)
		}
		v, ok := viewerFromHeader(c)
		if ok != want.ok || (v != nil) != want.viewer {
			t.Errorf("header %q: viewer=%v ok=%v, want viewer=%v ok=%v", header, v, ok, want.viewer, want.ok)
		}
	}
}

// Handlers reach data only through the service, so every route passes the
// gate (K-M1, K-M2: the space-posts, wiki, events and featured routes called
// the store straight from the handler).
func TestHandlersNeverReachTheStore(t *testing.T) {
	files, _ := filepath.Glob("*.go")
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") || f == "admin_token.go" {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{".Store()", "svc.store", "store.New("} {
			if strings.Contains(string(src), bad) {
				t.Errorf("%s calls %q: a handler that reads the store skips the access gate", f, bad)
			}
		}
	}
}
