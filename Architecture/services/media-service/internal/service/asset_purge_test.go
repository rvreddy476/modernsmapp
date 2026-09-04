package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Post purge → asset delete: media-service must remove EVERY object under
// the asset's key prefix (original, variants, thumb, hls/*), not only the
// keys its row tables recorded, and must refuse when its own reference
// check says something else still holds the asset.

type fakePurgeStore struct {
	rec        *postgres.AssetPurgeRecord
	err        error
	cleared    []string
	failedKeys []string
}

func (f *fakePurgeStore) DeleteAssetForReferrer(_ context.Context, _ uuid.UUID, _ string, _ uuid.UUID) (*postgres.AssetPurgeRecord, error) {
	return f.rec, f.err
}
func (f *fakePurgeStore) ClearBlobReclaim(_ context.Context, k string) error {
	f.cleared = append(f.cleared, k)
	return nil
}
func (f *fakePurgeStore) RecordBlobReclaimFailure(_ context.Context, k, _ string) error {
	f.failedKeys = append(f.failedKeys, k)
	return nil
}

// fakeBlobs is an in-memory bucket keyed by object key.
type fakeBlobs struct {
	objects map[string]struct{}
	deleted []string
	failKey string
}

func (b *fakeBlobs) ListObjectKeys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range b.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (b *fakeBlobs) DeleteObject(_ context.Context, k string) error {
	if k == b.failKey {
		return errors.New("s3: 503")
	}
	delete(b.objects, k)
	b.deleted = append(b.deleted, k)
	return nil
}

func TestAssetPurge_RemovesEveryObjectUnderThePrefix(t *testing.T) {
	uploader, media, post := uuid.New(), uuid.New(), uuid.New()
	other := uuid.New()
	prefix := postgres.AssetPrefix(uploader, media)
	blobs := &fakeBlobs{objects: map[string]struct{}{
		prefix + "original":            {},
		prefix + "thumb_150.jpg":       {},
		prefix + "medium_1080.jpg":     {},
		prefix + "frames/frame_01.jpg": {},
		prefix + "hls/master.m3u8":     {},
		prefix + "hls/720p/index.m3u8": {},
		prefix + "hls/720p/seg_000.ts": {},
		prefix + "hls/720p/seg_001.ts": {},
		// A sibling asset of the same uploader must be untouched.
		postgres.AssetPrefix(uploader, other) + "original": {},
	}}
	// The row tables only knew about the original and one variant.
	store := &fakePurgeStore{rec: &postgres.AssetPurgeRecord{
		MediaID: media, UploaderID: uploader, Prefix: prefix,
		ObjectKeys: []string{prefix + "original", prefix + "thumb_150.jpg"},
	}}

	res, err := NewAssetPurger(store, blobs, nil).Purge(context.Background(), media, Referrer{Kind: "post", ID: post})
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if res.ObjectsDeleted != 8 || res.ObjectsFailed != 0 {
		t.Fatalf("result %+v, want 8 deleted / 0 failed", res)
	}
	if len(blobs.objects) != 1 {
		t.Fatalf("objects left in bucket: %v — everything under %s must be gone", blobs.objects, prefix)
	}
	if _, ok := blobs.objects[postgres.AssetPrefix(uploader, other)+"original"]; !ok {
		t.Fatal("the sibling asset's object was deleted")
	}
	// Every deleted key had its reclaim row cleared (the two recorded ones
	// existed; clearing the others is a harmless no-op).
	if len(store.cleared) != 8 {
		t.Fatalf("cleared %d reclaim rows, want 8", len(store.cleared))
	}
}

func TestAssetPurge_RefusesWhenStoreSaysStillReferenced(t *testing.T) {
	blobs := &fakeBlobs{objects: map[string]struct{}{"user/x/y/original": {}}}
	store := &fakePurgeStore{err: postgres.ErrMediaStillReferenced}
	_, err := NewAssetPurger(store, blobs, nil).Purge(context.Background(), uuid.New(), Referrer{Kind: "post", ID: uuid.New()})
	if !errors.Is(err, ErrAssetStillReferenced) {
		t.Fatalf("err = %v, want ErrAssetStillReferenced", err)
	}
	if len(blobs.deleted) != 0 {
		t.Fatal("objects were deleted although the rows were refused")
	}
}

func TestAssetPurge_FailedObjectStaysInReclaimLedger(t *testing.T) {
	uploader, media := uuid.New(), uuid.New()
	prefix := postgres.AssetPrefix(uploader, media)
	blobs := &fakeBlobs{objects: map[string]struct{}{
		prefix + "original":        {},
		prefix + "hls/master.m3u8": {},
	}, failKey: prefix + "hls/master.m3u8"}
	store := &fakePurgeStore{rec: &postgres.AssetPurgeRecord{
		MediaID: media, UploaderID: uploader, Prefix: prefix, ObjectKeys: []string{prefix + "original"},
	}}
	res, err := NewAssetPurger(store, blobs, nil).Purge(context.Background(), media, Referrer{Kind: "post", ID: uuid.New()})
	if err != nil {
		t.Fatalf("rows are gone and the listing worked: the purge must report success, got %v", err)
	}
	if res.ObjectsDeleted != 1 || res.ObjectsFailed != 1 {
		t.Fatalf("result %+v", res)
	}
	if len(store.failedKeys) != 1 || store.failedKeys[0] != prefix+"hls/master.m3u8" {
		t.Fatalf("failed key not recorded for the sweeper: %v", store.failedKeys)
	}
}

func TestParseReferrer(t *testing.T) {
	if _, err := ParseReferrer("post", uuid.New().String()); err != nil {
		t.Fatal(err)
	}
	if _, err := ParseReferrer("story", uuid.New().String()); err == nil {
		t.Fatal("only post referrers are accepted")
	}
	if _, err := ParseReferrer("post", "nope"); err == nil {
		t.Fatal("referrer_id must be a uuid")
	}
}
