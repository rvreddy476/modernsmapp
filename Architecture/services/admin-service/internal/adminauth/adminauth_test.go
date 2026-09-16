package adminauth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestStepUpWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	at := func(d time.Duration) string { return strconv.FormatInt(now.Add(d).Unix(), 10) }
	for name, tc := range map[string]struct {
		v    string
		want bool
	}{
		"just now":        {at(0), true},
		"299s ago":        {at(-299 * time.Second), true},
		"300s ago":        {at(-300 * time.Second), false},
		"an hour ago":     {at(-time.Hour), false},
		"20s ahead":       {at(20 * time.Second), true},
		"far future":      {at(time.Hour), false},
		"empty":           {"", false},
		"not a number":    {"2026-09-16T10:00:00Z", false},
		"zero":            {"0", false},
		"negative":        {"-5", false},
		"milliseconds ok": {at(0) + "000", false}, // a millisecond stamp is a far-future time
	} {
		until, ok := StepUpValidUntil(tc.v, now)
		if ok != tc.want {
			t.Fatalf("%s: valid=%v, want %v", name, ok, tc.want)
		}
		if ok {
			parsed, _ := ParseUnix(tc.v)
			if !until.Equal(parsed.Add(StepUpWindow)) {
				t.Fatalf("%s: valid until %v", name, until)
			}
		}
	}
}

func TestMFAHeader(t *testing.T) {
	for v, want := range map[string]bool{"true": true, "TRUE": true, " true ": true, "": false, "1": false, "yes": false, "false": false} {
		if MFAVerified(v) != want {
			t.Fatalf("MFAVerified(%q) != %v", v, want)
		}
	}
}

func TestRequireMFAFlag(t *testing.T) {
	cases := []struct {
		flag, env string
		want      bool
		err       bool
	}{
		{"", "", true, false},
		{"", "prod", true, false},
		{"", "staging", true, false},
		{"", "dev", false, false},
		{"", "local", false, false},
		{"", "Development", false, false},
		{"true", "dev", true, false},
		{"false", "prod", false, false},
		{"flase", "prod", false, true},
	}
	for _, tc := range cases {
		got, err := RequireMFAFromEnv(func(k string) string {
			return map[string]string{"ADMIN_REQUIRE_MFA": tc.flag, "ENV": tc.env}[k]
		})
		if (err != nil) != tc.err || (err == nil && got != tc.want) {
			t.Fatalf("flag=%q env=%q: got %v err %v", tc.flag, tc.env, got, err)
		}
	}
}

func TestPermissionHasIsExactAndPerApp(t *testing.T) {
	p := Permissions{Apps: map[string][]string{"dating": {"dating:reports.act"}}, Platform: []string{"*:audit.read", "platform:users.read"}}
	for perm, want := range map[string]bool{
		"dating:reports.act":   true,
		"commerce:reports.act": false,
		"dating:reports":       false,
		"*:audit.read":         true,
		"platform:users.read":  true,
		"commerce:audit.read":  false,
		"":                     false,
	} {
		if p.Has(perm) != want {
			t.Fatalf("Has(%q) != %v", perm, want)
		}
	}
}

type identityStub struct {
	mu     sync.Mutex
	status int
	body   string
	path   string
	query  string
	key    string
	userH  []string
	hits   int
}

