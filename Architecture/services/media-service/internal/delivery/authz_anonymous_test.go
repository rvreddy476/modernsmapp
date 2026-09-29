package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Signed-out reads of post media (2026-09-29, founder decision 2).
//
// The property under test: an anonymous viewer is a QUESTION for the post
// authority, sent as the documented sentinel viewer_id "", and post-service's
// answer is final in both directions — a yes signs the bytes (stills, video
// renditions and HLS alike), a no stays a no, an outage stays retryable. The
// chat and group authorities still refuse anonymity without a call, and the
// profile and commerce authorities still receive the viewer string untouched.

// recordingAuthority answers with `body` and records every viewer_id it saw.
type recordingAuthority struct {
	srv     *httptest.Server
	viewers []string
	batch   []string
}

func newRecordingAuthority(t *testing.T, status int, body string) *recordingAuthority {
	t.Helper()
	r := &recordingAuthority{}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var payload struct {
			ViewerID *string `json:"viewer_id"`
		}
		_ = json.NewDecoder(req.Body).Decode(&payload)
		seen := "<absent>"
		if payload.ViewerID != nil {
			seen = *payload.ViewerID
		}
		if strings.HasSuffix(req.URL.Path, "/batch") {
			r.batch = append(r.batch, seen)
		} else {
			r.viewers = append(r.viewers, seen)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(r.srv.Close)
	return r
}

func TestPostAuthorizerSendsAnonymousViewerAsTheEmptySentinel(t *testing.T) {
	for _, viewer := range []string{"", nilViewerID, "  " + nilViewerID + " "} {
		t.Run("viewer "+strings.TrimSpace(viewer), func(t *testing.T) {
			authority := newRecordingAuthority(t, 200, `{"allowed":true,"reason":"post_allowed"}`)
			a := NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client())
			if err := a.Authorize(context.Background(), viewer, "m1"); err != nil {
				t.Fatalf("anonymous public read refused: %v", err)
			}
			if len(authority.viewers) != 1 || authority.viewers[0] != "" {
				t.Fatalf("post-service saw viewer_id %q, want the empty sentinel", authority.viewers)
			}
		})
	}
}

func TestPostAuthorizerBatchSendsAnonymousViewerAsTheEmptySentinel(t *testing.T) {
	authority := newRecordingAuthority(t, 200, `{"allowed":{"m1":true,"m2":false},"reasons":{"m2":"no_public_post"}}`)
	a := NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client())
	got, err := a.AuthorizeBatch(context.Background(), nilViewerID, []string{"m1", "m2"})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if !got["m1"] || got["m2"] {
		t.Fatalf("batch verdicts %v", got)
	}
	if len(authority.batch) != 1 || authority.batch[0] != "" {
		t.Fatalf("post-service saw batch viewer_id %q, want the empty sentinel", authority.batch)
	}
}

// A signed-in viewer is sent exactly as it is — the sentinel is only for
// anonymity.
func TestPostAuthorizerSendsASignedInViewerUnchanged(t *testing.T) {
	authority := newRecordingAuthority(t, 200, `{"allowed":true}`)
	a := NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client())
	if err := a.Authorize(context.Background(), "11111111-1111-1111-1111-111111111111", "m1"); err != nil {
		t.Fatal(err)
	}
	if authority.viewers[0] != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("viewer rewritten to %q", authority.viewers[0])
	}
}

// post-service's answer is final: a resolved no (403 / allowed:false) is a
// denial, and an outage stays unresolved so the caller retries a 503
// rather than caching a 404 — there is no longer a local poster SQL to
// overturn either.
func TestAnonymousDenialAndOutageAreNotOverturned(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   error
	}{
		"resolved no (403)":        {403, `{"allowed":false,"reason":"no_public_post"}`, ErrDeliveryDenied},
		"resolved no (200, false)": {200, `{"allowed":false,"reason":"no_public_post"}`, ErrDeliveryDenied},
		"outage (503)":             {503, `{"allowed":false,"reason":"policy_unresolved"}`, ErrDeliveryUnresolved},
		"outage (500)":             {500, ``, ErrDeliveryUnresolved},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			authority := newRecordingAuthority(t, tc.status, tc.body)
			gate := NewGate(gateSigner(t), NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client()))
			for _, key := range []string{ProtectedPrefix + "media/m1/thumb_150.jpg", ProtectedPrefix + "media/m1/720p.mp4", ProtectedPrefix + "media/m1/hls/master.m3u8"} {
				_, err := gate.URLForVariant(context.Background(), nilViewerID, "m1", "thumb_150", key)
				if !errors.Is(err, tc.want) {
					t.Fatalf("%s: got %v, want %v", key, err, tc.want)
				}
			}
		})
	}
}

