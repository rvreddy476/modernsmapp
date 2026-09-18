package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
)

// Open Graph posters for shared links (2026-09-18).
//
// The property under test is that the poster path is a ONE-WAY widening: an
// anonymous caller gains a still of a post that is public to the entire
// internet, and gains nothing else. Every case below fails on the code as it
// stood before public_poster.go — either because the poster was refused, or
// because the narrowing that keeps it to stills did not exist.

// fakePosterLookup records whether it was consulted, so a test can assert the
// difference between "refused by the poster authority" and "never reached it".
type fakePosterLookup struct {
	public bool
	err    error
	calls  int
}

func (f *fakePosterLookup) MediaIsOnPublicPost(context.Context, string) (bool, error) {
	f.calls++
	if f.err != nil {
		return false, f.err
	}
	return f.public, nil
}

const (
	anonViewer     = "00000000-0000-0000-0000-000000000000"
	signedInViewer = "11111111-1111-1111-1111-111111111111"
	posterKey      = "protected/media/m1/thumb_150.jpg"
	renditionKey   = "protected/media/m1/720p.mp4"
)

func posterGate(t *testing.T, authz ContentAuthorizer, lookup PublicPostLookup) *Gate {
	t.Helper()
	return NewGate(gateSigner(t), authz).WithPublicPoster(lookup)
}

func TestAnonymousReadsPublicPostPoster(t *testing.T) {
	lookup := &fakePosterLookup{public: true}
	gate := posterGate(t, fakeAuthz{allow: false}, lookup)

	url, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey)
	if err != nil {
		t.Fatalf("anonymous crawler was refused a public post's poster: %v", err)
	}
	if !strings.Contains(url, "Signature=") {
		t.Fatalf("poster was not delivered as a bounded signed URL: %s", url)
	}
	if lookup.calls != 1 {
		t.Fatalf("poster authority consulted %d times, want exactly 1", lookup.calls)
	}
}

// The empty string is the other spelling of "no viewer" — a caller that has
// not yet been converted to uuid.Nil must not read as an identified viewer.
func TestAnonymousReadsPublicPostPosterWithEmptyViewerID(t *testing.T) {
	lookup := &fakePosterLookup{public: true}
	gate := posterGate(t, fakeAuthz{allow: false}, lookup)

	if _, err := gate.URLForVariant(context.Background(), "", "m1", "thumb_150", posterKey); err != nil {
		t.Fatalf("empty viewer id was not treated as anonymous: %v", err)
	}
}

// Unlisted, followers-only, private, scheduled, soft-deleted and
// pending-moderation all reach the gate as the same answer — not public — and
// all must stay refused. Which of them is which is the SQL's job
// (MediaIsOnPublicPost, exercised in public_post_media_integration_test.go);
// the gate's job is to keep "not public" indistinguishable from "no such
// media".
func TestAnonymousRefusedPosterOfNonPublicPost(t *testing.T) {
	for _, name := range []string{"unlisted", "private", "followers", "scheduled", "deleted", "pending_moderation"} {
		t.Run(name, func(t *testing.T) {
			lookup := &fakePosterLookup{public: false}
			gate := posterGate(t, fakeAuthz{allow: false}, lookup)

			_, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey)
			if !errors.Is(err, ErrDeliveryDenied) {
				t.Fatalf("got %v, want ErrDeliveryDenied", err)
			}
			if lookup.calls != 1 {
				t.Fatalf("poster authority consulted %d times, want exactly 1", lookup.calls)
			}
		})
	}
}

// A crawler needs a still, not the film. Nothing that carries the work itself
// may be read anonymously, however public the post is — and the poster
// authority is not even asked, so a video fetch cannot be used to probe which
// posts are public.
func TestAnonymousRefusedNonStillVariantsOfPublicPost(t *testing.T) {
	for _, variant := range []string{"original", "360p", "480p", "720p", "1080p", "4k", "hls", "master", "preview", "audio_aac"} {
		t.Run(variant, func(t *testing.T) {
			lookup := &fakePosterLookup{public: true}
			gate := posterGate(t, fakeAuthz{allow: false}, lookup)

			_, err := gate.URLForVariant(context.Background(), anonViewer, "m1", variant, renditionKey)
			if !errors.Is(err, ErrDeliveryDenied) {
				t.Fatalf("anonymous caller reached %q of a public post: %v", variant, err)
			}
			if lookup.calls != 0 {
				t.Fatalf("poster authority was consulted for %q", variant)
			}
		})
	}
}

