package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/atpost/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// These tests drive the REAL router (Handler.RegisterRoutes) with an
// in-memory user reader, and assert which JSON keys each kind of caller gets
// back from every route that returns a user object.

var (
	pvOwnerID    = uuid.MustParse("66668bc2-a3f6-40a5-9cdd-c998dcf72f29")
	pvStrangerID = uuid.MustParse("2d598287-eee7-40b4-a7f5-b46b9412e4e7")
)

const (
	pvUsername    = "owner_handle"
	pvInternalKey = "test-internal-key"
)

// privateProfileKeys must never reach a caller who is not the owner or a
// service-token holder.
var privateProfileKeys = []string{"first_name", "last_name", "dob", "gender"}

// publicProfileKeys are the public card; every caller keeps them.
var publicProfileKeys = []string{
	"entityType", "id", "username", "display_name", "bio", "avatar_media_id",
	"category", "profession", "website", "location", "pronouns",
	"badge_flags", "is_verified", "created_at", "updated_at",
}

type fakeUserReader struct {
	users map[uuid.UUID]*store.User
}

func (f *fakeUserReader) GetUser(_ context.Context, id uuid.UUID) (*store.User, error) {
	return f.users[id], nil // same pointer every time, like the Redis-backed service
}

func (f *fakeUserReader) GetUserByUsername(_ context.Context, username string) (*store.User, error) {
	for _, u := range f.users {
		if u.Username != nil && *u.Username == username {
			return u, nil
		}
	}
	return nil, nil
}

func pvStr(s string) *string { return &s }

func newPrivacyFixture() *fakeUserReader {
	dob := time.Date(1990, 4, 11, 0, 0, 0, 0, time.UTC)
	avatar := uuid.New()
	return &fakeUserReader{users: map[uuid.UUID]*store.User{
		pvOwnerID: {
			EntityType:    "user",
			ID:            pvOwnerID,
			Username:      pvStr(pvUsername),
			DisplayName:   "Owner",
			FirstName:     pvStr("Priya"),
			LastName:      pvStr("Rao"),
			Bio:           "hello",
			DoB:           &dob,
			Gender:        pvStr("female"),
			AvatarMediaID: &avatar,
			Category:      pvStr("personal"),
			Profession:    pvStr("engineer"),
			Website:       pvStr("https://example.com"),
			Location:      pvStr("Hyderabad"),
			Pronouns:      pvStr("she/her"),
			BadgeFlags:    1,
			IsVerified:    true,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		},
	}}
}

type pvTokens struct {
	verifier      *servicetoken.Verifier
	valid         string // audience user-service, scope users:profile.read_private
	wrongScope    string
	wrongAudience string
}

func newPVTokens(t *testing.T) pvTokens {
	t.Helper()
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	v := servicetoken.NewVerifier(AudienceUserService)
	if err := v.RegisterBase64("chat-service", "c1", pub, []string{OpReadPrivateProfile}, nil); err != nil {
		t.Fatal(err)
	}
	signer, err := servicetoken.NewSignerFromBase64("chat-service", "c1", priv)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(aud string, scope ...string) string {
		tok, err := signer.Mint(aud, "profile-read", scope, nil, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	return pvTokens{
		verifier:      v,
		valid:         mint(AudienceUserService, OpReadPrivateProfile),
		wrongScope:    mint(AudienceUserService, "users:profile.read"),
		wrongAudience: mint("dating", OpReadPrivateProfile),
	}
}

func newPrivacyRouter(reader userReader, v *servicetoken.Verifier) *gin.Engine {
	gin.SetMode(gin.TestMode)
	h := &Handler{users: reader, pageAdmins: map[string]bool{}}
	h.WithInternalRoutes(pvInternalKey)
	h.WithServiceAuth(v)
	r := gin.New()
	h.RegisterRoutes(r)
	return r
}

func getUserKeys(t *testing.T, r *gin.Engine, path string, headers map[string]string) (int, map[string]json.RawMessage) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
			t.Fatalf("decode %s: %v (body %s)", path, err, w.Body.String())
		}
	}
	return w.Code, env.Data
}

