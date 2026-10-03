package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Pulse dating clips — the store-free decisions. The routes themselves are
// driven end to end in internal/http/dating_clip_handler_test.go.

func datingClipAsset(owner uuid.UUID) *postgres.MediaAsset {
	a := protectedAsset(owner, "passed")
	a.AccessScope = postgres.AccessScopeDatingClip
	return a
}

// GetMediaURL, GetHLSPlaylist (master and child playlists — the segment
// URLs exist only inside a playlist this refuses), GetMediaVariantURL
// (ServeMedia / ServeMediaVariant), BatchMediaURLs, the record reads, HEAD
// on the serve routes, captions and downloads all ask DatingScopeDenies.
// Dropping 'dating_clip' from postgres.IsDatingScope fails this.
func TestDatingScopeDenies_DatingClip(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	clip := datingClipAsset(owner)
	if !DatingScopeDenies(clip, stranger) {
		t.Fatal("a stranger was allowed a dating clip through a public read route")
	}
	if !DatingScopeDenies(clip, uuid.Nil) {
		t.Fatal("a signed-out viewer was allowed a dating clip")
	}
	if DatingScopeDenies(clip, owner) {
		t.Fatal("the uploader was refused their own dating clip")
	}
}

// The record read (GET /v1/media/:id, /status) settles a dating clip before
// the delivery authority, which would admit anyone here.
func TestAuthorizeMediaRecord_DatingClipIsTheUploadersAlone(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	gate := delivery.NewGate(noopSigner{}, nil)
	clip := datingClipAsset(owner)
	for name, viewer := range map[string]uuid.UUID{"stranger": stranger, "signed-out": uuid.Nil} {
		if err := authorizeMediaRecord(context.Background(), gate, clip, viewer); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Fatalf("%s: got %v, want denied", name, err)
		}
	}
	if err := authorizeMediaRecord(context.Background(), gate, clip, owner); err != nil {
		t.Fatalf("owner: %v", err)
	}
}

// Captions and the alternate audio tracks (both AuthorizeMediaRead).
func TestCaptionReadLocalVerdict_DatingClip(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	clip := datingClipAsset(owner)
	if got := captionReadLocalVerdict(clip, stranger); got != captionReadDenied {
		t.Fatalf("stranger: %d, want denied", got)
	}
	if got := captionReadLocalVerdict(clip, uuid.Nil); got != captionReadDenied {
		t.Fatalf("signed-out: %d, want denied", got)
	}
	if got := captionReadLocalVerdict(clip, owner); got != captionReadOwner {
		t.Fatalf("owner: %d, want owner", got)
	}
}

// A dating clip is never downloadable by anyone but its owner.
func TestDownloadVerdict_DatingClipRefusedToOthers(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	clip := downloadAsset(owner)
	clip.AccessScope = postgres.AccessScopeDatingClip
	if err := downloadVerdict(clip, stranger); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("stranger: got %v, want denied", err)
	}
	if err := downloadVerdict(clip, uuid.Nil); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("signed-out: got %v, want denied", err)
	}
	if err := downloadVerdict(clip, owner); err != nil {
		t.Fatalf("owner: got %v, want allowed", err)
	}
}

// A sound names its creator; a dating clip's video has none.
func TestSoundSourceRefusal_DatingClip(t *testing.T) {
	ms := 12_000
	clip := datingClipAsset(uuid.New())
	clip.DurationMs = &ms
	if err := soundSourceRefusal(clip); !errors.Is(err, ErrAssetNotFound) {
		t.Fatalf("got %v, want ErrAssetNotFound", err)
	}
}

func TestNormaliseClipModeration(t *testing.T) {
	for in, want := range map[string]string{
		"passed":        ClipModerationPassed,
		"approved":      ClipModerationPassed,
		"manual_review": ClipModerationReview,
		"failed":        ClipModerationReview,
		"rejected":      ClipModerationRejected,
		"pending":       ClipModerationPending,
		"":              ClipModerationPending,
		"something-new": ClipModerationPending,
	} {
		if got := NormaliseClipModeration(in); got != want {
			t.Errorf("NormaliseClipModeration(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestResolveDatingClipSettings(t *testing.T) {
	env := func(kv ...string) func(string) string {
		m := map[string]string{}
		for i := 0; i+1 < len(kv); i += 2 {
			m[kv[i]] = kv[i+1]
		}
		return func(k string) string { return m[k] }
	}
	s, err := ResolveDatingClipSettings(env())
	if err != nil || s.Enabled || s.URLTTL != DefaultDatingClipURLTTL || s.MaxMs != DefaultDatingClipMaxMs {
		t.Fatalf("unset = %+v, %v; want off with the defaults", s, err)
	}
	// Off: nothing else is read, so a bad value cannot stop the boot.
	if s, err := ResolveDatingClipSettings(env(EnvDatingClipsEnabled, "false", EnvDatingClipMaxMs, "nope")); err != nil || s.Enabled {
		t.Fatalf("off with a bad value = %+v, %v", s, err)
	}
	s, err = ResolveDatingClipSettings(env(EnvDatingClipsEnabled, "true", EnvDatingClipURLTTLSeconds, "90", EnvDatingClipMaxMs, "15000"))
	if err != nil || !s.Enabled || s.URLTTL != 90*time.Second || s.MaxMs != 15000 {
		t.Fatalf("on = %+v, %v", s, err)
	}
	for _, bad := range [][]string{
		{EnvDatingClipsEnabled, "yes please"},
		{EnvDatingClipsEnabled, "true", EnvDatingClipURLTTLSeconds, "301"},
		{EnvDatingClipsEnabled, "true", EnvDatingClipURLTTLSeconds, "29"},
		{EnvDatingClipsEnabled, "true", EnvDatingClipMaxMs, "999"},
		{EnvDatingClipsEnabled, "true", EnvDatingClipMaxMs, "180001"},
		{EnvDatingClipsEnabled, "true", EnvDatingClipMaxMs, "30s"},
	} {
		if _, err := ResolveDatingClipSettings(env(bad...)); err == nil || !strings.Contains(err.Error(), "MEDIA_DATING_CLIP") {
			t.Fatalf("%v accepted: %v", bad, err)
		}
	}
}
