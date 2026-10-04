package service

import (
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"testing"
)

func TestDoorstepPhotoPublicReadGuard(t *testing.T) {
	owner := uuid.New()
	asset := &postgres.MediaAsset{UploaderID: owner, AccessScope: postgres.AccessScopeDoorstepPhoto}
	if !DatingScopeDenies(asset, uuid.New()) || !DatingScopeDenies(asset, uuid.Nil) {
		t.Fatal("private visit photo publicly readable")
	}
	if DatingScopeDenies(asset, owner) {
		t.Fatal("owner denied")
	}
	if captionReadLocalVerdict(asset, uuid.New()) != captionReadDenied {
		t.Fatal("caption/record gate bypassed")
	}
}