func keysOf(m map[string]json.RawMessage) string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return strings.Join(ks, ",")
}

func assertFullProfile(t *testing.T, data map[string]json.RawMessage) {
	t.Helper()
	for _, k := range append(append([]string{}, publicProfileKeys...), privateProfileKeys...) {
		if _, ok := data[k]; !ok {
			t.Errorf("expected key %q in full profile; got keys [%s]", k, keysOf(data))
		}
	}
}

func assertPublicProfileOnly(t *testing.T, data map[string]json.RawMessage) {
	t.Helper()
	for _, k := range privateProfileKeys {
		if _, ok := data[k]; ok {
			t.Errorf("private key %q leaked to a non-owner; got keys [%s]", k, keysOf(data))
		}
	}
	for _, k := range publicProfileKeys {
		if _, ok := data[k]; !ok {
			t.Errorf("public key %q missing; the public card shape must not change; got keys [%s]", k, keysOf(data))
		}
	}
}

// userObjectRoutes is every path-addressed route that returns another user's
// profile object.
var userObjectRoutes = map[string]string{
	"by-id":       "/v1/users/" + pvOwnerID.String(),
	"by-username": "/v1/users/by-username/" + pvUsername,
}

type pvCase struct {
	name    string
	headers func(tk pvTokens) map[string]string
	full    bool
}

var pvCases = []pvCase{
	{"owner", func(pvTokens) map[string]string {
		return map[string]string{"X-User-Id": pvOwnerID.String(), "X-Internal-Service-Key": pvInternalKey}
	}, true},
	{"authenticated_stranger", func(pvTokens) map[string]string {
		return map[string]string{"X-User-Id": pvStrangerID.String(), "X-Verified-User-Id": pvStrangerID.String()}
	}, false},
	{"anonymous_no_headers", func(pvTokens) map[string]string { return nil }, false},
	// What the gateway forwards for a request with no token: the key, no identity.
	{"internal_key_only", func(pvTokens) map[string]string {
		return map[string]string{"X-Internal-Service-Key": pvInternalKey}
	}, false},
	// What the gateway forwards for a logged-in stranger: the key AND an identity.
	{"internal_key_with_stranger_user_id", func(pvTokens) map[string]string {
		return map[string]string{"X-Internal-Service-Key": pvInternalKey, "X-User-Id": pvStrangerID.String()}
	}, false},
	{"malformed_user_id", func(pvTokens) map[string]string {
		return map[string]string{"X-User-Id": "not-a-uuid", "X-Internal-Service-Key": pvInternalKey}
	}, false},
	{"service_token", func(tk pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer " + tk.valid, "X-Internal-Service-Key": pvInternalKey}
	}, true},
	{"service_token_with_stranger_user_id", func(tk pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer " + tk.valid, "X-User-Id": pvStrangerID.String()}
	}, false},
	{"service_token_with_gateway_scopes", func(tk pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer " + tk.valid, "X-Scopes": "admin"}
	}, false},
	{"service_token_wrong_scope", func(tk pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer " + tk.wrongScope}
	}, false},
	{"service_token_wrong_audience", func(tk pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer " + tk.wrongAudience}
	}, false},
	{"service_token_garbage", func(pvTokens) map[string]string {
		return map[string]string{ServiceAuthHeader: "Bearer a.b.c"}
	}, false},
}

func TestUserObjectRoutes_PrivateFieldsByCaller(t *testing.T) {
	tk := newPVTokens(t)
	r := newPrivacyRouter(newPrivacyFixture(), tk.verifier)
	for route, path := range userObjectRoutes {
		for _, tc := range pvCases {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				code, data := getUserKeys(t, r, path, tc.headers(tk))
				if code != http.StatusOK {
					t.Fatalf("status %d, want 200", code)
				}
				if tc.full {
					assertFullProfile(t, data)
				} else {
					assertPublicProfileOnly(t, data)
				}
			})
		}
	}
}