// A public post's media — the poster AND the film — is signed for a
// signed-out viewer once post-service has said yes. Before this the gate
// admitted anonymous stills only, through a local SQL, and refused every
// rendition and playlist.
func TestAnonymousPlaybackOfAPublicPostIsSigned(t *testing.T) {
	authority := newRecordingAuthority(t, 200, `{"allowed":true,"reason":"post_allowed"}`)
	gate := NewGate(gateSigner(t), NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client()))
	keys := map[string]string{
		"thumb_150": ProtectedPrefix + "media/m1/thumb_150.jpg",
		"720p":      ProtectedPrefix + "media/m1/720p.mp4",
		"hls":       ProtectedPrefix + "media/m1/hls/master.m3u8",
	}
	urls, err := gate.URLsForAsset(context.Background(), nilViewerID, "m1", keys)
	if err != nil {
		t.Fatalf("anonymous playback refused: %v", err)
	}
	for name, u := range urls {
		if !strings.Contains(u, "Signature=") {
			t.Fatalf("%s was not a bounded signed URL: %s", name, u)
		}
	}
	if len(authority.viewers) != 1 {
		t.Fatalf("post-service asked %d times, want exactly 1", len(authority.viewers))
	}
	if _, err := gate.URLForVariant(context.Background(), "", "m1", "720p", keys["720p"]); err != nil {
		t.Fatalf("empty-string viewer refused: %v", err)
	}
}

// The chat and group authorities keep refusing anonymity locally, without a
// call, for both spellings of "no viewer".
func TestChatAndGroupAuthoritiesStillRefuseAnonymityWithoutACall(t *testing.T) {
	for name, build := range map[string]func(string) *HTTPContentAuthorizer{
		"chat":  func(u string) *HTTPContentAuthorizer { return NewHTTPChatAuthorizer(u, "k", nil) },
		"group": func(u string) *HTTPContentAuthorizer { return NewHTTPGroupAuthorizer(u, "k", nil) },
	} {
		for _, viewer := range []string{"", nilViewerID} {
			t.Run(name+" "+viewer, func(t *testing.T) {
				authority := newRecordingAuthority(t, 200, `{"allowed":true}`)
				err := build(authority.srv.URL).Authorize(context.Background(), viewer, "m1")
				if !errors.Is(err, ErrDeliveryDenied) {
					t.Fatalf("got %v, want ErrDeliveryDenied", err)
				}
				if len(authority.viewers) != 0 {
					t.Fatalf("%s authority was asked about an anonymous viewer", name)
				}
			})
		}
	}
}

// The profile and commerce authorities receive the viewer string untouched:
// they resolve a nil viewer themselves and must not start seeing "" for a
// nil UUID (or a nil UUID for "").
func TestProfileAndCommerceAuthoritiesForwardTheViewerUntouched(t *testing.T) {
	for name, build := range map[string]func(string, *http.Client) *HTTPContentAuthorizer{
		"profile":  func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPProfileAuthorizer(u, "k", c) },
		"commerce": func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPCommerceAuthorizer(u, "k", c) },
	} {
		for _, viewer := range []string{"", nilViewerID} {
			t.Run(name+" "+viewer, func(t *testing.T) {
				authority := newRecordingAuthority(t, 200, `{"data":{"allowed":true}}`)
				if err := build(authority.srv.URL, authority.srv.Client()).Authorize(context.Background(), viewer, "m1"); err != nil {
					t.Fatalf("%s refused: %v", name, err)
				}
				if len(authority.viewers) != 1 || authority.viewers[0] != viewer {
					t.Fatalf("%s saw %q, want %q", name, authority.viewers, viewer)
				}
			})
		}
	}
}

func TestAnonymousViewerSpellings(t *testing.T) {
	for _, anon := range []string{"", " ", nilViewerID, " " + nilViewerID} {
		if !AnonymousViewer(anon) {
			t.Errorf("%q should be anonymous", anon)
		}
	}
	for _, named := range []string{"viewer", "11111111-1111-1111-1111-111111111111"} {
		if AnonymousViewer(named) {
			t.Errorf("%q must not be anonymous", named)
		}
	}
}
