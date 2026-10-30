package edgeheaders

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func cookieReq(lines ...string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	for _, l := range lines {
		req.Header.Add("Cookie", l)
	}
	return req
}

func TestRefreshCookiesStrippedForNonAuthUpstreams(t *testing.T) {
	cases := []struct {
		name  string
		lines []string
		want  []string // remaining Cookie header lines
	}{
		{"mixed", []string{"access_token=a; refresh_token=r; csrf_token=c"}, []string{"access_token=a; csrf_token=c"}},
		{"admin refresh", []string{"admin_access_token=a; admin_refresh_token=r"}, []string{"admin_access_token=a"}},
		{"only refresh", []string{"refresh_token=r"}, nil},
		{"several lines", []string{"refresh_token=r", "theme=dark"}, []string{"theme=dark"}},
		{"spacing", []string{" refresh_token = r ;x=1"}, []string{"x=1"}},
		{"similar names kept", []string{"refresh_token_hint=1; my_refresh_token=2"}, []string{"refresh_token_hint=1; my_refresh_token=2"}},
		{"no cookies", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := cookieReq(tc.lines...)
			StripRefreshCookies(req, "/v1/feed")
			got := req.Header.Values("Cookie")
			if len(got) != len(tc.want) {
				t.Fatalf("Cookie = %q, want %q", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("Cookie = %q, want %q", got, tc.want)
				}
			}
			for _, c := range req.Cookies() {
				if c.Name == "refresh_token" || c.Name == "admin_refresh_token" {
					t.Fatalf("refresh cookie %q still forwarded", c.Name)
				}
			}
		})
	}
}

func TestRefreshCookiesKeptForAuthService(t *testing.T) {
	req := cookieReq("access_token=a; refresh_token=r; admin_refresh_token=ar")
	StripRefreshCookies(req, AuthRoutePrefix)
	if got := req.Header.Get("Cookie"); got != "access_token=a; refresh_token=r; admin_refresh_token=ar" {
		t.Fatalf("auth-service Cookie = %q, want it untouched", got)
	}
	// A near-prefix is not auth-service.
	req = cookieReq("refresh_token=r")
	StripRefreshCookies(req, "/v1/authx")
	if got := req.Header.Get("Cookie"); got != "" {
		t.Fatalf("/v1/authx Cookie = %q, want refresh stripped", got)
	}
}
