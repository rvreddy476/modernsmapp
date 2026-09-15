package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Distinctive values so a leak into a log line or an error is unambiguous.
const (
	testIdentityDOB  = "1989-11-23"
	testIdentityName = "Zanobiaqx"
	testIdentityKey  = "test-internal-key"
)

type identityFake struct {
	status int
	body   func(userID string) string
	hits   atomic.Int32
	last   atomic.Pointer[http.Request]
}

func (f *identityFake) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.hits.Add(1)
	f.last.Store(r.Clone(context.Background()))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(f.status)
	if f.body != nil {
		parts := strings.Split(r.URL.Path, "/")
		uid := ""
		if len(parts) >= 2 {
			uid = parts[len(parts)-2]
		}
		_, _ = w.Write([]byte(f.body(uid)))
	}
}

func okIdentityBody(dob, source string) func(string) string {
	return func(uid string) string {
		d := "null"
		if dob != "" {
			d = `"` + dob + `"`
		}
		return fmt.Sprintf(`{"data":{"user_id":%q,"first_name":%q,"dob":%s,"dob_source":%q}}`, uid, testIdentityName, d, source)
	}
}

func newIdentityTestClient(t *testing.T, f *identityFake) (*HTTPIdentityClient, *bytes.Buffer) {
	t.Helper()
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return NewHTTPIdentityClient(srv.URL+"/", testIdentityKey, logger), logs
}

// TestHTTPIdentityClient_PathHeadersAndDecode: the right path, the internal
// key, the caller label, and no end-user identity header of any kind.
func TestHTTPIdentityClient_PathHeadersAndDecode(t *testing.T) {
	f := &identityFake{status: http.StatusOK, body: okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)}
	c, _ := newIdentityTestClient(t, f)
	user := uuid.New()

	// A context from an inbound user request must not leak into the call.
	got, err := c.GetIdentityBasics(context.WithValue(context.Background(), struct{ k string }{"X-User-Id"}, user.String()), user)
	if err != nil {
		t.Fatalf("GetIdentityBasics: %v", err)
	}
	r := f.last.Load()
	if r.Method != http.MethodGet {
		t.Errorf("method = %s, want GET", r.Method)
	}
	if want := "/internal/v1/profiles/users/" + user.String() + "/identity"; r.URL.Path != want {
		t.Errorf("path = %q, want %q", r.URL.Path, want)
	}
	if r.Header.Get("X-Internal-Service-Key") != testIdentityKey {
		t.Errorf("X-Internal-Service-Key not sent")
	}
	if got := r.Header.Get("X-Caller-Service"); got != "dating-service" {
		t.Errorf("X-Caller-Service = %q, want dating-service", got)
	}
	for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role", "Authorization", "Cookie"} {
		if v := r.Header.Values(h); len(v) > 0 {
			t.Errorf("user header %s sent (%d values); identity refuses those", h, len(v))
		}
	}
	for name := range r.Header {
		if strings.HasPrefix(strings.ToLower(name), "x-") && name != "X-Internal-Service-Key" && name != "X-Caller-Service" {
			t.Errorf("unexpected X- header %s sent", name)
		}
	}

	if !got.Found || got.FirstName != testIdentityName || got.DOBSource != IdentityDOBSourceRegistration ||
		got.BirthDate == nil || got.BirthDate.Format("2006-01-02") != testIdentityDOB {
		t.Fatalf("decoded basics do not match the identity body")
	}

	// profile source and a null dob.
	f.body = okIdentityBody(testIdentityDOB, IdentityDOBSourceProfile)
	if got, err = c.GetIdentityBasics(context.Background(), user); err != nil || got.DOBSource != IdentityDOBSourceProfile || got.BirthDate == nil {
		t.Fatalf("profile source: err=%v", err)
	}
	f.body = okIdentityBody("", IdentityDOBSourceNone)
	if got, err = c.GetIdentityBasics(context.Background(), user); err != nil || !got.Found || got.BirthDate != nil || got.DOBSource != IdentityDOBSourceNone {
		t.Fatalf("none source: err=%v", err)
	}
}

// TestHTTPIdentityClient_404IsNoIdentity: 404 is a typed "no identity", not
// an error.
func TestHTTPIdentityClient_404IsNoIdentity(t *testing.T) {
	c, _ := newIdentityTestClient(t, &identityFake{status: http.StatusNotFound, body: func(string) string {
		return `{"error":{"code":"NOT_FOUND"}}`
	}})
	got, err := c.GetIdentityBasics(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("404: err=%v, want nil", err)
	}
	if got == nil || got.Found || got.BirthDate != nil {
		t.Fatalf("404: got %+v, want Found=false and no birth date", got)
	}
}