// With no verifier configured, a service token grants nothing.
func TestUserObjectRoutes_ServiceTokenIgnoredWithoutVerifier(t *testing.T) {
	tk := newPVTokens(t)
	r := newPrivacyRouter(newPrivacyFixture(), nil)
	for route, path := range userObjectRoutes {
		t.Run(route, func(t *testing.T) {
			code, data := getUserKeys(t, r, path, map[string]string{ServiceAuthHeader: "Bearer " + tk.valid})
			if code != http.StatusOK {
				t.Fatalf("status %d, want 200", code)
			}
			assertPublicProfileOnly(t, data)
		})
	}
}

// A stranger's read must not strip the fields from the shared (cached) object,
// or the owner's next read would come back without them.
func TestUserObjectRoutes_PublicViewDoesNotMutateSharedUser(t *testing.T) {
	fixture := newPrivacyFixture()
	r := newPrivacyRouter(fixture, nil)
	getUserKeys(t, r, userObjectRoutes["by-id"], map[string]string{"X-User-Id": pvStrangerID.String()})
	u := fixture.users[pvOwnerID]
	if u.DoB == nil || u.FirstName == nil || u.LastName == nil || u.Gender == nil {
		t.Fatal("public view mutated the shared user object")
	}
	_, data := getUserKeys(t, r, userObjectRoutes["by-id"], map[string]string{"X-User-Id": pvOwnerID.String()})
	assertFullProfile(t, data)
}

func TestUserObjectRoutes_UnknownUserIs404(t *testing.T) {
	r := newPrivacyRouter(newPrivacyFixture(), nil)
	for _, path := range []string{"/v1/users/" + uuid.NewString(), "/v1/users/by-username/nobody"} {
		if code, _ := getUserKeys(t, r, path, nil); code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, code)
		}
	}
}

func TestGetMe_OwnerSeesPrivateFields(t *testing.T) {
	r := newPrivacyRouter(newPrivacyFixture(), nil)
	code, data := getUserKeys(t, r, "/v1/users/me", map[string]string{
		"X-User-Id": pvOwnerID.String(), "X-Internal-Service-Key": pvInternalKey,
	})
	if code != http.StatusOK {
		t.Fatalf("status %d, want 200", code)
	}
	assertFullProfile(t, data)
}

func TestGetMe_RequiresValidUserID(t *testing.T) {
	r := newPrivacyRouter(newPrivacyFixture(), nil)
	for name, headers := range map[string]map[string]string{
		"anonymous":         {"X-Internal-Service-Key": pvInternalKey},
		"malformed_user_id": {"X-User-Id": "not-a-uuid", "X-Internal-Service-Key": pvInternalKey},
	} {
		t.Run(name, func(t *testing.T) {
			if code, _ := getUserKeys(t, r, "/v1/users/me", headers); code != http.StatusUnauthorized {
				t.Fatalf("status %d, want 401", code)
			}
		})
	}
}

func TestServiceCallersFromEnv(t *testing.T) {
	pub, _, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

	if v, err := ServiceCallersFromEnv(env(nil)); v != nil || err != nil {
		t.Fatalf("blank SERVICE_CALLERS: got (%v, %v), want (nil, nil)", v, err)
	}
	if _, err := ServiceCallersFromEnv(env(map[string]string{
		"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_KID": "c1",
		"SERVICE_CALLER_CHAT_SERVICE_OPS": OpReadPrivateProfile,
	})); err == nil {
		t.Fatal("caller without a public key must be refused")
	}
	if _, err := ServiceCallersFromEnv(env(map[string]string{
		"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_KID": "c1",
		"SERVICE_CALLER_CHAT_SERVICE_PUBKEY": pub,
	})); err == nil {
		t.Fatal("caller without operations must be refused")
	}
	v, err := ServiceCallersFromEnv(env(map[string]string{
		"SERVICE_CALLERS": "chat-service", "SERVICE_CALLER_CHAT_SERVICE_KID": "c1",
		"SERVICE_CALLER_CHAT_SERVICE_PUBKEY": pub, "SERVICE_CALLER_CHAT_SERVICE_OPS": OpReadPrivateProfile,
	}))
	if err != nil || v == nil || v.Callers() != 1 {
		t.Fatalf("valid caller: got (%v, %v)", v, err)
	}
}
