package delivery

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The poster question (2026-10-02).
//
// post-service's rule for a members-only post differs by what is asked for:
// a signed-in viewer who is not a member sees the poster (the join card) and
// is refused the video. Only a read that named a thumbnail still can say it
// is a poster read, so only that read sends `purpose: "poster"`, and only to
// post-service. Everything else — renditions, the HLS graph, the original,
// the storyboard, dubbed audio — is asked the ordinary playback question.

// purposeAuthority is a fake post-service that answers the way the real one
// does for a members-only video and a signed-in non-member: yes to a poster
// read, no (403, members_only) to playback. It records every purpose sent.
type purposeAuthority struct {
	srv      *httptest.Server
	purposes []string
	raw      []map[string]json.RawMessage
}

func newPurposeAuthority(t *testing.T) *purposeAuthority {
	t.Helper()
	a := &purposeAuthority{}
	a.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		var raw map[string]json.RawMessage
		_ = json.NewDecoder(req.Body).Decode(&raw)
		a.raw = append(a.raw, raw)
		var purpose string
		if p, ok := raw["purpose"]; ok {
			_ = json.Unmarshal(p, &purpose)
		}
		a.purposes = append(a.purposes, purpose)
		w.Header().Set("Content-Type", "application/json")
		if purpose == PurposePoster {
			_, _ = w.Write([]byte(`{"allowed":true,"decision":"allowed","reason":"post_allowed"}`))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"allowed":false,"decision":"denied","reason":"members_only"}`))
	}))
	t.Cleanup(a.srv.Close)
	return a
}

const posterViewer = "11111111-1111-1111-1111-111111111111"

func TestIsPosterVariant(t *testing.T) {
	for _, v := range []string{"thumb_150", "thumb_300"} {
		if !IsPosterVariant(v) {
			t.Errorf("%q is a thumbnail still", v)
		}
	}
	for _, v := range []string{"", "original", "avatar", "720p", "480p", "360p", "hls", "storyboard_jpg", "storyboard_vtt",
		"dub_hi_720p", "preview_gif", "thumb", "Thumb_300", "poster"} {
		if IsPosterVariant(v) {
			t.Errorf("%q must be asked the playback question", v)
		}
	}
}

// A thumbnail still of a members-only video is signed for a signed-in
// non-member; every other variant of the same asset is refused.
func TestThumbnailReadAsksThePosterQuestionAndNothingElseDoes(t *testing.T) {
	authority := newPurposeAuthority(t)
	gate := NewGate(gateSigner(t), AnyContentAuthorizer{
		NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client()),
	})
	ctx := context.Background()

	for _, variant := range []string{"thumb_150", "thumb_300"} {
		url, err := gate.URLForVariant(ctx, posterViewer, "m1", variant, ProtectedPrefix+"media/m1/"+variant+".jpg")
		if err != nil || url == "" {
			t.Fatalf("%s of a members-only video was hidden from a signed-in non-member: %v", variant, err)
		}
	}
	if len(authority.purposes) != 2 || authority.purposes[0] != "poster" || authority.purposes[1] != "poster" {
		t.Fatalf("purposes sent for thumbnails = %q, want poster twice", authority.purposes)
	}

	authority.purposes, authority.raw = nil, nil
	for _, variant := range []string{"720p", "480p", "360p", "storyboard_jpg", "storyboard_vtt", "dub_hi_720p", "preview_gif"} {
		if _, err := gate.URLForVariant(ctx, posterViewer, "m1", variant, ProtectedPrefix+"media/m1/"+variant); !errors.Is(err, ErrDeliveryDenied) {
			t.Fatalf("%s of a members-only video: err=%v, want a denial", variant, err)
		}
	}
	// The reads that name no variant at all: the original, the HLS graph,
	// the whole record, a caption track, a page of cards.
	if _, err := gate.URLFor(ctx, posterViewer, "m1", ProtectedPrefix+"media/m1/original.mp4"); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("URLFor: %v", err)
	}
	keys := map[string]string{"thumb_300": ProtectedPrefix + "media/m1/thumb_300.jpg", "720p": ProtectedPrefix + "media/m1/720p.mp4"}
	if _, err := gate.URLsForAsset(ctx, posterViewer, "m1", keys); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("URLsForAsset: %v", err)
	}
	if err := gate.AuthorizeAsset(ctx, posterViewer, "m1", keys); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("AuthorizeAsset: %v", err)
	}
	for i, purpose := range authority.purposes {
		if purpose != "" {
			t.Fatalf("playback read %d sent purpose %q", i, purpose)
		}
		if _, sent := authority.raw[i]["purpose"]; sent {
			t.Fatalf("playback read %d sent a purpose key at all: %v", i, authority.raw[i])
		}
	}
	if len(authority.purposes) != 10 {
		t.Fatalf("authority was asked %d times, want 10", len(authority.purposes))
	}
}

// post-service's answer to the poster question is final in both directions:
// a no stays a no (a signed-out viewer of a members-only post), an outage
// stays retryable.
func TestPosterAnswerIsPostServicesAndIsFinal(t *testing.T) {
	cases := map[string]struct {
		status int
		body   string
		want   error
	}{
		"resolved no": {403, `{"allowed":false,"reason":"no_public_post"}`, ErrDeliveryDenied},
		"outage":      {503, `{"allowed":false,"reason":"policy_unresolved"}`, ErrDeliveryUnresolved},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			authority := newRecordingAuthority(t, tc.status, tc.body)
			gate := NewGate(gateSigner(t), AnyContentAuthorizer{NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client())})
			_, err := gate.URLForVariant(context.Background(), nilViewerID, "m1", "thumb_300", ProtectedPrefix+"media/m1/thumb_300.jpg")
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if len(authority.viewers) != 1 || authority.viewers[0] != "" {
				t.Fatalf("anonymous poster read sent viewer %q, want the empty sentinel", authority.viewers)
			}
		})
	}
}

// Only post-service is told about the purpose. The other authorities have no
// rule that differs for a still, and are asked exactly what they always were.
func TestPosterPurposeIsSentToPostServiceOnly(t *testing.T) {
	ctx := context.Background()
	for name, build := range map[string]func(url string, c *http.Client) *HTTPContentAuthorizer{
		"chat":     func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPChatAuthorizer(u, "k", c) },
		"group":    func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPGroupAuthorizer(u, "k", c) },
		"profile":  func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPProfileAuthorizer(u, "k", c) },
		"commerce": func(u string, c *http.Client) *HTTPContentAuthorizer { return NewHTTPCommerceAuthorizer(u, "k", c) },
	} {
		t.Run(name, func(t *testing.T) {
			authority := newPurposeAuthority(t)
			a := build(authority.srv.URL, authority.srv.Client())
			_ = a.AuthorizePoster(ctx, posterViewer, "m1")
			if len(authority.raw) != 1 {
				t.Fatalf("asked %d times", len(authority.raw))
			}
			if _, sent := authority.raw[0]["purpose"]; sent {
				t.Fatalf("%s was sent a purpose: %v", name, authority.raw[0])
			}
		})
	}
	authority := newPurposeAuthority(t)
	if err := NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client()).AuthorizePoster(ctx, posterViewer, "m1"); err != nil {
		t.Fatalf("post-service poster read: %v", err)
	}
	if authority.purposes[0] != "poster" {
		t.Fatalf("post-service was sent purpose %q", authority.purposes[0])
	}
}

// strictAuthorizer cannot ask the poster question.
type strictAuthorizer struct{ asked int }

func (s *strictAuthorizer) Authorize(context.Context, string, string) error {
	s.asked++
	return ErrDeliveryDenied
}

// An authorizer that cannot ask the poster question is asked the ordinary,
// stricter one; a public-class key is still signed with no question at all.
func TestPosterReadFallsBackToTheOrdinaryDecision(t *testing.T) {
	strict := &strictAuthorizer{}
	gate := NewGate(gateSigner(t), strict)
	if _, err := gate.URLForVariant(context.Background(), posterViewer, "m1", "thumb_300", ProtectedPrefix+"media/m1/thumb_300.jpg"); !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want the ordinary denial", err)
	}
	if strict.asked != 1 {
		t.Fatalf("ordinary authorizer asked %d times", strict.asked)
	}
	if url, err := gate.URLForVariant(context.Background(), posterViewer, "m1", "thumb_300", PublicPrefix+"avatars/a.jpg"); err != nil || url == "" {
		t.Fatalf("public key: %v", err)
	}
	if strict.asked != 1 {
		t.Fatal("a public-class key was authorized")
	}
	// The same with an authorizer that CAN ask the poster question: a
	// public-class key is nobody's to authorize.
	authority := newPurposeAuthority(t)
	posterGate := NewGate(gateSigner(t), AnyContentAuthorizer{NewHTTPContentAuthorizer(authority.srv.URL, "k", authority.srv.Client())})
	if url, err := posterGate.URLForVariant(context.Background(), posterViewer, "m1", "thumb_300", PublicPrefix+"avatars/a.jpg"); err != nil || url == "" {
		t.Fatalf("public key through a poster-capable gate: %v", err)
	}
	if len(authority.raw) != 0 {
		t.Fatalf("a public-class key was sent to the authority %d times", len(authority.raw))
	}
	// No authorizer at all: unresolved, never signed.
	if _, err := NewGate(gateSigner(t), nil).URLForVariant(context.Background(), posterViewer, "m1", "thumb_300", ProtectedPrefix+"x"); !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("nil authorizer: %v", err)
	}
}
