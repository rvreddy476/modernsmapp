package delivery

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// MTube download: the post-service question and the attachment signature.

func downloadServer(t *testing.T, status int, body string, seen *url.Values, seenKey *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DownloadAllowedPath || r.Method != http.MethodGet {
			t.Errorf("asked %s %s, want GET %s", r.Method, r.URL.Path, DownloadAllowedPath)
		}
		if seen != nil {
			*seen = r.URL.Query()
		}
		if seenKey != nil {
			*seenKey = r.Header.Get("X-Internal-Service-Key")
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func TestHTTPDownloadAuthorizerAsksPostServiceWithTheViewerAndKey(t *testing.T) {
	var q url.Values
	var key string
	srv := downloadServer(t, http.StatusOK, `{"allowed":true}`, &q, &key)
	defer srv.Close()
	a := NewHTTPDownloadAuthorizer(srv.URL, "k-1", nil)
	if err := a.AllowDownload(context.Background(), "viewer-1", "media-1"); err != nil {
		t.Fatalf("allowed=true should pass: %v", err)
	}
	if q.Get("viewer_id") != "viewer-1" || q.Get("media_id") != "media-1" || key != "k-1" {
		t.Fatalf("request carried viewer=%q media=%q key=%q", q.Get("viewer_id"), q.Get("media_id"), key)
	}
}

func TestHTTPDownloadAuthorizerFailsClosed(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   error
	}{
		{"200 allowed false", http.StatusOK, `{"allowed":false}`, ErrDeliveryDenied},
		{"200 empty body", http.StatusOK, ``, ErrDeliveryUnresolved},
		{"200 missing field", http.StatusOK, `{}`, ErrDeliveryDenied},
		{"403", http.StatusForbidden, `{"allowed":false}`, ErrDeliveryDenied},
		{"404", http.StatusNotFound, ``, ErrDeliveryDenied},
		{"500", http.StatusInternalServerError, ``, ErrDeliveryUnresolved},
		{"503", http.StatusServiceUnavailable, ``, ErrDeliveryUnresolved},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := downloadServer(t, tc.status, tc.body, nil, nil)
			defer srv.Close()
			err := NewHTTPDownloadAuthorizer(srv.URL, "", nil).AllowDownload(context.Background(), "v", "m")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHTTPDownloadAuthorizerAcceptsTheEnvelopeShape(t *testing.T) {
	srv := downloadServer(t, http.StatusOK, `{"data":{"allowed":true}}`, nil, nil)
	defer srv.Close()
	if err := NewHTTPDownloadAuthorizer(srv.URL, "", nil).AllowDownload(context.Background(), "v", "m"); err != nil {
		t.Fatalf("enveloped yes should pass: %v", err)
	}
}

func TestHTTPDownloadAuthorizerRefusesAnonymousWithoutAsking(t *testing.T) {
	asked := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = true
		_, _ = w.Write([]byte(`{"allowed":true}`))
	}))
	defer srv.Close()
	a := NewHTTPDownloadAuthorizer(srv.URL, "", nil)
	for _, viewer := range []string{"", nilViewerID} {
		if err := a.AllowDownload(context.Background(), viewer, "m"); !errors.Is(err, ErrDeliveryDenied) {
			t.Fatalf("viewer %q: got %v, want denied", viewer, err)
		}
	}
	if asked {
		t.Fatal("post-service was asked about an anonymous download")
	}
}

func TestHTTPDownloadAuthorizerUnreachableIsUnresolved(t *testing.T) {
	a := NewHTTPDownloadAuthorizer("http://127.0.0.1:1", "", &http.Client{Timeout: 200 * time.Millisecond})
	if err := a.AllowDownload(context.Background(), "v", "m"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want unresolved", err)
	}
	var nilAuthz *HTTPDownloadAuthorizer
	if err := nilAuthz.AllowDownload(context.Background(), "v", "m"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("nil authorizer: got %v, want unresolved", err)
	}
}

func TestGateDownloadRequiresAnAuthorizer(t *testing.T) {
	g := NewGate(gateSigner(t), fakeAuthz{allow: true})
	if err := g.AuthorizeDownload(context.Background(), "v", "m"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("no download authorizer: got %v, want unresolved (never allowed)", err)
	}
	g.WithDownloadAuthorizer(fakeDownloadAuthz{err: ErrDeliveryDenied})
	if err := g.AuthorizeDownload(context.Background(), "v", "m"); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want denied", err)
	}
	g.WithDownloadAuthorizer(fakeDownloadAuthz{})
	if err := g.AuthorizeDownload(context.Background(), "v", "m"); err != nil {
		t.Fatalf("got %v, want allowed", err)
	}
}

type fakeDownloadAuthz struct{ err error }

func (f fakeDownloadAuthz) AllowDownload(context.Context, string, string) error { return f.err }

func TestSignProtectedDownloadCarriesTheDispositionInsideTheSignature(t *testing.T) {
	s := gateSigner(t)
	signed, err := s.SignProtectedDownload("uploads/u1/m1/720p", MaxProtectedTTL, time.Unix(1_700_000_000, 0), "m1.mp4")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	u, err := url.Parse(signed)
	if err != nil {
		t.Fatalf("parse %q: %v", signed, err)
	}
	q := u.Query()
	if got := q.Get("response-content-disposition"); got != `attachment; filename="m1.mp4"` {
		t.Fatalf("disposition = %q", got)
	}
	for _, k := range []string{"Expires", "Signature", "Key-Pair-Id"} {
		if q.Get(k) == "" {
			t.Errorf("signed URL lacks %s: %s", k, signed)
		}
	}
	if strings.Count(signed, "?") != 1 {
		t.Fatalf("query joined with a second '?': %s", signed)
	}
	// Same base URL as the playback signature, so the CDN path is unchanged.
	if !strings.HasPrefix(signed, "https://d111.cloudfront.net/uploads/u1/m1/720p?") {
		t.Fatalf("unexpected resource: %s", signed)
	}
}

func TestGateSignDownloadNeedsADownloadSigner(t *testing.T) {
	g := NewGate(plainSigner{}, nil)
	if _, err := g.SignDownload("k", "f.mp4"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("a signer without attachment support must not fall back to a plain URL: %v", err)
	}
	g = NewGate(gateSigner(t), nil)
	signed, err := g.SignDownload("uploads/u1/m1/720p", "m1.mp4")
	if err != nil || !strings.Contains(signed, "response-content-disposition=") {
		t.Fatalf("signed=%q err=%v", signed, err)
	}
}

type plainSigner struct{}

func (plainSigner) PublicURL(key string) (string, error) { return "https://cdn/" + key, nil }
func (plainSigner) SignProtected(key string, _ time.Duration, _ time.Time) (string, error) {
	return "https://cdn/" + key + "?sig", nil
}

func TestContentDispositionAttachmentIsHeaderSafe(t *testing.T) {
	if got := ContentDispositionAttachment("abc-123_x.mp4"); got != `attachment; filename="abc-123_x.mp4"` {
		t.Fatalf("plain name mangled: %s", got)
	}
	if got := ContentDispositionAttachment("a\"b\r\nc.mp4"); got != `attachment; filename="a_b__c.mp4"` {
		t.Fatalf("unsafe characters not replaced: %s", got)
	}
	if got := ContentDispositionAttachment(""); got != `attachment; filename="download"` {
		t.Fatalf("empty name: %s", got)
	}
}
