package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// MTube download: the decision, exercised against a real delivery.Gate and
// a real asset row with only the post-service answer faked.

type fakeDownloadAuthority struct {
	err   error
	asked []string
}

func (f *fakeDownloadAuthority) AllowDownload(_ context.Context, viewerID, mediaID string) error {
	f.asked = append(f.asked, viewerID+"/"+mediaID)
	return f.err
}

type noopSigner struct{}

func (noopSigner) PublicURL(key string) (string, error) { return "https://cdn/" + key, nil }
func (noopSigner) SignProtected(key string, _ time.Duration, _ time.Time) (string, error) {
	return "https://cdn/" + key + "?sig", nil
}

func downloadAsset(owner uuid.UUID) *postgres.MediaAsset {
	return &postgres.MediaAsset{
		ID: uuid.New(), UploaderID: owner, FileType: "video",
		StorageKey: "uploads/" + owner.String() + "/m/original",
		Variants: []postgres.MediaVariant{
			{Name: "thumb_150", ObjectKey: "k/thumb_150"},
			{Name: "360p", ObjectKey: "k/360p"},
			{Name: "480p", ObjectKey: "k/480p"},
			{Name: "720p", ObjectKey: "k/720p"},
		},
	}
}

func TestDownloadVerdictOwnerNeverAsksPostService(t *testing.T) {
	owner := uuid.New()
	authority := &fakeDownloadAuthority{err: delivery.ErrDeliveryDenied}
	gate := delivery.NewGate(noopSigner{}, nil).WithDownloadAuthorizer(authority)
	if err := downloadVerdict(context.Background(), gate, downloadAsset(owner), owner); err != nil {
		t.Fatalf("owner refused: %v", err)
	}
	if len(authority.asked) != 0 {
		t.Fatalf("post-service was asked about the owner's own upload: %v", authority.asked)
	}
}

func TestDownloadVerdictViewerFollowsPostService(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	media := downloadAsset(owner)
	cases := map[string]struct {
		answer error
		want   error
	}{
		"allowed":    {nil, nil},
		"denied":     {delivery.ErrDeliveryDenied, delivery.ErrDeliveryDenied},
		"unresolved": {delivery.ErrDeliveryUnresolved, delivery.ErrDeliveryUnresolved},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			authority := &fakeDownloadAuthority{err: tc.answer}
			gate := delivery.NewGate(noopSigner{}, nil).WithDownloadAuthorizer(authority)
			err := downloadVerdict(context.Background(), gate, media, viewer)
			if !errors.Is(err, tc.want) && !(tc.want == nil && err == nil) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
			if len(authority.asked) != 1 || authority.asked[0] != viewer.String()+"/"+media.ID.String() {
				t.Fatalf("asked %v, want exactly one question about this viewer and asset", authority.asked)
			}
		})
	}
}

func TestDownloadVerdictAnonymousIsNeverAnOwner(t *testing.T) {
	// An asset whose uploader is the nil id (a scrubbed/anonymised row)
	// must not make every anonymous caller its owner.
	media := downloadAsset(uuid.Nil)
	authority := &fakeDownloadAuthority{err: delivery.ErrDeliveryDenied}
	gate := delivery.NewGate(noopSigner{}, nil).WithDownloadAuthorizer(authority)
	if err := downloadVerdict(context.Background(), gate, media, uuid.Nil); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("anonymous viewer of a nil-uploader asset: got %v, want denied", err)
	}
}

func TestDownloadVerdictFailsClosedWithoutAGate(t *testing.T) {
	owner, viewer := uuid.New(), uuid.New()
	err := downloadVerdict(context.Background(), nil, downloadAsset(owner), viewer)
	if !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("no gate: got %v, want unresolved", err)
	}
	if err := downloadVerdict(context.Background(), nil, nil, owner); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("no asset: got %v, want denied", err)
	}
}

func TestDownloadVerdictRefusesScopedAssetsEvenToTheOwner(t *testing.T) {
	owner := uuid.New()
	gate := delivery.NewGate(noopSigner{}, nil).WithDownloadAuthorizer(&fakeDownloadAuthority{})
	anon := downloadAsset(owner)
	anon.AccessScope = postgres.AccessScopeAnonymous
	if err := downloadVerdict(context.Background(), gate, anon, owner); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("anonymous-scope asset: got %v, want denied", err)
	}
}

func TestPickDownloadObjectPrefers720Then480ThenOriginal(t *testing.T) {
	media := downloadAsset(uuid.New())
	if key, v := pickDownloadObject(media); key != "k/720p" || v != "720p" {
		t.Fatalf("got %s (%s), want 720p", key, v)
	}
	media.Variants = media.Variants[:3] // thumb, 360p, 480p
	if key, v := pickDownloadObject(media); key != "k/480p" || v != "480p" {
		t.Fatalf("got %s (%s), want 480p", key, v)
	}
	media.Variants = media.Variants[:2] // thumb, 360p — no preferred rung
	if key, v := pickDownloadObject(media); key != media.StorageKey || v != "original" {
		t.Fatalf("got %s (%s), want the original", key, v)
	}
}
