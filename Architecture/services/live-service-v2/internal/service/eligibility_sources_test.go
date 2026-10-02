package service

// The eligibility fact sources (eligibility_sources.go) against httptest
// servers that answer in the shapes the real routes do — each shape was read
// from the running service on 2 Oct 2026 — and what each failure means to
// the caller: an answer, "no such account", or an error (= unknown).

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// factServer answers `path` with status and body, and records the headers.
type factServer struct {
	srv     *httptest.Server
	status  int
	body    string
	sawPath string
	sawUser string
	caller  string
	hits    int
}

func newFactServer(t *testing.T) *factServer {
	t.Helper()
	f := &factServer{status: http.StatusOK}
	f.srv = keyed(t, func(w http.ResponseWriter, r *http.Request) {
		f.hits++
		f.sawPath = r.URL.Path
		f.sawUser = r.Header.Get("X-User-Id") + r.Header.Get("X-Verified-User-Id")
		f.caller = r.Header.Get("X-Caller-Service")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	})
	return f
}

func (f *factServer) checkCall(t *testing.T, wantPath string) {
	t.Helper()
	if f.sawPath != wantPath {
		t.Fatalf("path %q, want %q", f.sawPath, wantPath)
	}
	if f.sawUser != "" {
		t.Fatalf("the call carried a user header; the identity read refuses those")
	}
	if f.caller != "live-service-v2" {
		t.Fatalf("X-Caller-Service = %q", f.caller)
	}
}

func TestHTTPBirthDates(t *testing.T) {
	u := uuid.New()
	f := newFactServer(t)
	c := NewHTTPBirthDates(f.srv.URL+"/", dirKey)
	body := func(dob, source string) string {
		return `{"data":{"user_id":"` + u.String() + `","first_name":"Asha","dob":` + dob + `,"dob_source":"` + source + `"}}`
	}

	f.body = body(`"1990-05-17"`, "registration")
	dob, found, err := c.BirthDate(ctx, u)
	if err != nil || !found || dob == nil || !dob.Equal(time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("registration dob: %v %v %v", dob, found, err)
	}
	f.checkCall(t, "/internal/v1/profiles/users/"+u.String()+"/identity")

	f.body = body(`"2001-01-02"`, "profile")
	if dob, found, err = c.BirthDate(ctx, u); err != nil || !found || dob == nil {
		t.Fatalf("profile dob: %v %v %v", dob, found, err)
	}
	// No date, or one nobody stands behind: found, but no date.
	for _, b := range []string{body(`null`, "none"), body(`""`, "registration"), body(`"1990-05-17"`, "none"), body(`"1990-05-17"`, "guess")} {
		f.body = b
		if dob, found, err = c.BirthDate(ctx, u); err != nil || !found || dob != nil {
			t.Fatalf("%s: %v %v %v", b, dob, found, err)
		}
	}
	// 404: no such account, or one that is shut or hidden.
	f.status, f.body = http.StatusNotFound, `{"error":{"code":"NOT_FOUND","message":"User not found"}}`
	if dob, found, err = c.BirthDate(ctx, u); err != nil || found || dob != nil {
		t.Fatalf("404: %v %v %v", dob, found, err)
	}
	// Everything else is an error, and no error quotes the date or the name.
	for status, b := range map[int]string{
		http.StatusInternalServerError: `{"error":{"code":"INTERNAL_ERROR"}}`,
		http.StatusUnauthorized:        `{"error":{"code":"UNAUTHORIZED"}}`,
		http.StatusForbidden:           `{"error":{"code":"USER_CALLER_REFUSED"}}`,
		http.StatusFound:               ``,
	} {
		f.status, f.body = status, b
		if _, _, err = c.BirthDate(ctx, u); err == nil {
			t.Fatalf("status %d read as an answer", status)
		}
	}
	f.status = http.StatusOK
	for _, b := range []string{
		`not json`, `{}`, `{"data":null}`,
		body(`"17/05/1990"`, "registration"),
		`{"data":{"user_id":"` + uuid.NewString() + `","first_name":"Asha","dob":"1990-05-17","dob_source":"registration"}}`,
	} {
		f.body = b
		_, _, err = c.BirthDate(ctx, u)
		if err == nil {
			t.Fatalf("%s read as an answer", b)
		}
		if msg := err.Error(); strings.Contains(msg, "1990") || strings.Contains(msg, "Asha") {
			t.Fatalf("the error quotes the body: %s", msg)
		}
	}
	// Unreachable.
	f.srv.Close()
	if _, _, err = c.BirthDate(ctx, u); err == nil {
		t.Fatalf("an unreachable service read as an answer")
	}
	if NewHTTPBirthDates("  ", dirKey) != nil {
		t.Fatalf("a client without a base URL")
	}
}