// The uploader of an asset that has not cleared moderation still previews it
// (post-service's canonical gate allows the uploader), and that decision is
// the ordinary one — the poster authority, which would say no, is never
// reached and so cannot narrow it.
func TestOwnerOfPendingAssetStillReadsItWithoutThePosterAuthority(t *testing.T) {
	lookup := &fakePosterLookup{public: false}
	gate := posterGate(t, fakeAuthz{allow: true}, lookup)

	if _, err := gate.URLForVariant(context.Background(), signedInViewer, "m1", "thumb_150", posterKey); err != nil {
		t.Fatalf("owner was refused their own pending asset: %v", err)
	}
	if lookup.calls != 0 {
		t.Fatal("poster authority was consulted for a signed-in viewer")
	}
}

// A signed-in viewer who is refused stays refused. The poster path is scoped
// to anonymity on purpose: a logged-in stranger's audience decision belongs to
// the content authority, and "public post" must not become a way around a
// block or a mute.
func TestSignedInDenialIsNotOverturnedByThePosterAuthority(t *testing.T) {
	lookup := &fakePosterLookup{public: true}
	gate := posterGate(t, fakeAuthz{allow: false}, lookup)

	_, err := gate.URLForVariant(context.Background(), signedInViewer, "m1", "thumb_150", posterKey)
	if !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want ErrDeliveryDenied", err)
	}
	if lookup.calls != 0 {
		t.Fatal("poster authority was consulted for a signed-in viewer")
	}
}

// A post-service outage is not an answer. The poster authority can still
// resolve the question positively — a public post is public whether or not the
// content authority is reachable — so an outage does not blank the previews of
// every shared link.
func TestPosterAuthorityResolvesAnUnreachableContentAuthority(t *testing.T) {
	lookup := &fakePosterLookup{public: true}
	gate := posterGate(t, fakeAuthz{err: ErrDeliveryUnresolved}, lookup)

	if _, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey); err != nil {
		t.Fatalf("public poster was not served during a content-authority outage: %v", err)
	}
}

// …but it must not CONVERT one. If the poster authority cannot say yes, the
// unresolved answer is returned unchanged, so the caller retries instead of
// caching a 404.
func TestUnresolvedStaysUnresolvedWhenThePostIsNotPublic(t *testing.T) {
	lookup := &fakePosterLookup{public: false}
	gate := posterGate(t, fakeAuthz{err: ErrDeliveryUnresolved}, lookup)

	_, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey)
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
}

// A failed lookup is an outage, not a denial. Answering 404 would teach every
// crawler and every cache that a public video has no picture.
func TestPosterLookupFailureIsUnresolvedNotDenied(t *testing.T) {
	lookup := &fakePosterLookup{err: fmt.Errorf("connection reset")}
	gate := posterGate(t, fakeAuthz{allow: false}, lookup)

	_, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey)
	if !errors.Is(err, ErrDeliveryUnresolved) {
		t.Fatalf("got %v, want ErrDeliveryUnresolved", err)
	}
}

// An unwired poster authority leaves the gate exactly as it was.
func TestGateWithoutPosterAuthorityIsUnchanged(t *testing.T) {
	gate := NewGate(gateSigner(t), fakeAuthz{allow: false})

	_, err := gate.URLForVariant(context.Background(), anonViewer, "m1", "thumb_150", posterKey)
	if !errors.Is(err, ErrDeliveryDenied) {
		t.Fatalf("got %v, want ErrDeliveryDenied", err)
	}
}

// The allowlist is the narrowing, so it is asserted by name rather than by
// behaviour: a variant added to the pipeline later is refused until somebody
// decides it is a still.
func TestAnonymousImageVariantAllowlist(t *testing.T) {
	for _, allowed := range []string{"thumb_150", "thumb_300", "small_480", "medium_1080", " THUMB_150 "} {
		if !AnonymousImageVariant(allowed) {
			t.Errorf("%q should be an anonymous still", allowed)
		}
	}
	for _, refused := range []string{"original", "avatar", "360p", "720p", "4k", "hls", "master", "preview", "audio_aac", "", "thumb"} {
		if AnonymousImageVariant(refused) {
			t.Errorf("%q must not be an anonymous still", refused)
		}
	}
}
