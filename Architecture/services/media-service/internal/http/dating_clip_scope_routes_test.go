package http

import (
	"net/http"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Pulse dating clips on the public read routes. Each fixture below already
// holds a dating PHOTO asset whose delivery authority would admit anyone;
// re-scoping it to 'dating_clip' must keep every route uploader-only.
// Dropping 'dating_clip' from postgres.IsDatingScope fails each test.

// GET /v1/media/:id and /status, viewer and keyed service caller.
func TestDatingClipRecordStaysUploaderOnly(t *testing.T) {
	f := newRecordFixture(t)
	f.store.media[f.datingID].AccessScope = postgres.AccessScopeDatingClip
	id := f.datingID
	if rec := f.info(id, f.owner); rec.Code != http.StatusOK {
		t.Fatalf("owner: got %d want 200", rec.Code)
	}
	if rec := f.status(id, f.owner); rec.Code != http.StatusOK {
		t.Fatalf("owner status: got %d want 200", rec.Code)
	}
	wantDeniedLikeMissing(t, "clip other viewer", f.info(id, f.viewer), f.info(uuid.New(), f.viewer))
	wantDeniedLikeMissing(t, "clip signed-out", f.info(id, uuid.Nil), f.info(uuid.New(), uuid.Nil))
	wantDeniedLikeMissing(t, "clip service caller", f.info(id, uuid.Nil, internalKeyHeader, recordInternalKey),
		f.info(uuid.New(), uuid.Nil, internalKeyHeader, recordInternalKey))
	for name, viewer := range map[string]uuid.UUID{"other viewer": f.viewer, "signed-out": uuid.Nil} {
		if rec := f.status(id, viewer); rec.Code != http.StatusNotFound {
			t.Errorf("clip status, %s: got %d %s want 404", name, rec.Code, rec.Body.String())
		}
	}
	if f.authz.calls != 0 {
		t.Errorf("a dating clip is settled locally; the authority was asked %d times", f.authz.calls)
	}
}

// HEAD /v1/media/:id/serve/:variant.
func TestHeadDatingClipIsTheUploadersAlone(t *testing.T) {
	f := newHeadFixture(t)
	f.store.media[f.datingID].AccessScope = postgres.AccessScopeDatingClip
	wantHead(t, "clip, uploader", f.head(f.datingID, "720p", f.owner), "video/mp4", head720pSize)
	for name, viewer := range map[string]uuid.UUID{"stranger": f.stranger, "signed-out": uuid.Nil} {
		f.authz.viewers = nil
		wantHeadDeniedLikeMissing(t, "clip, "+name, f.head(f.datingID, "720p", viewer), f.head(uuid.New(), "720p", viewer))
		if len(f.authz.viewers) != 0 {
			t.Errorf("clip, %s: the authority was asked; the scope is settled before it", name)
		}
	}
}

// POST /v1/media/internal/:id/sound: a dating clip's video has no sound.
func TestEnsureSoundRefusesADatingClip(t *testing.T) {
	f, reel := newEnsureFixture(t)
	f.store.media[reel].AccessScope = postgres.AccessScopeDatingClip
	rec := f.ensure(reel, `{"title":"Take"}`)
	if rec.Code != http.StatusNotFound || errorCode(t, rec) != "NOT_FOUND" {
		t.Fatalf("got %d %s want 404 NOT_FOUND", rec.Code, rec.Body.String())
	}
	wantNothingRead(t, "dating clip", f, 0)
}