func TestHTTPAccounts(t *testing.T) {
	u := uuid.New()
	f := newFactServer(t)
	c := NewHTTPAccounts(f.srv.URL, dirKey)
	body := func(status, created string) string {
		return `{"data":{"id":"` + u.String() + `","status":"` + status + `","is_verified":false,"created_at":` + created + `,"updated_at":"2026-08-23T10:30:21.764611Z"}}`
	}

	f.body = body("active", `"2026-08-23T10:29:07.064151Z"`)
	got, err := c.Account(ctx, u)
	want := time.Date(2026, 8, 23, 10, 29, 7, 64151000, time.UTC)
	if err != nil || !got.Found || !got.Active || !got.CreatedAt.Equal(want) {
		t.Fatalf("active account: %+v %v", got, err)
	}
	f.checkCall(t, "/v1/users/"+u.String())

	// A deactivated or deletion-scheduled account is "hidden": it exists and
	// is not active. So is anything that is not exactly "active".
	for _, status := range []string{"hidden", "suspended", "pending_verification", "Active"} {
		f.body = body(status, `"2026-08-23T10:29:07Z"`)
		if got, err = c.Account(ctx, u); err != nil || !got.Found || got.Active {
			t.Fatalf("status %q: %+v %v", status, got, err)
		}
	}
	f.status, f.body = http.StatusNotFound, `{"error":{"code":"NOT_FOUND","message":"User not found"}}`
	if got, err = c.Account(ctx, u); err != nil || got.Found || got.Active {
		t.Fatalf("404: %+v %v", got, err)
	}
	f.status, f.body = http.StatusInternalServerError, `{"error":{"code":"INTERNAL_ERROR"}}`
	if _, err = c.Account(ctx, u); err == nil {
		t.Fatalf("a 500 read as an answer")
	}
	// A body without a creation time or a status is not an old, active
	// account.
	f.status = http.StatusOK
	for _, b := range []string{
		`{}`, `nope`,
		`{"data":{"id":"` + u.String() + `","status":"active"}}`,
		body("active", `null`),
		body("active", `"0001-01-01T00:00:00Z"`),
		body("", `"2026-08-23T10:29:07Z"`),
		`{"data":{"id":"` + uuid.NewString() + `","status":"active","created_at":"2020-01-01T00:00:00Z"}}`,
	} {
		f.body = b
		if got, err = c.Account(ctx, u); err == nil {
			t.Fatalf("%s read as %+v", b, got)
		}
	}
	if NewHTTPAccounts("", dirKey) != nil {
		t.Fatalf("a client without a base URL")
	}
}

func TestHTTPPostCounts(t *testing.T) {
	u := uuid.New()
	f := newFactServer(t)
	c := NewHTTPPostCounts(f.srv.URL, dirKey)

	f.body = `{"data":{"long_video":1,"post":7,"total":8}}`
	if n, err := c.PostCount(ctx, u); err != nil || n != 8 {
		t.Fatalf("counts: %d %v", n, err)
	}
	f.checkCall(t, "/v1/posts/by-author/"+u.String()+"/counts")
	// Nobody's author: post-service answers a total of zero.
	f.body = `{"data":{"total":0}}`
	if n, err := c.PostCount(ctx, u); err != nil || n != 0 {
		t.Fatalf("no posts: %d %v", n, err)
	}
	// A body without `total` is not zero posts.
	for _, b := range []string{`{}`, `{"data":{}}`, `{"data":{"post":7}}`, `{"data":{"total":-1}}`, `{"data":{"total":"many"}}`, `x`} {
		f.body = b
		if n, err := c.PostCount(ctx, u); err == nil {
			t.Fatalf("%s read as %d", b, n)
		}
	}
	f.status, f.body = http.StatusInternalServerError, `{"data":{"total":9}}`
	if n, err := c.PostCount(ctx, u); err == nil {
		t.Fatalf("a 500 read as %d", n)
	}
	if NewHTTPPostCounts("", dirKey) != nil {
		t.Fatalf("a client without a base URL")
	}
}

func TestHTTPFollowerCounts(t *testing.T) {
	u := uuid.New()
	f := newFactServer(t)
	c := NewHTTPFollowerCounts(f.srv.URL, dirKey)

	f.body = `{"data":{"user_id":"` + u.String() + `","follower_count":12,"following_count":3,"friend_count":3,"updated_at":"2026-10-02T06:11:17.548451Z"}}`
	if n, err := c.FollowerCount(ctx, u); err != nil || n != 12 {
		t.Fatalf("counts: %d %v", n, err)
	}
	f.checkCall(t, "/v1/graph/counts/"+u.String())
	// A user graph-service has no row for.
	f.body = `{"data":{"user_id":"` + u.String() + `","follower_count":0,"following_count":0,"friend_count":0,"updated_at":"0001-01-01T00:00:00Z"}}`
	if n, err := c.FollowerCount(ctx, u); err != nil || n != 0 {
		t.Fatalf("no followers: %d %v", n, err)
	}
	for _, b := range []string{
		`{}`, `x`,
		`{"data":{"user_id":"` + u.String() + `"}}`,
		`{"data":{"user_id":"` + uuid.NewString() + `","follower_count":99}}`,
		`{"data":{"user_id":"` + u.String() + `","follower_count":-4}}`,
	} {
		f.body = b
		if n, err := c.FollowerCount(ctx, u); err == nil {
			t.Fatalf("%s read as %d", b, n)
		}
	}
	f.status, f.body = http.StatusUnauthorized, ``
	if n, err := c.FollowerCount(ctx, u); err == nil {
		t.Fatalf("a 401 read as %d", n)
	}
	if NewHTTPFollowerCounts(" ", dirKey) != nil {
		t.Fatalf("a client without a base URL")
	}
}

// TestFactSourcesSendTheInternalKey: without the key the real services
// answer 401, which is an error, never an answer.
func TestFactSourcesSendTheInternalKey(t *testing.T) {
	u := uuid.New()
	f := newFactServer(t)
	f.body = `{"data":{"total":4}}`
	if _, err := NewHTTPPostCounts(f.srv.URL, "wrong-key").PostCount(ctx, u); err == nil {
		t.Fatalf("a wrong key was accepted")
	}
	if f.hits != 0 {
		t.Fatalf("the handler ran without the key")
	}
	if n, err := NewHTTPPostCounts(f.srv.URL, dirKey).PostCount(ctx, u); err != nil || n != 4 {
		t.Fatalf("with the key: %d %v", n, err)
	}
}
