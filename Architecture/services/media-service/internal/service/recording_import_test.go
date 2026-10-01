package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Live recording import over fake stores: no database, no object store.

const testRecBucket = "live-recordings"

type fakeRecObject struct {
	data []byte
	etag string
}

type fakeRecBlob struct {
	bucket   string
	sources  map[string]fakeRecObject // "<bucket>/<key>"
	own      map[string]fakeRecObject // this store's bucket
	deleted  []string
	copies   []string // "<srcBucket>/<srcKey> -> <dstKey>"
	copyETag string   // the precondition ETag of the last copy
	// changeOnCopy makes the copy fail its ETag precondition.
	changeOnCopy bool
	// shortCopy makes the stored copy smaller than the source.
	shortCopy bool
}

func newFakeRecBlob() *fakeRecBlob {
	return &fakeRecBlob{bucket: "media", sources: map[string]fakeRecObject{}, own: map[string]fakeRecObject{}}
}

func (b *fakeRecBlob) StatObjectIn(_ context.Context, bucket, key string) (blob.ObjectInfo, error) {
	o, ok := b.sources[bucket+"/"+key]
	if !ok {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(o.data)), ETag: o.etag}, nil
}

func (b *fakeRecBlob) ReadObjectRangeIn(_ context.Context, bucket, key string, start, end int64) ([]byte, error) {
	o, ok := b.sources[bucket+"/"+key]
	if !ok {
		return nil, blob.ErrObjectNotFound
	}
	if end >= int64(len(o.data)) {
		end = int64(len(o.data)) - 1
	}
	return o.data[start : end+1], nil
}

