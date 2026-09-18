package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The subtitle read gate.
//
// Before this, GET /v1/subtitles/:mediaId and /status took no viewer and
// asked nothing: the transcript of any asset was readable from its UUID
// alone. These tests pin the three cases that matter — the owner of a
// pending asset reads it, a stranger does not, and a stranger reads a
// public one — against a real delivery.Gate, so the decision is the same
// object the byte path uses rather than a second copy of it.

// fakeAuthority stands in for post-service. It records whether it was
// asked, because "the owner never needs the authority" and "a locally
// denied read never reaches the authority" are part of the contract.
type fakeAuthority struct {
	verdict error
	asked   int
	viewers []string
}

func (f *fakeAuthority) Authorize(_ context.Context, viewerID, _ string) error {
	f.asked++
	f.viewers = append(f.viewers, viewerID)
	return f.verdict
}

func protectedAsset(owner uuid.UUID, moderation string) *postgres.MediaAsset {
	return &postgres.MediaAsset{
		ID:               uuid.New(),
		UploaderID:       owner,
		FileType:         "video",
		StorageKey:       "protected/user/" + owner.String() + "/clip.mp4",
		ProcessingStatus: "ready",
		ModerationStatus: moderation,
	}
}

func TestAuthorizeCaptionRead(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()

	t.Run("owner reads their own pending asset", func(t *testing.T) {
		authority := &fakeAuthority{verdict: delivery.ErrDeliveryDenied}
		gate := delivery.NewGate(nil, authority)
		media := protectedAsset(owner, "pending")

		if err := authorizeCaptionRead(context.Background(), gate, media, owner); err != nil {
			t.Fatalf("the uploader must read their own captions while moderation is pending: %v", err)
		}
		if authority.asked != 0 {
			t.Fatalf("the owner's own asset is not an audience question: authority asked %d times", authority.asked)
		}
	})

	t.Run("stranger is refused a pending asset without asking anyone", func(t *testing.T) {
		authority := &fakeAuthority{verdict: nil} // would allow
		gate := delivery.NewGate(nil, authority)
		media := protectedAsset(owner, "pending")

		err := authorizeCaptionRead(context.Background(), gate, media, stranger)
		if !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Fatalf("an unreleased asset's transcript must not reach a stranger: %v", err)
		}
		if authority.asked != 0 {
			t.Fatalf("moderation is media-service's own fact: authority asked %d times", authority.asked)
		}
	})

	t.Run("stranger is refused a private or unlisted asset", func(t *testing.T) {
		// Released by moderation, but the content authority — which owns
		// the audience — says no.
		authority := &fakeAuthority{verdict: delivery.ErrDeliveryDenied}
		gate := delivery.NewGate(nil, authority)
		media := protectedAsset(owner, "passed")

		err := authorizeCaptionRead(context.Background(), gate, media, stranger)
		if !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Fatalf("a private asset's transcript must not reach a stranger: %v", err)
		}
		if authority.asked != 1 {
			t.Fatalf("the audience question belongs to the content authority: asked %d times", authority.asked)
		}
		if authority.viewers[0] != stranger.String() {
			t.Fatalf("the authority must be asked about the real viewer, got %q", authority.viewers[0])
		}
	})

	t.Run("stranger reads a public asset", func(t *testing.T) {
		authority := &fakeAuthority{verdict: nil}
		gate := delivery.NewGate(nil, authority)
		media := protectedAsset(owner, "passed")

		if err := authorizeCaptionRead(context.Background(), gate, media, stranger); err != nil {
			t.Fatalf("captions of a public asset must be readable: %v", err)
		}
	})

	t.Run("anonymous viewer reads a public-class asset with no authority call", func(t *testing.T) {
		authority := &fakeAuthority{verdict: delivery.ErrDeliveryDenied}
		gate := delivery.NewGate(nil, authority)
		media := protectedAsset(owner, "passed")
		media.StorageKey = "public/avatars/" + owner.String() + ".mp4"

		if err := authorizeCaptionRead(context.Background(), gate, media, uuid.Nil); err != nil {
			t.Fatalf("a public-class object needs no audience decision: %v", err)
		}
		if authority.asked != 0 {
			t.Fatalf("public class must not pay for an authority round trip: asked %d times", authority.asked)
		}
	})

	t.Run("an unreachable authority is unresolved, never a served transcript", func(t *testing.T) {
		authority := &fakeAuthority{verdict: delivery.ErrDeliveryUnresolved}
		gate := delivery.NewGate(nil, authority)

		err := authorizeCaptionRead(context.Background(), gate, protectedAsset(owner, "passed"), stranger)
		if !errors.Is(err, delivery.ErrDeliveryUnresolved) {
			t.Fatalf("want unresolved (retryable 503), got %v", err)
		}
	})

	t.Run("no gate configured denies rather than falling through", func(t *testing.T) {
		err := authorizeCaptionRead(context.Background(), nil, protectedAsset(owner, "passed"), stranger)
		if !errors.Is(err, delivery.ErrDeliveryUnresolved) {
			t.Fatalf("an unwired gate must not permit a read: %v", err)
		}
	})
}

func TestCaptionReadLocalVerdict(t *testing.T) {
	owner := uuid.New()
	stranger := uuid.New()

	dating := protectedAsset(owner, "passed")
	dating.AccessScope = postgres.AccessScopeDatingPhoto

	for _, tc := range []struct {
		name   string
		media  *postgres.MediaAsset
		viewer uuid.UUID
		want   captionReadVerdict
	}{
		{"missing asset", nil, owner, captionReadDenied},
		{"owner of a pending asset", protectedAsset(owner, "pending"), owner, captionReadOwner},
		{"owner of a rejected asset", protectedAsset(owner, "rejected"), owner, captionReadOwner},
		{"stranger, pending", protectedAsset(owner, "pending"), stranger, captionReadDenied},
		{"stranger, rejected", protectedAsset(owner, "rejected"), stranger, captionReadDenied},
		{"stranger, failed", protectedAsset(owner, "failed"), stranger, captionReadDenied},
		{"stranger, empty moderation", protectedAsset(owner, ""), stranger, captionReadDenied},
		{"anonymous, pending", protectedAsset(owner, "pending"), uuid.Nil, captionReadDenied},
		{"stranger, passed", protectedAsset(owner, "passed"), stranger, captionReadAskAuthority},
		{"stranger, voice approved", protectedAsset(owner, "approved"), stranger, captionReadAskAuthority},
		{"anonymous, passed", protectedAsset(owner, "passed"), uuid.Nil, captionReadAskAuthority},
		{"dating photo, stranger", dating, stranger, captionReadDenied},
		{"dating photo, anonymous", dating, uuid.Nil, captionReadDenied},
		{"dating photo, owner", dating, owner, captionReadOwner},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := captionReadLocalVerdict(tc.media, tc.viewer); got != tc.want {
				t.Fatalf("captionReadLocalVerdict = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestSubtitleTrackPathIsThisServicesOwnRoute(t *testing.T) {
	id := uuid.MustParse("11111111-2222-3333-4444-555555555555")
	want := "/v1/subtitles/11111111-2222-3333-4444-555555555555/track/en-IN.vtt"
	if got := SubtitleTrackPath(id, "en-IN"); got != want {
		t.Fatalf("SubtitleTrackPath = %q, want %q", got, want)
	}
}
