package service

import (
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Lane D6 — the public read routes (GetMedia, GetMediaURL, GetMediaVariantURL,
// ServeMedia/ServeMediaVariant, BatchMediaURLs, GetHLSPlaylist) refuse a
// dating-scoped asset to everyone but its uploader. This is that decision.
func TestDatingScopeDenies(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	dating := &postgres.MediaAsset{UploaderID: owner, AccessScope: postgres.AccessScopeDatingPhoto}
	plain := &postgres.MediaAsset{UploaderID: owner}

	if !DatingScopeDenies(dating, stranger) {
		t.Fatal("a stranger was allowed a dating photo through a public read route")
	}
	if !DatingScopeDenies(dating, uuid.Nil) {
		t.Fatal("an anonymous viewer was allowed a dating photo")
	}
	if DatingScopeDenies(dating, owner) {
		t.Fatal("the uploader was refused their own dating photo")
	}
	if DatingScopeDenies(plain, stranger) || DatingScopeDenies(nil, stranger) {
		t.Fatal("a non-dating asset was refused by the dating rule (the delivery gate decides those)")
	}
}

func TestResolveDatingPhotoURLTTL(t *testing.T) {
	env := func(v string) func(string) string {
		return func(k string) string {
			if k == EnvDatingPhotoURLTTLSeconds {
				return v
			}
			return ""
		}
	}
	if ttl, err := ResolveDatingPhotoURLTTL(env("")); err != nil || ttl != DefaultDatingPhotoURLTTL {
		t.Fatalf("unset = %s, %v", ttl, err)
	}
	if ttl, err := ResolveDatingPhotoURLTTL(env(" 90 ")); err != nil || ttl != 90*time.Second {
		t.Fatalf("90 = %s, %v", ttl, err)
	}
	for _, bad := range []string{"29", "301", "abc", "-5", "1.5"} {
		if _, err := ResolveDatingPhotoURLTTL(env(bad)); err == nil || !strings.Contains(err.Error(), EnvDatingPhotoURLTTLSeconds) {
			t.Fatalf("%q accepted: %v", bad, err)
		}
	}
}
