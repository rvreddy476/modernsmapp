package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func req(remote string, xff []string, realIP string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/v1/auth/login", nil)
	r.RemoteAddr = remote
	for _, l := range xff {
		r.Header.Add("X-Forwarded-For", l)
	}
	if realIP != "" {
		r.Header.Set("X-Real-IP", realIP)
	}
	return r
}

func mustNew(t *testing.T, cidrs []string, hops int) Resolver {
	t.Helper()
	r, err := New(cidrs, hops)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestResolveCIDR(t *testing.T) {
	alb := mustNew(t, []string{"10.30.0.0/16", "fd00::/8"}, 0)
	cases := []struct {
		name   string
		remote string
		xff    []string
		realIP string
		want   string
	}{
		{"no XFF: the peer", "10.30.1.1:80", nil, "", "10.30.1.1"},
		{"ALB appended the client", "10.30.1.1:80", []string{"203.0.113.4"}, "", "203.0.113.4"},
		{"forged leftmost hop ignored", "10.30.1.1:80", []string{"198.51.100.1, 203.0.113.4"}, "", "203.0.113.4"},
		{"forged trusted-looking hop ignored", "10.30.1.1:80", []string{"10.30.9.9, 203.0.113.4"}, "", "203.0.113.4"},
		{"forged X-Real-IP ignored", "10.30.1.1:80", []string{"203.0.113.4"}, "198.51.100.1", "203.0.113.4"},
		{"several header lines are one list", "10.30.1.1:80", []string{"198.51.100.1", "203.0.113.4"}, "", "203.0.113.4"},
		{"chain of trusted proxies walked", "10.30.1.1:80", []string{"198.51.100.1, 203.0.113.4, 10.30.2.2"}, "", "203.0.113.4"},
		{"untrusted peer: headers ignored", "203.0.113.50:80", []string{"198.51.100.1"}, "198.51.100.2", "203.0.113.50"},
		{"garbage rightmost: the trusted peer", "10.30.1.1:80", []string{"198.51.100.1, nonsense"}, "", "10.30.1.1"},
		{"all trusted: the leftmost", "10.30.1.1:80", []string{"10.30.3.3"}, "", "10.30.3.3"},
		{"IPv6 client", "[fd00::1]:80", []string{"2001:db8::7"}, "", "2001:db8::7"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := alb.Resolve(req(tc.remote, tc.xff, tc.realIP)); got != tc.want {
				t.Errorf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveHops(t *testing.T) {
	one := mustNew(t, nil, 1)
	two := mustNew(t, nil, 2)
	cases := []struct {
		name string
		r    Resolver
		xff  string
		want string
	}{
		{"one hop: rightmost", one, "198.51.100.1, 203.0.113.4", "203.0.113.4"},
		{"two hops: second from right", two, "198.51.100.1, 203.0.113.4, 10.0.0.9", "203.0.113.4"},
		{"too few entries: the peer", two, "203.0.113.4", "192.0.2.10"},
		{"no header: the peer", one, "", "192.0.2.10"},
		{"garbage at the hop: the peer", one, "nonsense", "192.0.2.10"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var xff []string
			if tc.xff != "" {
				xff = []string{tc.xff}
			}
			if got := tc.r.Resolve(req("192.0.2.10:5000", xff, "198.51.100.3")); got != tc.want {
				t.Errorf("Resolve = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestZeroResolverTrustsNoHeader(t *testing.T) {
	var r Resolver
	if r.TrustsProxies() {
		t.Fatal("zero resolver trusts proxies")
	}
	if got := r.Resolve(req("192.0.2.10:5000", []string{"203.0.113.4"}, "203.0.113.5")); got != "192.0.2.10" {
		t.Fatalf("Resolve = %q, want the peer", got)
	}
}

func TestFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	r, err := FromEnv(env(map[string]string{EnvTrustedCIDRs: " 10.30.0.0/16 , ,10.31.0.0/16"}))
	if err != nil || len(r.trusted) != 2 || r.hops != 0 {
		t.Fatalf("CIDRs: %+v err=%v", r, err)
	}
	r, err = FromEnv(env(map[string]string{EnvTrustedHops: "1"}))
	if err != nil || r.hops != 1 {
		t.Fatalf("hops: %+v err=%v", r, err)
	}
	r, err = FromEnv(env(nil))
	if err != nil || r.TrustsProxies() {
		t.Fatalf("unset: %+v err=%v", r, err)
	}
	for name, m := range map[string]map[string]string{
		"bad cidr":   {EnvTrustedCIDRs: "10.30.0.0/99"},
		"bare ip":    {EnvTrustedCIDRs: "10.30.0.1"},
		"bad hops":   {EnvTrustedHops: "one"},
		"neg hops":   {EnvTrustedHops: "-1"},
		"huge hops":  {EnvTrustedHops: "50"},
		"both modes": {EnvTrustedCIDRs: "10.30.0.0/16", EnvTrustedHops: "1"},
	} {
		if _, err := FromEnv(env(m)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestMiddlewareRewritesForwardingHeaders(t *testing.T) {
	alb := mustNew(t, []string{"10.30.0.0/16"}, 0)
	var seen *http.Request
	h := Middleware(alb, http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { seen = r }))
	r := req("10.30.1.1:80", []string{"198.51.100.1, 203.0.113.4"}, "198.51.100.2")
	r.Header.Set("Forwarded", "for=198.51.100.3")
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got := FromRequest(seen); got != "203.0.113.4" {
		t.Errorf("FromRequest = %q", got)
	}
	if got := seen.Header.Values("X-Forwarded-For"); len(got) != 1 || got[0] != "203.0.113.4" {
		t.Errorf("X-Forwarded-For = %q, want only the resolved client", got)
	}
	if got := seen.Header.Get("X-Real-IP"); got != "203.0.113.4" {
		t.Errorf("X-Real-IP = %q", got)
	}
	if got := seen.Header.Get("Forwarded"); got != "" {
		t.Errorf("Forwarded = %q, want removed", got)
	}
	if !ArrivedForwarded(seen) {
		t.Error("ArrivedForwarded = false for a proxied request")
	}

	// Without the middleware: the peer, never a header.
	plain := req("192.0.2.10:5000", []string{"203.0.113.4"}, "203.0.113.5")
	if got := FromRequest(plain); got != "192.0.2.10" {
		t.Errorf("FromRequest without middleware = %q, want the peer", got)
	}
	// A direct request did not arrive forwarded, even after the rewrite.
	h.ServeHTTP(httptest.NewRecorder(), req("10.30.1.1:80", nil, ""))
	if ArrivedForwarded(seen) {
		t.Error("ArrivedForwarded = true for a direct request")
	}
}
