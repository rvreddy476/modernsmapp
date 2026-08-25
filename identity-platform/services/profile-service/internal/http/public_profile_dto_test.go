package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/atpost/identity-profile-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 3 M3-P0-4 / SR-4 — public profiles must be private by construction.
//
// `GET /v1/profiles/:userId` is unauthenticated and used to serialise
// store.Profile directly, so anyone could enumerate user IDs and harvest exact
// dates of birth, gender and timezone.

var (
	testTargetID = uuid.MustParse("aaaaaaaa-1111-4111-8111-aaaaaaaaaaaa")
	testViewerID = uuid.MustParse("bbbbbbbb-2222-4222-8222-bbbbbbbbbbbb")
)

func str(s string) *string { return &s }

// fullProfile has EVERY sensitive field populated, so a leak shows up as a
// value in the response rather than as an omitted empty field.
func fullProfile() *store.Profile {
	dob := time.Date(1994, 3, 17, 0, 0, 0, 0, time.UTC)
	return &store.Profile{
		UserID:         testTargetID,
		Username:       str("publicname"),
		DisplayName:    "Public Name",
		Bio:            "a bio",
		FirstName:      str("Legalfirst"),
		LastName:       str("Legallast"),
		PreferredName:  str("Preferredname"),
		Pronouns:       str("they/them"),
		DoB:            &dob,
		Gender:         str("nonbinary"),
		Timezone:       str("Asia/Kolkata"),
		Category:       "creator",
		Profession:     "engineer",
		Website:        "https://example.com",
		Location:       "Hyderabad",
		FollowerCount:  10,
		FollowingCount: 5,
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		UpdatedAt:      time.Now(),
	}
}

// dtoProfileService serves fullProfile from every read surface.
type dtoProfileService struct {
	stubProfileService
}

func (s *dtoProfileService) GetProfile(_ context.Context, _ uuid.UUID) (*store.Profile, error) {
	return fullProfile(), nil
}
func (s *dtoProfileService) GetProfileByUsername(_ context.Context, _ string) (*store.Profile, error) {
	return fullProfile(), nil
}
func (s *dtoProfileService) GetProfilesBatch(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*store.Profile, error) {
	out := map[uuid.UUID]*store.Profile{}
	for _, id := range ids {
		p := fullProfile()
		p.UserID = id
		out[id] = p
	}
	return out, nil
}
func (s *dtoProfileService) ListProfiles(_ context.Context, _, _ int) ([]store.Profile, int64, error) {
	return []store.Profile{*fullProfile()}, 1, nil
}

// staticBlockChecker returns a fixed answer, including a fixed error.
type staticBlockChecker struct {
	blocked bool
	err     error
}

func (s staticBlockChecker) BlockedEitherWay(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return s.blocked, s.err
}

func dtoRouter(t *testing.T, checker BlockChecker) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(&dtoProfileService{}, nil)
	if checker != nil {
		h.WithBlockChecker(checker)
	}
	h.RegisterRoutes(r, func(c *gin.Context) { c.Next() }, func(c *gin.Context) { c.Next() })
	return r
}

// The central assertion. Rather than checking a list of field names, this
// walks the RAW response body: any private value that appears anywhere in the
// JSON is a leak, however it got there.
func TestPublicProfileResponseContainsNoPrivateValues(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{})

	surfaces := []struct {
		name, method, path, body string
	}{
		{"by id", http.MethodGet, "/v1/profiles/" + testTargetID.String(), ""},
		{"by username", http.MethodGet, "/v1/profiles/by-username/publicname", ""},
		{"batch", http.MethodPost, "/v1/profiles/batch",
			`{"user_ids":["` + testTargetID.String() + `"]}`},
		{"discovery", http.MethodGet, "/v1/profiles/discover", ""},
	}

	// The literal values a leak would expose.
	forbidden := map[string]string{
		"1994-03-17":    "exact date of birth",
		"nonbinary":     "gender",
		"Asia/Kolkata":  "timezone",
		"Legalfirst":    "legal first name",
		"Legallast":     "legal last name",
		"Preferredname": "preferred name",
	}

	for _, s := range surfaces {
		t.Run(s.name, func(t *testing.T) {
			var req *http.Request
			if s.body != "" {
				req = httptest.NewRequest(s.method, s.path, strings.NewReader(s.body))
				req.Header.Set("Content-Type", "application/json")
			} else {
				req = httptest.NewRequest(s.method, s.path, nil)
			}
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)

			if resp.Code != http.StatusOK {
				t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
			}
			body := resp.Body.String()
			for value, what := range forbidden {
				if strings.Contains(body, value) {
					t.Errorf("PUBLIC RESPONSE LEAKS %s (%q). This endpoint is "+
						"unauthenticated: anyone can enumerate user IDs and harvest it.\n%s",
						what, value, body)
				}
			}
			// Confirm the surface actually returned a profile, or the absence
			// of private values proves nothing.
			if !strings.Contains(body, "Public Name") {
				t.Fatalf("no profile in the response, so this test asserts nothing: %s", body)
			}
		})
	}
}

