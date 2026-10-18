package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/posts, PATCH /v1/posts/:id and the draft error writer for the
// post screens parity fields (2026-10-18).

func createWith(t *testing.T, h *Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/posts", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	c.Request.Header.Set("X-User-Id", uuid.NewString())
	c.Request.Header.Set("Idempotency-Key", uuid.NewString())
	h.CreatePost(c)
	return rec
}

// visibility_policy was parsed and dropped; a request carrying one is now
// refused before anything else happens (the Handler has no service).
func TestCreatePost_VisibilityPolicyIsRefused(t *testing.T) {
	rec := createWith(t, &Handler{}, `{"text":"hi","visibility":"public","visibility_policy":{"mode":"circles","allow_users":["x"]}}`)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "UNSUPPORTED_VISIBILITY_POLICY" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

// Each detail field reaches the service and comes back as its own 400. The
// first case also proves `friends` passes the visibility binding and a JSON
// null visibility_policy is "absent".
func TestCreatePost_DetailFieldsAre400s(t *testing.T) {
	media := uuid.NewString()
	h := &Handler{svc: service.New(nil, nil, nil)}
	cases := []struct {
		name, extra, code string
	}{
		{"friends + bad link", `"visibility":"friends","visibility_policy":null,"link_url":"javascript:alert(1)"`, "INVALID_LINK_URL"},
		{"http link", `"visibility":"public","link_url":"http://example.com"`, "INVALID_LINK_URL"},
		{"remix", `"visibility":"public","remix_setting":"deny"`, "INVALID_REMIX_SETTING"},
		{"comment access", `"visibility":"public","comment_access":"friends_only"`, "INVALID_COMMENT_ACCESS"},
		{"place id", `"visibility":"public","location_place_id":"<x>"`, "INVALID_LOCATION_PLACE_ID"},
		{"cover off the carousel", `"visibility":"public","cover_media_id":"` + uuid.NewString() + `"`, "INVALID_COVER"},
		{"cover not an id", `"visibility":"public","cover_media_id":"nope"`, "INVALID_COVER"},
		{"cover_index", `"visibility":"public","cover_index":3`, "INVALID_COVER"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			body := fmt.Sprintf(`{"text":"hi","post_type":"image","media_ids":[%q],%s}`, media, tc.extra)
			rec := createWith(t, h, body)
			if rec.Code != http.StatusBadRequest || errorCode(t, rec) != tc.code {
				t.Fatalf("got %d %s, want 400 %s", rec.Code, rec.Body.String(), tc.code)
			}
		})
	}
	// Anything else is still refused by the binding.
	rec := createWith(t, h, `{"text":"hi","visibility":"mutuals"}`)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_REQUEST" {
		t.Fatalf("unknown visibility: %d %s", rec.Code, rec.Body.String())
	}
}

// A request that sends none of the new fields fingerprints as it did before
// the fields existed, so a retry in flight across the deploy still replays.
func TestCreateFingerprint_NewFieldsAbsentWhenUnset(t *testing.T) {
	raw, err := canonicalCreateRequest(baseRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"cover_index", "is_branded", "ai_generated", "link_url", "location_place_id", "visibility_policy"} {
		if strings.Contains(string(raw), `"`+k+`"`) {
			t.Errorf("canonical request carries %q when unset: %s", k, raw)
		}
	}
	withLink := baseRequest()
	withLink.LinkURL = "https://example.com"
	if fingerprintOrFail(t, withLink) == fingerprintOrFail(t, baseRequest()) {
		t.Fatal("link_url is not part of the fingerprint")
	}
}

func TestCreateGuard_DetailCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for err, code := range map[error]string{
		service.ErrInvalidCover:           "INVALID_COVER",
		service.ErrInvalidRemixSetting:    "INVALID_REMIX_SETTING",
		service.ErrInvalidCommentAccess:   "INVALID_COMMENT_ACCESS",
		service.ErrInvalidLinkURL:         "INVALID_LINK_URL",
		service.ErrInvalidLocationPlaceID: "INVALID_LOCATION_PLACE_ID",
		service.ErrInvalidVisibility:      "INVALID_VISIBILITY",
	} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		if !writeCreateGuardError(c, fmt.Errorf("wrapped: %w", err)) || rec.Code != http.StatusBadRequest || errorCode(t, rec) != code {
			t.Errorf("%v -> %d %s, want 400 %s", err, rec.Code, rec.Body.String(), code)
		}
	}
}

// A draft refusal names the field (it used to be a bare INVALID_DRAFT or,
// at publish, a 500).
func TestDraftErrors_NameTheField(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	(&Handler{}).writePostDraftError(c, fmt.Errorf("%w: %w", service.ErrInvalidDraft, service.ErrInvalidLinkURL))
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_LINK_URL" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	c, _ = gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	(&Handler{}).writePostDraftError(c, fmt.Errorf("%w: draft needs text or media", service.ErrInvalidDraft))
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "INVALID_DRAFT" {
		t.Fatalf("plain draft refusal: %d %s", rec.Code, rec.Body.String())
	}
}

// PATCH carries link_url / is_branded / ai_generated / friends to the store
// and refuses a bad link with 422, through the real router.
func TestPatchPost_DetailsScreenFields(t *testing.T) {
	r := newHubRouteRig(t, nil)
	path := "/v1/posts/" + r.post.ID.String()
	w := r.do(t, http.MethodPatch, path, &r.owner, `{"link_url":"javascript:alert(1)"}`)
	expectCode(t, w, http.StatusUnprocessableEntity, "INVALID_LINK_URL")

	w = r.do(t, http.MethodPatch, path, &r.owner, `{"link_url":"https://example.com/x","is_branded":true,"ai_generated":true,"visibility":"friends"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
	p := r.edits.lastPatch
	if p.LinkURL == nil || *p.LinkURL != "https://example.com/x" || p.IsBranded == nil || !*p.IsBranded ||
		p.AIGenerated == nil || !*p.AIGenerated || p.Visibility == nil || *p.Visibility != "friends" {
		t.Fatalf("patch = %+v", p)
	}
	w = r.do(t, http.MethodPatch, path, &r.owner, `{"link_url":""}`)
	if w.Code != http.StatusOK || !r.edits.lastPatch.ClearLinkURL {
		t.Fatalf(`"" must clear: %d %+v`, w.Code, r.edits.lastPatch)
	}
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	for _, k := range []string{"is_branded", "ai_generated"} {
		if _, ok := env.Data[k]; !ok {
			t.Errorf("PATCH answer has no %q", k)
		}
	}
}
