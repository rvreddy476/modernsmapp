package http

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// Private accounts on the profile surfaces: is_private and follow_status
// are display facts, resolved per request, fail-open to their zero values.

type staticPrivacy struct {
	private   bool
	privErr   error
	status    string
	statusErr error
	// seen records the (viewer, target) pairs FollowStatus was asked about.
	seen [][2]uuid.UUID
}

func (s *staticPrivacy) IsPrivate(context.Context, uuid.UUID) (bool, error) {
	return s.private, s.privErr
}
func (s *staticPrivacy) FollowStatus(_ context.Context, viewer, target uuid.UUID) (string, error) {
	s.seen = append(s.seen, [2]uuid.UUID{viewer, target})
	return s.status, s.statusErr
}

func privacyRouter(t *testing.T, resolver ProfilePrivacyResolver) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&dtoProfileService{}, nil).WithBlockChecker(staticBlockChecker{})
	h.WithProfilePrivacy(resolver)
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

func getProfile(t *testing.T, r *gin.Engine, path, viewer string) map[string]any {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewer != "" {
		req.Header.Set("X-User-Id", viewer)
	}
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var env struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return env.Data
}

func TestProfileCarriesIsPrivateAndViewerFollowStatus(t *testing.T) {
	viewer := uuid.New()
	resolver := &staticPrivacy{private: true, status: FollowStatusRequested}
	r := privacyRouter(t, resolver)

	for _, path := range []string{
		"/v1/profiles/" + testTargetID.String(),
		"/v1/profiles/by-username/publicname",
	} {
		t.Run(path, func(t *testing.T) {
			resolver.seen = nil
			data := getProfile(t, r, path, viewer.String())
			if data["is_private"] != true {
				t.Fatalf("is_private = %v, want true", data["is_private"])
			}
			if data["follow_status"] != FollowStatusRequested {
				t.Fatalf("follow_status = %v, want %q", data["follow_status"], FollowStatusRequested)
			}
			if len(resolver.seen) != 1 || resolver.seen[0][0] != viewer {
				t.Fatalf("follow status resolved for the wrong viewer: %v", resolver.seen)
			}
		})
	}
}

// No viewer, or the owner viewing themselves: no follow_status at all
// (not "none" — the field is absent), and no relationship call is made.
func TestFollowStatusOmittedForAnonymousAndOwner(t *testing.T) {
	resolver := &staticPrivacy{private: false, status: FollowStatusFollowing}
	r := privacyRouter(t, resolver)
	path := "/v1/profiles/" + testTargetID.String()

	for name, viewer := range map[string]string{"anonymous": "", "owner": testTargetID.String()} {
		t.Run(name, func(t *testing.T) {
			resolver.seen = nil
			data := getProfile(t, r, path, viewer)
			if _, present := data["follow_status"]; present {
				t.Fatalf("follow_status present for %s viewer: %v", name, data["follow_status"])
			}
			if data["is_private"] != false {
				t.Fatalf("is_private = %v, want false", data["is_private"])
			}
			if len(resolver.seen) != 0 {
				t.Fatalf("relationship resolved for %s viewer", name)
			}
		})
	}
}

// These are display facts, not gates: a resolver failure renders the zero
// values and the profile still answers 200.
func TestProfilePrivacyFailsOpenToZeroValues(t *testing.T) {
	resolver := &staticPrivacy{privErr: errors.New("settings down"), statusErr: errors.New("graph down")}
	r := privacyRouter(t, resolver)
	data := getProfile(t, r, "/v1/profiles/"+testTargetID.String(), uuid.New().String())
	if data["is_private"] != false {
		t.Fatalf("is_private = %v on failure, want false", data["is_private"])
	}
	if _, present := data["follow_status"]; present {
		t.Fatalf("follow_status present on failure: %v", data["follow_status"])
	}
}

// The batch page stamps is_private on every entry; follow_status is a
// per-viewer edge and is not resolved there.
func TestBatchProfilesCarryIsPrivateOnly(t *testing.T) {
	resolver := &staticPrivacy{private: true, status: FollowStatusFollowing}
	r := privacyRouter(t, resolver)
	ids := []string{uuid.New().String(), uuid.New().String()}
	body, _ := json.Marshal(map[string]any{"user_ids": ids})
	req := httptest.NewRequest(http.MethodPost, "/v1/profiles/batch", bytesReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.New().String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("got %d profiles", len(out))
	}
	for id, p := range out {
		if p["is_private"] != true {
			t.Fatalf("%s is_private = %v", id, p["is_private"])
		}
		if _, present := p["follow_status"]; present {
			t.Fatalf("%s carries follow_status in batch", id)
		}
	}
	if len(resolver.seen) != 0 {
		t.Fatalf("batch resolved follow status: %v", resolver.seen)
	}
}

func TestFollowStatusFrom(t *testing.T) {
	cases := []struct {
		follows bool
		req     string
		want    string
	}{
		{true, "", FollowStatusFollowing},
		{true, "pending_sent", FollowStatusFollowing},
		{false, "pending_sent", FollowStatusRequested},
		{false, "pending_received", FollowStatusNone},
		{false, "none", FollowStatusNone},
		{false, "", FollowStatusNone},
	}
	for _, tc := range cases {
		if got := followStatusFrom(tc.follows, tc.req); got != tc.want {
			t.Errorf("followStatusFrom(%v,%q)=%q want %q", tc.follows, tc.req, got, tc.want)
		}
	}
}

// The resolver decodes graph-service's relationship envelope and the
// identity settings envelope, and treats an unknown user as public.
func TestHTTPResolverContracts(t *testing.T) {
	viewer, target := uuid.New(), uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Internal-Service-Key") != "k" {
			t.Errorf("internal key missing on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/graph/relationship":
			if r.URL.Query().Get("user_id") != viewer.String() || r.URL.Query().Get("other_id") != target.String() {
				t.Errorf("relationship query = %s", r.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"data":{"follows":false,"follow_request_status":"pending_sent","is_private":true}}`))
		case "/v1/users/" + target.String() + "/settings":
			_, _ = w.Write([]byte(`{"data":{"account_visibility":"private"}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()
	res := NewProfilePrivacyResolver(srv.URL, srv.URL, "k")

	if status, err := res.FollowStatus(context.Background(), viewer, target); err != nil || status != FollowStatusRequested {
		t.Fatalf("FollowStatus = %q, %v", status, err)
	}
	if private, err := res.IsPrivate(context.Background(), target); err != nil || !private {
		t.Fatalf("IsPrivate = %v, %v", private, err)
	}
	if private, err := res.IsPrivate(context.Background(), uuid.New()); err != nil || private {
		t.Fatalf("unknown user IsPrivate = %v, %v; want false, nil", private, err)
	}
}