// The DTO must be an ALLOWLIST. If it is ever changed to embed store.Profile
// or built by reflection, every field added to the store type would silently
// become public. This checks the shape itself, not one response.
func TestPublicProfileTypeDoesNotEmbedTheStoredProfile(t *testing.T) {
	pt := reflect.TypeOf(PublicProfile{})
	for i := 0; i < pt.NumField(); i++ {
		f := pt.Field(i)
		if f.Anonymous {
			t.Fatalf("PublicProfile embeds %s: every field later added to the "+
				"embedded type becomes public without review", f.Type)
		}
	}

	// Every field name documented as private must be absent from the DTO's
	// JSON tags. This keeps the reasoning in privateProfileFields honest.
	tags := map[string]bool{}
	for i := 0; i < pt.NumField(); i++ {
		tag := pt.Field(i).Tag.Get("json")
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			tags[name] = true
		}
	}
	for name, why := range privateProfileFields {
		if tags[name] {
			t.Errorf("PublicProfile publishes %q, documented as private: %s", name, why)
		}
	}
}

// The owner must still see their own private fields — a user has to be able to
// read and correct their own date of birth.
func TestOwnerStillSeesTheirOwnPrivateFields(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/me", nil)
	req.Header.Set("X-User-Id", testTargetID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status %d: %s", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "1994-03-17") {
		t.Fatal("the owner cannot see their own date of birth; the DTO was applied " +
			"to the owner surface by mistake")
	}
}

// ── Block denial ────────────────────────────────────────────────────────────

func TestBlockedViewerGetsNotFoundOnEveryProfileSurface(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{blocked: true})

	for _, path := range []string{
		"/v1/profiles/" + testTargetID.String(),
		"/v1/profiles/by-username/publicname",
	} {
		t.Run(path, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("X-User-Id", testViewerID.String())
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, req)

			if resp.Code != http.StatusNotFound {
				t.Fatalf("status %d, want 404. A blocked viewer could read the "+
					"profile of the person who blocked them.", resp.Code)
			}
			// 403 would confirm the account exists and that this specific
			// person blocked them — turning a block into a notification and
			// giving a harasser a probe.
			if strings.Contains(resp.Body.String(), "BLOCK") ||
				strings.Contains(strings.ToUpper(resp.Body.String()), "FORBIDDEN") {
				t.Errorf("the response reveals that a block exists: %s", resp.Body.String())
			}
		})
	}
}

func TestBlockedEntriesAreOmittedFromListSurfaces(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{blocked: true})

	req := httptest.NewRequest(http.MethodPost, "/v1/profiles/batch",
		strings.NewReader(`{"user_ids":["`+testTargetID.String()+`"]}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", testViewerID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	var got map[string]json.RawMessage
	if err := json.Unmarshal(resp.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v (%s)", err, resp.Body.String())
	}
	if _, present := got[testTargetID.String()]; present {
		t.Fatal("batch lookup returned a profile the viewer has blocked")
	}
}

// FAIL CLOSED. If graph-service is unreachable the profile must not be served:
// failing open would silently re-expose every blocked user for the duration of
// an incident, with every response still 200.
func TestBlockCheckFailureDeniesRatherThanServes(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{err: errors.New("graph-service unreachable")})

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+testTargetID.String(), nil)
	req.Header.Set("X-User-Id", testViewerID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code == http.StatusOK {
		t.Fatal("a profile was served while the block check was FAILING. During a " +
			"graph-service incident every blocked user would be re-exposed to the " +
			"person who blocked them, silently.")
	}
}

// An unconfigured checker is the same hazard as a failing one: it means block
// enforcement is simply not running.
func TestUnconfiguredBlockCheckerDeniesAuthenticatedReads(t *testing.T) {
	r := dtoRouter(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+testTargetID.String(), nil)
	req.Header.Set("X-User-Id", testViewerID.String())
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code == http.StatusOK {
		t.Fatal("profiles are being served with NO block enforcement configured")
	}
}

// Anonymous callers have no block relationship to evaluate, so public profiles
// stay public — the DTO is what protects them.
func TestAnonymousCallersStillGetPublicProfiles(t *testing.T) {
	r := dtoRouter(t, staticBlockChecker{blocked: true})
	req := httptest.NewRequest(http.MethodGet, "/v1/profiles/"+testTargetID.String(), nil)
	resp := httptest.NewRecorder()
	r.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("anonymous read was refused (status %d); public profiles must "+
			"stay reachable", resp.Code)
	}
}

// ── Handle changes ──────────────────────────────────────────────────────────

// A handle is an identity claim. Changing it through the general profile
// update bypassed the cooldown, the handle-history record and the reservation
// of the old handle.
func TestGeneralProfileUpdateCannotChangeTheUsername(t *testing.T) {
	rt := reflect.TypeOf(UpdateProfileRequest{})
	for i := 0; i < rt.NumField(); i++ {
		tag := rt.Field(i).Tag.Get("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "username" || name == "handle" {
			t.Fatalf("UpdateProfileRequest still accepts %q. PUT /me/handle is the "+
				"only path that may change a handle — it carries the cooldown, the "+
				"history record and the old-handle reservation.", name)
		}
	}
}
