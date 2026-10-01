package media

// The KYC image read, against a stand-in media-service.
//
// media-service's GET /v1/media/internal/:mediaId/image-bytes is being built
// to a pinned shape at the same time as this client; these tests are that
// shape, from commerce's side: the path, the two credentials, the refusals
// that must never be passed on as an image, and the size cap.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// jpegish is enough bytes to be recognisably "the image" in an assertion.
var jpegish = append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte("kyc"), 300)...)

func imageServer(t *testing.T, h http.HandlerFunc) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(h)
	t.Cleanup(s.Close)
	return s
}

func TestFetchImageBytesCallsThePinnedRouteWithTheInternalKey(t *testing.T) {
	id := uuid.New()
	var gotPath, gotMethod, gotKey, gotAuth string
	var userHeaders []string
	s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod = r.URL.Path, r.Method
		gotKey, gotAuth = r.Header.Get("X-Internal-Service-Key"), r.Header.Get("X-Service-Authorization")
		// media-service answers 403 USER_CALLER_REFUSED to any of these.
		for _, h := range []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"} {
			if _, present := r.Header[http.CanonicalHeaderKey(h)]; present {
				userHeaders = append(userHeaders, h)
			}
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(jpegish)
	})
	// A viewer on the context — what every commerce request carries — must
	// NOT turn into a user header on this call.
	ctx := WithViewer(context.Background(), uuid.New())
	body, ct, err := New(s.URL, "the-key").FetchImageBytes(ctx, id)
	if err != nil {
		t.Fatalf("FetchImageBytes: %v", err)
	}
	defer body.Close()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "/v1/media/internal/" + id.String() + "/image-bytes"; gotPath != want || gotMethod != http.MethodGet {
		t.Fatalf("called %s %s, want GET %s", gotMethod, gotPath, want)
	}
	if gotKey != "the-key" {
		t.Fatalf("X-Internal-Service-Key = %q; the /v1/media/internal group refuses a call without it", gotKey)
	}
	if gotAuth != "" {
		t.Fatalf("a client without a signer sent a service token: %q", gotAuth)
	}
	if len(userHeaders) != 0 {
		t.Fatalf("sent user headers %v; media-service refuses the route with 403 USER_CALLER_REFUSED", userHeaders)
	}
	if ct != "image/jpeg" || !bytes.Equal(got, jpegish) {
		t.Fatalf("got %q / %d bytes, want image/jpeg / %d bytes", ct, len(got), len(jpegish))
	}
}

func TestFetchImageBytesPresentsACommerceServiceToken(t *testing.T) {
	pub, priv, err := servicetoken.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ServiceTokenSigner("c1", priv)
	if err != nil || signer == nil {
		t.Fatalf("ServiceTokenSigner: %v %v", signer, err)
	}
	// media-service's side, as the contract pins it: audience media, issuer
	// commerce-service, operation media:image-bytes.
	v := servicetoken.NewVerifier(ImageBytesAudience)
	if err := v.RegisterBase64("commerce-service", "c1", pub, []string{ImageBytesOperation}, nil); err != nil {
		t.Fatal(err)
	}
	var verified *servicetoken.Verified
	var verr error
	s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
		raw := strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		verified, verr = v.Verify(raw, ImageBytesOperation, "")
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write([]byte("png"))
	})
	body, _, err := New(s.URL, "k").WithServiceToken(signer).FetchImageBytes(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("FetchImageBytes: %v", err)
	}
	body.Close()
	if verr != nil {
		t.Fatalf("media-service could not verify the token: %v", verr)
	}
	if verified.Issuer != "commerce-service" {
		t.Fatalf("issuer = %q, want commerce-service", verified.Issuer)
	}
}

func TestServiceTokenSignerIsAbsentWithoutAKey(t *testing.T) {
	for _, tc := range [][2]string{{"", ""}, {"kid", ""}, {"", "key"}} {
		s, err := ServiceTokenSigner(tc[0], tc[1])
		if s != nil || err != nil {
			t.Fatalf("ServiceTokenSigner(%q, %q) = %v, %v; want nil, nil", tc[0], tc[1], s, err)
		}
	}
	if _, err := ServiceTokenSigner("kid", "not base64!"); err == nil {
		t.Fatal("an unparseable key was accepted")
	}
}