func (b *fakeRecBlob) CopyObjectFrom(_ context.Context, srcBucket, srcKey, srcETag, dstKey, _ string) (blob.ObjectInfo, error) {
	b.copyETag = srcETag
	o, ok := b.sources[srcBucket+"/"+srcKey]
	if !ok {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	if b.changeOnCopy || (srcETag != "" && srcETag != o.etag) {
		return blob.ObjectInfo{}, blob.ErrObjectChanged
	}
	data := o.data
	if b.shortCopy {
		data = data[:len(data)-1]
	}
	b.own[dstKey] = fakeRecObject{data: data, etag: "copy-" + o.etag}
	b.copies = append(b.copies, srcBucket+"/"+srcKey+" -> "+dstKey)
	return blob.ObjectInfo{Size: int64(len(data)), ETag: "copy-" + o.etag}, nil
}

func (b *fakeRecBlob) DeleteObject(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	delete(b.own, key)
	return nil
}

func (b *fakeRecBlob) Bucket() string { return b.bucket }

type fakeRecStore struct {
	rows    map[string]*postgres.ImportedMedia // "<source>/<ref>"
	created []postgres.ImportedVideo
	// raceWinner, when set, is committed by "another import" between the
	// lookup and this call's insert.
	raceWinner *postgres.ImportedMedia
	insertErr  error
}

func newFakeRecStore() *fakeRecStore {
	return &fakeRecStore{rows: map[string]*postgres.ImportedMedia{}}
}

func (s *fakeRecStore) FindImportedMedia(_ context.Context, source, ref string) (*postgres.ImportedMedia, error) {
	if m, ok := s.rows[source+"/"+ref]; ok {
		cp := *m
		return &cp, nil
	}
	return nil, nil
}

func (s *fakeRecStore) CreateImportedVideo(_ context.Context, in postgres.ImportedVideo) (*postgres.ImportedMedia, bool, error) {
	if s.insertErr != nil {
		return nil, false, s.insertErr
	}
	k := in.Source + "/" + in.SourceRef
	if s.raceWinner != nil {
		s.rows[k] = s.raceWinner
		s.raceWinner = nil
	}
	if m, ok := s.rows[k]; ok {
		cp := *m
		return &cp, false, nil
	}
	s.created = append(s.created, in)
	m := &postgres.ImportedMedia{ID: in.ID, UploaderID: in.OwnerID, ProcessingStatus: "processing"}
	s.rows[k] = m
	cp := *m
	return &cp, true, nil
}

// mp4Bytes is a minimal ftyp-led body.
func mp4Bytes(n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	return b
}

type recFixture struct {
	imp    *RecordingImporter
	blob   *fakeRecBlob
	store  *fakeRecStore
	owner  uuid.UUID
	stream uuid.UUID
	key    string
}

func newRecFixture(t *testing.T) *recFixture {
	t.Helper()
	f := &recFixture{blob: newFakeRecBlob(), store: newFakeRecStore(), owner: uuid.New(), stream: uuid.New()}
	f.key = "recordings/" + f.stream.String() + ".mp4"
	f.blob.sources[testRecBucket+"/"+f.key] = fakeRecObject{data: mp4Bytes(4096), etag: "etag-1"}
	imp, err := NewRecordingImporter(f.store, f.blob, RecordingImportConfig{AllowedBucket: testRecBucket, MaxBytes: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	f.imp = imp
	return f
}

func (f *recFixture) input() RecordingImportInput {
	return RecordingImportInput{
		OwnerUserID: f.owner.String(),
		Bucket:      testRecBucket,
		Key:         f.key,
		ContentType: "video/mp4",
		DurationMs:  61_000,
		Source:      "live_recording",
		SourceRef:   f.stream.String(),
	}
}

func TestRecordingImport_CreatesOwnedProcessingVideo(t *testing.T) {
	f := newRecFixture(t)
	res, err := f.imp.Import(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if !res.Created || res.ProcessingStatus != "processing" || res.MediaID == uuid.Nil {
		t.Fatalf("result = %+v", res)
	}
	if len(f.store.created) != 1 {
		t.Fatalf("created %d rows", len(f.store.created))
	}
	row := f.store.created[0]
	if row.OwnerID != f.owner {
		t.Fatalf("owner = %s, want the stream host %s", row.OwnerID, f.owner)
	}
	wantKey := "user/" + f.owner.String() + "/" + res.MediaID.String() + "/original"
	if row.StorageKey != wantKey || row.StorageBucket != "media" {
		t.Fatalf("stored at %s/%s, want media/%s", row.StorageBucket, row.StorageKey, wantKey)
	}
	if row.MimeType != "video/mp4" || row.SizeBytes != 4096 || row.OriginalETag != "copy-etag-1" {
		t.Fatalf("row = %+v", row)
	}
	if row.Source != "live_recording" || row.SourceRef != f.stream.String() {
		t.Fatalf("import key = %s/%s", row.Source, row.SourceRef)
	}
	// The copy came FROM the recordings bucket INTO the media layout; the
	// source is untouched.
	if len(f.blob.copies) != 1 || f.blob.copies[0] != testRecBucket+"/"+f.key+" -> "+wantKey {
		t.Fatalf("copies = %v", f.blob.copies)
	}
	if _, ok := f.blob.sources[testRecBucket+"/"+f.key]; !ok || len(f.blob.deleted) != 0 {
		t.Fatalf("source touched: deleted=%v", f.blob.deleted)
	}
	// The copy is preconditioned on the ETag that was validated.
	if f.blob.copyETag != "etag-1" {
		t.Fatalf("copy precondition ETag = %q, want the stat's etag-1", f.blob.copyETag)
	}
}

func TestRecordingImport_ReclaimExemptPurpose(t *testing.T) {
	f := newRecFixture(t)
	if _, err := f.imp.Import(context.Background(), f.input()); err != nil {
		t.Fatal(err)
	}
	p := f.store.created[0].UploadPurpose
	if p != postgres.UploadPurposeLiveRecording {
		t.Fatalf("upload_purpose = %q, want %q", p, postgres.UploadPurposeLiveRecording)
	}
	if p == postgres.UploadPurposeComposer || p == "" {
		t.Fatalf("upload_purpose %q is the reclaim lease or absent", p)
	}
	// A client cannot claim the import purpose through init.
	if got := normaliseUploadPurpose(postgres.UploadPurposeLiveRecording); got != "" {
		t.Fatalf("normaliseUploadPurpose(live_recording) = %q, want empty", got)
	}
}

func TestRecordingImport_Idempotent(t *testing.T) {
	f := newRecFixture(t)
	first, err := f.imp.Import(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	// A retry with a non-canonical (upper-case) stream id is the same stream.
	in := f.input()
	in.SourceRef = strings.ToUpper(in.SourceRef)
	in.Key = "recordings/" + f.stream.String() + ".mp4"
	second, err := f.imp.Import(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if second.Created || second.MediaID != first.MediaID {
		t.Fatalf("retry = %+v, want the first asset %s uncreated", second, first.MediaID)
	}
	if len(f.store.created) != 1 || len(f.blob.copies) != 1 {
		t.Fatalf("retry wrote: rows=%d copies=%d", len(f.store.created), len(f.blob.copies))
	}
}

func TestRecordingImport_LostRaceDropsOwnCopyOnly(t *testing.T) {
	f := newRecFixture(t)
	winner := &postgres.ImportedMedia{ID: uuid.New(), UploaderID: f.owner, ProcessingStatus: "processing"}
	f.store.raceWinner = winner
	res, err := f.imp.Import(context.Background(), f.input())
	if err != nil {
		t.Fatal(err)
	}
	if res.Created || res.MediaID != winner.ID {
		t.Fatalf("result = %+v, want the winner %s", res, winner.ID)
	}
	if len(f.blob.deleted) != 1 || !strings.HasPrefix(f.blob.deleted[0], "user/"+f.owner.String()+"/") {
		t.Fatalf("deleted = %v, want only this attempt's copy", f.blob.deleted)
	}
}

func TestRecordingImport_OtherOwnerConflict(t *testing.T) {
	f := newRecFixture(t)
	if _, err := f.imp.Import(context.Background(), f.input()); err != nil {
		t.Fatal(err)
	}
	in := f.input()
	in.OwnerUserID = uuid.NewString()
	if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingOwnerConflict) {
		t.Fatalf("err = %v, want ErrRecordingOwnerConflict", err)
	}
}

func TestRecordingImport_BucketAllowList(t *testing.T) {
	f := newRecFixture(t)
	for _, b := range []string{"media", "Live-Recordings", "live-recordings ", "", "other"} {
		f.blob.sources[b+"/"+f.key] = fakeRecObject{data: mp4Bytes(100), etag: "x"}
		in := f.input()
		in.Bucket = b
		if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingBucketNotAllowed) {
			t.Fatalf("bucket %q: err = %v, want ErrRecordingBucketNotAllowed", b, err)
		}
	}
	if len(f.blob.copies) != 0 {
		t.Fatalf("copied from a refused bucket: %v", f.blob.copies)
	}
}

func TestRecordingImport_RefusesMediaBucketAsSource(t *testing.T) {
	_, err := NewRecordingImporter(newFakeRecStore(), newFakeRecBlob(), RecordingImportConfig{AllowedBucket: "media"})
	if err == nil {
		t.Fatal("an importer whose recordings bucket is the media bucket was built")
	}
	if _, err := NewRecordingImporter(newFakeRecStore(), newFakeRecBlob(), RecordingImportConfig{}); err == nil {
		t.Fatal("an importer with no recordings bucket was built")
	}
}

func TestRecordingImport_TraversalRefused(t *testing.T) {
	f := newRecFixture(t)
	id := f.stream.String()
	bad := []string{
		"../recordings/" + id + ".mp4",
		"recordings/../" + id + ".mp4",
		"recordings/./" + id + ".mp4",
		"/recordings/" + id + ".mp4",
		"recordings//" + id + ".mp4",
		"recordings\\" + id + ".mp4",
		"recordings/%2e%2e/" + id + ".mp4",
		"recordings/" + id + ".mp4/",
		"recordings/" + id + ".mp4\x00",
		"recordings/ " + id + ".mp4",
		"recordings/" + id + ".mov",
		"recordings/" + uuid.NewString() + ".mp4", // another stream's file
		"recordings/" + id + "/x.mp4",             // stream id not in the file name
		strings.Repeat("a/", 300) + id + ".mp4",   // too long
		"",
	}
	for _, k := range bad {
		f.blob.sources[testRecBucket+"/"+k] = fakeRecObject{data: mp4Bytes(100), etag: "x"}
		in := f.input()
		in.Key = k
		if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingKeyInvalid) {
			t.Fatalf("key %q: err = %v, want ErrRecordingKeyInvalid", k, err)
		}
	}
	if len(f.blob.copies) != 0 {
		t.Fatalf("copied a refused key: %v", f.blob.copies)
	}
	// Prefixed and suffixed names that still name the stream are fine.
	for _, k := range []string{id + ".mp4", "recordings/2026/10/stream_" + id + "_final.mp4"} {
		if err := ValidateRecordingKey(k, id); err != nil {
			t.Fatalf("key %q refused: %v", k, err)
		}
	}
}

func TestRecordingImport_InvalidRequests(t *testing.T) {
	f := newRecFixture(t)
	cases := map[string]func(*RecordingImportInput){
		"owner missing":     func(in *RecordingImportInput) { in.OwnerUserID = "" },
		"owner nil":         func(in *RecordingImportInput) { in.OwnerUserID = uuid.Nil.String() },
		"source other":      func(in *RecordingImportInput) { in.Source = "upload" },
		"source_ref not id": func(in *RecordingImportInput) { in.SourceRef = "stream-1" },
		"content type":      func(in *RecordingImportInput) { in.ContentType = "video/webm" },
		"negative duration": func(in *RecordingImportInput) { in.DurationMs = -1 },
	}
	for name, mut := range cases {
		in := f.input()
		mut(&in)
		if _, err := f.imp.Import(context.Background(), in); !errors.Is(err, ErrRecordingInvalid) {
			t.Fatalf("%s: err = %v, want ErrRecordingInvalid", name, err)
		}
	}
}

func TestRecordingImport_SourceChecks(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		f := newRecFixture(t)
		delete(f.blob.sources, testRecBucket+"/"+f.key)
		if _, err := f.imp.Import(context.Background(), f.input()); !errors.Is(err, ErrRecordingNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("too large", func(t *testing.T) {
		f := newRecFixture(t)
		f.blob.sources[testRecBucket+"/"+f.key] = fakeRecObject{data: mp4Bytes(1<<20 + 1), etag: "e"}
		if _, err := f.imp.Import(context.Background(), f.input()); !errors.Is(err, ErrRecordingTooLarge) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not an mp4", func(t *testing.T) {
		f := newRecFixture(t)
		f.blob.sources[testRecBucket+"/"+f.key] = fakeRecObject{data: []byte("#!/bin/sh\necho not a video at all\n"), etag: "e"}
		if _, err := f.imp.Import(context.Background(), f.input()); !errors.Is(err, ErrRecordingNotVideo) {
			t.Fatalf("err = %v", err)
		}
		if len(f.blob.copies) != 0 {
			t.Fatal("copied a non-video")
		}
	})
	t.Run("changed during copy", func(t *testing.T) {
		f := newRecFixture(t)
		f.blob.changeOnCopy = true
		if _, err := f.imp.Import(context.Background(), f.input()); !errors.Is(err, ErrRecordingChanged) {
			t.Fatalf("err = %v", err)
		}
		if len(f.store.created) != 0 {
			t.Fatal("registered a changed source")
		}
	})
	t.Run("short copy", func(t *testing.T) {
		f := newRecFixture(t)
		f.blob.shortCopy = true
		if _, err := f.imp.Import(context.Background(), f.input()); !errors.Is(err, ErrRecordingChanged) {
			t.Fatalf("err = %v", err)
		}
		if len(f.store.created) != 0 || len(f.blob.deleted) != 1 {
			t.Fatalf("rows=%d deleted=%v", len(f.store.created), f.blob.deleted)
		}
	})
	t.Run("insert fails, copy dropped", func(t *testing.T) {
		f := newRecFixture(t)
		f.store.insertErr = errors.New("db down")
		if _, err := f.imp.Import(context.Background(), f.input()); err == nil {
			t.Fatal("no error")
		}
		if len(f.blob.deleted) != 1 || !strings.HasPrefix(f.blob.deleted[0], "user/") {
			t.Fatalf("deleted = %v", f.blob.deleted)
		}
	})
}

func TestRecordingImportConfigFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	cfg, err := RecordingImportConfigFromEnv(env(nil))
	if err != nil || cfg.AllowedBucket != "live-recordings" || cfg.MaxBytes != MaxVideoSize {
		t.Fatalf("defaults = %+v, %v", cfg, err)
	}
	cfg, err = RecordingImportConfigFromEnv(env(map[string]string{EnvLiveRecordingsBucket: "rec", EnvLiveRecordingMaxBytes: "1000"}))
	if err != nil || cfg.AllowedBucket != "rec" || cfg.MaxBytes != 1000 {
		t.Fatalf("set = %+v, %v", cfg, err)
	}
	for _, bad := range []string{"0", "-5", "lots"} {
		if _, err := RecordingImportConfigFromEnv(env(map[string]string{EnvLiveRecordingMaxBytes: bad})); err == nil {
			t.Fatalf("max bytes %q accepted", bad)
		}
	}
}
