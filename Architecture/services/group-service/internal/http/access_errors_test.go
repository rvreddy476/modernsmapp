package http

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/group-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The gate's refusals reach the client with the status they were meant to
// have: the private-group answer is the missing-group 404, and the rest are
// 403 (or 422 for a bad role), never a 500.
func TestAccessRefusalsMapToTheirStatus(t *testing.T) {
	gin.SetMode(gin.TestMode)
	status := func(err error) (int, string) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/groups/x/rules", nil)
		handleServiceError(c, err)
		return w.Code, w.Body.String()
	}

	missing, _ := status(errors.New("not found: group not found"))
	got, body := status(service.ErrGroupNotFound)
	if got != missing || got != http.StatusNotFound {
		t.Fatalf("ErrGroupNotFound -> %d, a missing group -> %d; both must be 404", got, missing)
	}
	if strings.Contains(body, "FORBIDDEN") || strings.Contains(body, "private") {
		t.Fatalf("the private-group refusal names its reason: %s", body)
	}

	for _, err := range []error{
		service.ErrGroupBanned, service.ErrNotGroupMember, service.ErrMemberListHidden,
		service.ErrCommentsRestricted, service.ErrCreatorCannotLeave,
		service.ErrCannotModerateSelf, service.ErrCannotModerateOwner, service.ErrCannotModeratePeer,
		service.ErrOnlyOwnerPromotes, service.ErrBanPlacedAbove,
	} {
		if got, _ := status(err); got != http.StatusForbidden {
			t.Errorf("%q -> %d, want 403", err, got)
		}
	}
	if got, _ := status(errors.New(`invalid role "banned": must be admin, moderator or member`)); got != http.StatusUnprocessableEntity {
		t.Errorf("an unassignable role -> %d, want 422", got)
	}
}

// A signed-out read is anonymous; a header that is present but not a uuid is
// refused rather than read as anonymous.
func TestOptionalUserID(t *testing.T) {
	gin.SetMode(gin.TestMode)
	read := func(header string) (uuid.UUID, bool, int) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/v1/groups/x/rules", nil)
		if header != "" {
			c.Request.Header.Set("X-User-Id", header)
		}
		id, ok := optionalUserID(c)
		return id, ok, w.Code
	}
	if id, ok, _ := read(""); !ok || id != uuid.Nil {
		t.Fatalf("no header -> %v, %v; want anonymous", id, ok)
	}
	if _, ok, code := read("not-a-uuid"); ok || code != http.StatusUnauthorized {
		t.Fatalf("a malformed header was accepted (ok=%v, %d)", ok, code)
	}
	want := uuid.New()
	if id, ok, _ := read(want.String()); !ok || id != want {
		t.Fatalf("a valid header -> %v, %v", id, ok)
	}
}