func TestFetchImageBytesRefusals(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		ctype   string
		wantErr error
	}{
		{"not found is not found", http.StatusNotFound, "application/json", ErrMediaNotFound},
		{"an upstream error is unavailability", http.StatusInternalServerError, "application/json", ErrMediaUnavailable},
		{"a refused credential is unavailability, not a verdict", http.StatusForbidden, "application/json", ErrMediaUnavailable},
		{"unauthorised likewise", http.StatusUnauthorized, "application/json", ErrMediaUnavailable},
		{"SVG is not passed on", http.StatusOK, "image/svg+xml", ErrNotAnImage},
		{"HTML is not passed on", http.StatusOK, "text/html; charset=utf-8", ErrNotAnImage},
		{"JSON is not passed on", http.StatusOK, "application/json", ErrNotAnImage},
		{"a body with no type is not passed on", http.StatusOK, "", ErrNotAnImage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
				// An empty Content-Type must reach the client as empty, not
				// as Go's sniffed guess.
				w.Header()["Content-Type"] = []string{tc.ctype}
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>x</script></svg>`))
			})
			body, _, err := New(s.URL, "k").FetchImageBytes(context.Background(), uuid.New())
			if body != nil {
				body.Close()
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestFetchImageBytesAcceptsTheRasterTypes(t *testing.T) {
	for _, ct := range []string{"image/jpeg", "image/png", "image/webp", "IMAGE/JPEG", "image/jpeg; q=1"} {
		s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", ct)
			_, _ = w.Write([]byte("x"))
		})
		body, got, err := New(s.URL, "k").FetchImageBytes(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("%s refused: %v", ct, err)
		}
		body.Close()
		if got != strings.ToLower(strings.Split(ct, ";")[0]) {
			t.Fatalf("%s came back as %q; the parameters and case must be normalised", ct, got)
		}
	}
}

func TestFetchImageBytesCapsTheSize(t *testing.T) {
	big := bytes.Repeat([]byte{0xAB}, MaxImageBytes+1)

	t.Run("a declared length over the cap is refused before reading", func(t *testing.T) {
		s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.Header().Set("Content-Length", strconv.Itoa(len(big)))
			_, _ = w.Write(big)
		})
		body, _, err := New(s.URL, "k").FetchImageBytes(context.Background(), uuid.New())
		if body != nil {
			body.Close()
		}
		if !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("err = %v, want ErrImageTooLarge", err)
		}
	})

	t.Run("an undeclared length over the cap fails the read, never truncates it", func(t *testing.T) {
		s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.(http.Flusher).Flush() // chunked: no Content-Length to refuse up front
			_, _ = w.Write(big)
		})
		body, _, err := New(s.URL, "k").FetchImageBytes(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("FetchImageBytes: %v", err)
		}
		defer body.Close()
		n, err := io.Copy(io.Discard, body)
		if !errors.Is(err, ErrImageTooLarge) {
			t.Fatalf("read %d bytes, err = %v; want ErrImageTooLarge — a clean EOF here is a truncated image", n, err)
		}
	})

	t.Run("exactly the cap is allowed", func(t *testing.T) {
		exact := big[:MaxImageBytes]
		s := imageServer(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "image/jpeg")
			w.(http.Flusher).Flush()
			_, _ = w.Write(exact)
		})
		body, _, err := New(s.URL, "k").FetchImageBytes(context.Background(), uuid.New())
		if err != nil {
			t.Fatalf("FetchImageBytes: %v", err)
		}
		defer body.Close()
		n, err := io.Copy(io.Discard, body)
		if err != nil || n != MaxImageBytes {
			t.Fatalf("read %d bytes, err = %v; want %d and no error", n, err, MaxImageBytes)
		}
	})
}

func TestFetchImageBytesWithoutAMediaServiceIsUnavailable(t *testing.T) {
	var c *Client
	if _, _, err := c.FetchImageBytes(context.Background(), uuid.New()); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("nil client: err = %v, want ErrMediaUnavailable", err)
	}
	s := httptest.NewServer(http.NotFoundHandler())
	url := s.URL
	s.Close()
	if _, _, err := New(url, "k").FetchImageBytes(context.Background(), uuid.New()); !errors.Is(err, ErrMediaUnavailable) {
		t.Fatalf("unreachable: err = %v, want ErrMediaUnavailable", err)
	}
	if _, _, err := New("http://x", "k").FetchImageBytes(context.Background(), uuid.Nil); !errors.Is(err, ErrMediaNotFound) {
		t.Fatalf("nil id: err = %v, want ErrMediaNotFound", err)
	}
}