// TestHTTPIdentityClient_ErrorClasses: 5xx / 429 / network / timeout /
// contract breaks are transient; 400 / 401 / 403 / redirects are
// misconfiguration, logged once.
func TestHTTPIdentityClient_ErrorClasses(t *testing.T) {
	for _, status := range []int{500, 502, 503, 504, 429} {
		c, _ := newIdentityTestClient(t, &identityFake{status: status})
		if _, err := c.GetIdentityBasics(context.Background(), uuid.New()); !errors.Is(err, ErrIdentityTransient) {
			t.Errorf("status %d: err=%v, want ErrIdentityTransient", status, err)
		}
	}

	for _, status := range []int{400, 401, 403, 302} {
		f := &identityFake{status: status}
		c, logs := newIdentityTestClient(t, f)
		for i := 0; i < 3; i++ {
			_, err := c.GetIdentityBasics(context.Background(), uuid.New())
			if !errors.Is(err, ErrIdentityMisconfigured) || errors.Is(err, ErrIdentityTransient) {
				t.Errorf("status %d: err=%v, want ErrIdentityMisconfigured only", status, err)
			}
		}
		if n := strings.Count(logs.String(), "identity-profile refused"); n != 1 {
			t.Errorf("status %d: misconfiguration logged %d times, want once", status, n)
		}
	}

	// Network: nothing listening.
	srv := httptest.NewServer(http.NotFoundHandler())
	dead := srv.URL
	srv.Close()
	if _, err := NewHTTPIdentityClient(dead, testIdentityKey, nil).GetIdentityBasics(context.Background(), uuid.New()); !errors.Is(err, ErrIdentityTransient) {
		t.Errorf("network: err=%v, want ErrIdentityTransient", err)
	}

	// Timeout (shortened from the 2 s production bound).
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	defer slow.Close()
	sc := NewHTTPIdentityClient(slow.URL, testIdentityKey, nil)
	if sc.http.Timeout != IdentityProfileTimeout || IdentityProfileTimeout != 2*time.Second {
		t.Fatalf("client timeout = %v, want 2s", sc.http.Timeout)
	}
	sc.http.Timeout = 50 * time.Millisecond
	if _, err := sc.GetIdentityBasics(context.Background(), uuid.New()); !errors.Is(err, ErrIdentityTransient) {
		t.Errorf("timeout: err=%v, want ErrIdentityTransient", err)
	}

	// Contract breaks.
	for name, body := range map[string]func(string) string{
		"bad dob":        okIdentityBody("1989-13-45", IdentityDOBSourceRegistration),
		"unknown source": okIdentityBody(testIdentityDOB, "selfie"),
		"other user": func(string) string {
			return okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)(uuid.NewString())
		},
		"not json": func(string) string { return "<html>" + testIdentityName + "</html>" },
		"no data":  func(string) string { return `{"dob":"` + testIdentityDOB + `"}` },
	} {
		c, _ := newIdentityTestClient(t, &identityFake{status: http.StatusOK, body: body})
		if _, err := c.GetIdentityBasics(context.Background(), uuid.New()); !errors.Is(err, ErrIdentityTransient) {
			t.Errorf("%s: err=%v, want ErrIdentityTransient", name, err)
		}
	}
}

// TestHTTPIdentityClient_NeverLogsOrReturnsDOBOrName drives every outcome
// with debug logging captured and checks neither value appears in a log line
// or an error message.
func TestHTTPIdentityClient_NeverLogsOrReturnsDOBOrName(t *testing.T) {
	logs := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	var messages []string

	bodies := []struct {
		status int
		body   func(string) string
	}{
		{200, okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)},
		{200, okIdentityBody(testIdentityDOB, IdentityDOBSourceProfile)},
		{200, okIdentityBody(testIdentityDOB+"x", IdentityDOBSourceRegistration)},
		{200, okIdentityBody(testIdentityDOB, "unknown-"+testIdentityName)},
		{200, func(string) string {
			return `{"data":{"user_id":"` + testIdentityName + `","dob":"` + testIdentityDOB + `"}}`
		}},
		{404, okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)},
		{401, okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)},
		{403, okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)},
		{500, okIdentityBody(testIdentityDOB, IdentityDOBSourceRegistration)},
	}
	for _, b := range bodies {
		srv := httptest.NewServer(&identityFake{status: b.status, body: b.body})
		c := NewHTTPIdentityClient(srv.URL, testIdentityKey, logger)
		got, err := c.GetIdentityBasics(context.Background(), uuid.New())
		if err != nil {
			messages = append(messages, err.Error())
		}
		if got != nil {
			// The service logs only errors; make sure a formatted result
			// is not what reaches a log through some error wrapper.
			_ = got
		}
		srv.Close()
	}

	// The service-level warning path wraps the client error.
	messages = append(messages, fmt.Errorf("%w: %v", ErrIdentityUnavailable, errors.New(strings.Join(messages, "; "))).Error())

	all := logs.String() + "\n" + strings.Join(messages, "\n")
	if logs.Len() == 0 {
		t.Fatalf("log capture is empty; the misconfiguration line should have been captured")
	}
	for _, secret := range []string{testIdentityDOB, testIdentityName, "1989", testIdentityKey} {
		if strings.Contains(all, secret) {
			t.Fatalf("a log line or error message contains an identity value or the key (%d bytes of output checked)", len(all))
		}
	}
}
