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

// The file-download route's decision (owner only since 2026-10-02): the
// uploader, and nobody else, whatever the post says. The route end to end
// over a real store is download_integration_test.go.

// fakeDownloadAuthority is post-service's old "does the post allow
// download" answer. It is wired to the gate in these tests exactly as
// cmd/server wires it, so a test can prove it is never asked.
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

// attachmentSigner is noopSigner that can also mark a URL as an attachment
// (delivery.DownloadSigner), the way both production signers can.
type attachmentSigner struct{ noopSigner }

func (attachmentSigner) SignProtectedDownload(key string, _ time.Duration, _ time.Time, filename string) (string, error) {
	return "https://cdn/" + key + "?sig&response-content-disposition=attachment%3B+filename%3D%22" + filename + "%22", nil
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

func TestDownloadVerdictIsTheUploaderAndNobodyElse(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	media := downloadAsset(owner)
	if err := downloadVerdict(media, owner); err != nil {
		t.Fatalf("the uploader was refused their own upload: %v", err)
	}
	for name, viewer := range map[string]uuid.UUID{"a stranger": stranger, "anonymous": uuid.Nil} {
		if err := downloadVerdict(media, viewer); !errors.Is(err, delivery.ErrDeliveryDenied) {
			t.Fatalf("%s: got %v, want denied", name, err)
		}
	}
	if err := downloadVerdict(nil, owner); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("no asset: got %v, want denied", err)
	}
}

func TestDownloadVerdictAnonymousIsNeverAnOwner(t *testing.T) {
	// An asset whose uploader is the nil id (a scrubbed/anonymised row)
	// must not make every anonymous caller its owner.
	media := downloadAsset(uuid.Nil)
	if err := downloadVerdict(media, uuid.Nil); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("anonymous viewer of a nil-uploader asset: got %v, want denied", err)
	}
}

func TestDownloadVerdictRefusesScopedAssetsEvenToTheOwner(t *testing.T) {
	owner := uuid.New()
	anon := downloadAsset(owner)
	anon.AccessScope = postgres.AccessScopeAnonymous
	if err := downloadVerdict(anon, owner); !errors.Is(err, delivery.ErrDeliveryDenied) {
		t.Fatalf("anonymous-scope asset: got %v, want denied", err)
	}
	if !downloadScopeDenies(anon, uuid.Nil) {
		t.Fatal("an anonymous-scope asset is a download for a service caller")
	}
	dating := downloadAsset(owner)
	dating.AccessScope = postgres.AccessScopeDatingPhoto
	if !downloadScopeDenies(dating, uuid.Nil) {
		t.Fatal("a dating photo is a download for a service caller")
	}
	if downloadScopeDenies(downloadAsset(owner), uuid.Nil) {
		t.Fatal("an ordinary video is refused to a service caller by scope")
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

// signDownload is the only place the route's URL is made: it is an
// attachment, and a signer that cannot mark one is an outage, never a plain
// playback URL handed out as a download.
func TestSignDownloadIsAnAttachmentOrNothing(t *testing.T) {
	media := downloadAsset(uuid.New())
	s := &Service{gate: delivery.NewGate(attachmentSigner{}, nil)}
	url, err := s.signDownload(media)
	if err != nil || url != "https://cdn/k/720p?sig&response-content-disposition=attachment%3B+filename%3D%22"+media.ID.String()+".mp4%22" {
		t.Fatalf("url=%q err=%v", url, err)
	}
	s = &Service{gate: delivery.NewGate(noopSigner{}, nil)}
	if _, err := s.signDownload(media); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("a signer without attachment support: %v, want unresolved", err)
	}
	if _, err := (&Service{}).signDownload(media); !errors.Is(err, delivery.ErrDeliveryUnresolved) {
		t.Fatalf("no gate: %v, want unresolved", err)
	}
}