func (s *identityStub) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.hits++
		s.path, s.query, s.key = r.URL.EscapedPath(), r.URL.RawQuery, r.Header.Get("X-Internal-Service-Key")
		for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
			if r.Header.Get(h) != "" {
				s.userH = append(s.userH, h)
			}
		}
		w.WriteHeader(s.status)
		_, _ = w.Write([]byte(s.body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

const uid = "7b0c6c1e-0f3a-4d59-9a57-3c1f7d1c2b10"

func TestIdentityClientReadsPermissionsAsAServiceOnly(t *testing.T) {
	s := &identityStub{status: 200, body: `{"data":{"user_id":"` + uid + `","admin":{"apps":{"commerce":["commerce:seller.approve"]},"platform":["*:audit.read"]}}}`}
	c := NewIdentityClient(s.server(t).URL, "the-key")
	p, err := c.UserPermissions(context.Background(), uid)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Has("commerce:seller.approve") || !p.Has("*:audit.read") || p.UserID != uid {
		t.Fatalf("permissions %+v", p)
	}
	if s.path != "/v1/auth/internal/users/"+uid+"/permissions" || s.key != "the-key" || len(s.userH) != 0 {
		t.Fatalf("path=%s key sent=%v user headers=%v", s.path, s.key == "the-key", s.userH)
	}
}

func TestIdentityClientErrorsAreNeverAnEmptyAnswer(t *testing.T) {
	for name, s := range map[string]*identityStub{
		"503":           {status: 503, body: `{"error":{"code":"PERMISSIONS_UNAVAILABLE"}}`},
		"403 user":      {status: 403, body: `{"error":{"code":"USER_CALLER_REFUSED"}}`},
		"200 garbage":   {status: 200, body: `<html>`},
		"200 no admin":  {status: 200, body: `{"data":{"user_id":"` + uid + `"}}`},
		"200 wrong uid": {status: 200, body: `{"data":{"user_id":"00000000-0000-4000-8000-000000000000","admin":{"apps":{},"platform":["*:audit.read"]}}}`},
		"200 null":      {status: 200, body: `{"data":null}`},
	} {
		c := NewIdentityClient(s.server(t).URL, "k")
		if _, err := c.UserPermissions(context.Background(), uid); !errors.Is(err, ErrIdentityUnavailable) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	if _, err := NewIdentityClient("http://127.0.0.1:1", "k").UserPermissions(context.Background(), uid); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("unreachable: err = %v", err)
	}
}

func TestHoldersClient(t *testing.T) {
	s := &identityStub{status: 200, body: `{"data":{"permission":"commerce:cod.settle","count":2}}`}
	c := NewIdentityClient(s.server(t).URL, "k")
	n, err := c.OtherTOTPHolders(context.Background(), "commerce:cod.settle", uid)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	if s.path != "/v1/auth/internal/permissions/commerce:cod.settle/holders" || s.query != "exclude_user_id="+uid || len(s.userH) != 0 {
		t.Fatalf("path=%s query=%s user headers=%v", s.path, s.query, s.userH)
	}

	for name, body := range map[string]string{
		"missing count":    `{"data":{"permission":"commerce:cod.settle"}}`,
		"negative":         `{"data":{"permission":"commerce:cod.settle","count":-1}}`,
		"other permission": `{"data":{"permission":"commerce:seller.approve","count":0}}`,
		"string count":     `{"data":{"permission":"commerce:cod.settle","count":"0"}}`,
	} {
		s := &identityStub{status: 200, body: body}
		if _, err := NewIdentityClient(s.server(t).URL, "k").OtherTOTPHolders(context.Background(), "commerce:cod.settle", uid); !errors.Is(err, ErrIdentityUnavailable) {
			t.Fatalf("%s: err = %v", name, err)
		}
	}
	s404 := &identityStub{status: 404, body: `{}`}
	if _, err := NewIdentityClient(s404.server(t).URL, "k").OtherTOTPHolders(context.Background(), "commerce:cod.settle", uid); !errors.Is(err, ErrIdentityUnavailable) {
		t.Fatalf("404 (route not deployed yet): err = %v", err)
	}
}

type countingSource struct {
	calls int
	err   error
}

func (c *countingSource) UserPermissions(_ context.Context, id string) (Permissions, error) {
	c.calls++
	if c.err != nil {
		return Permissions{}, c.err
	}
	return Permissions{UserID: id, Platform: []string{"platform:users.read"}}, nil
}

func TestCacheKeepsSuccessesBrieflyAndNeverErrors(t *testing.T) {
	src := &countingSource{}
	c := NewCachedPermissions(src, 10*time.Second)
	now := time.Unix(1_800_000_000, 0)
	c.now = func() time.Time { return now }
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.UserPermissions(ctx, uid); err != nil {
			t.Fatal(err)
		}
	}
	if src.calls != 1 {
		t.Fatalf("calls = %d, want 1 within the TTL", src.calls)
	}
	now = now.Add(11 * time.Second)
	_, _ = c.UserPermissions(ctx, uid)
	if src.calls != 2 {
		t.Fatalf("calls = %d, want a refresh after the TTL", src.calls)
	}

	src.err = errors.New("down")
	now = now.Add(11 * time.Second)
	for i := 0; i < 2; i++ {
		if _, err := c.UserPermissions(ctx, uid); err == nil {
			t.Fatal("an error became an answer")
		}
	}
	if src.calls != 4 {
		t.Fatalf("calls = %d, errors must not be cached", src.calls)
	}

	if NewCachedPermissions(src, time.Hour).ttl > 30*time.Second {
		t.Fatal("TTL above 30s accepted")
	}
}
