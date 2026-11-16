package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/google/uuid"
)

// Joining a multi-part live recording, over a fake media bucket and faked
// ffprobe/ffmpeg seams.

type fakeJoinBlobs struct {
	objects  map[string][]byte
	listed   []string // prefixes listed
	uploaded map[string][]byte
	deleted  []string
}

func (b *fakeJoinBlobs) StatObject(_ context.Context, key string) (blob.ObjectInfo, error) {
	o, ok := b.objects[key]
	if !ok {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(o)), ETag: "e-" + key}, nil
}

func (b *fakeJoinBlobs) ListObjectKeys(_ context.Context, prefix string) ([]string, error) {
	b.listed = append(b.listed, prefix)
	var out []string
	for k := range b.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	// Unordered on purpose: the join must sort.
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out, nil
}

func (b *fakeJoinBlobs) DownloadObjectToFileIfMatch(_ context.Context, key, path, etag string) (int64, error) {
	o, ok := b.objects[key]
	if !ok {
		return 0, blob.ErrObjectNotFound
	}
	if etag != "e-"+key {
		return 0, blob.ErrObjectChanged
	}
	return int64(len(o)), os.WriteFile(path, o, 0o600)
}

func (b *fakeJoinBlobs) UploadFileInfo(_ context.Context, key, path, _ string) (blob.ObjectInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return blob.ObjectInfo{}, err
	}
	b.uploaded[key] = data
	b.objects[key] = data
	return blob.ObjectInfo{Size: int64(len(data)), ETag: "joined"}, nil
}

func (b *fakeJoinBlobs) DeleteObject(_ context.Context, key string) error {
	b.deleted = append(b.deleted, key)
	delete(b.objects, key)
	return nil
}

type fakeRewriter struct {
	etag string
	size int64
}

func (r *fakeRewriter) RecordOriginalRewrite(_ context.Context, _ uuid.UUID, etag string, size int64) error {
	r.etag, r.size = etag, size
	return nil
}

func ftypBytes(tag byte, n int) []byte {
	b := make([]byte, n)
	copy(b, []byte{0, 0, 0, 0x20, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm'})
	b[n-1] = tag
	return b
}

type joinCall struct {
	parts []processing.JoinPart
	tags  []byte
}

func installJoinSeams(t *testing.T) *joinCall {
	t.Helper()
	call := &joinCall{}
	prevProbe, prevJoin := probeJoinPart, joinVideoParts
	t.Cleanup(func() { probeJoinPart, joinVideoParts = prevProbe, prevJoin })
	probeJoinPart = func(_ context.Context, _ string) (processing.PartStreams, error) {
		return processing.PartStreams{Video: &processing.PartVideo{Codec: "h264", Width: 2, Height: 2}, DurationSec: 1}, nil
	}
	joinVideoParts = func(_ context.Context, parts []processing.JoinPart, _ string, out string) (processing.JoinMode, error) {
		call.parts = parts
		for _, p := range parts {
			b, err := os.ReadFile(p.Path)
			if err != nil {
				t.Fatal(err)
			}
			call.tags = append(call.tags, b[len(b)-1])
		}
		return processing.JoinCopy, os.WriteFile(out, ftypBytes('J', 64), 0o600)
	}
	return call
}

func joinSubject() *transcodeSubject {
	owner, id := uuid.New(), uuid.New()
	return &transcodeSubject{ID: id, UploaderID: owner, StorageKey: "user/" + owner.String() + "/" + id.String() + "/original", MimeType: "video/mp4"}
}

func noDeadline(time.Duration) context.Context { return context.Background() }

func TestJoinRecordingParts_JoinsInOrderAndReplacesTheCopies(t *testing.T) {
	call := installJoinSeams(t)
	s := joinSubject()
	prefix := s.assetPrefix()
	b := &fakeJoinBlobs{objects: map[string][]byte{
		processing.RecordingPartKey(prefix, 1): ftypBytes('1', 100),
		processing.RecordingPartKey(prefix, 2): ftypBytes('2', 100),
		processing.RecordingPartKey(prefix, 3): ftypBytes('3', 100),
		prefix + "parts/notes.txt":             []byte("x"), // not a part
	}, uploaded: map[string][]byte{}}
	rw := &fakeRewriter{}
	tmp := t.TempDir()
	path, err := joinRecordingParts(context.Background(), b, rw, s, tmp, 1<<20, noDeadline)
	if err != nil {
		t.Fatal(err)
	}
	if path != filepath.Join(tmp, "original") || string(call.tags) != "123" {
		t.Fatalf("path=%q joined order=%q", path, call.tags)
	}
	if _, ok := b.uploaded[s.StorageKey]; !ok || rw.etag != "joined" || rw.size != 64 {
		t.Fatalf("original not uploaded/recorded: %v %+v", b.uploaded, rw)
	}
	if len(b.deleted) != 3 {
		t.Fatalf("deleted = %v", b.deleted)
	}
	if _, err := os.Stat(filepath.Join(tmp, "parts")); !os.IsNotExist(err) {
		t.Fatal("local parts left behind")
	}
}

func TestJoinRecordingParts_OriginalPresentDoesNothing(t *testing.T) {
	installJoinSeams(t)
	s := joinSubject()
	b := &fakeJoinBlobs{objects: map[string][]byte{s.StorageKey: ftypBytes('o', 64)}, uploaded: map[string][]byte{}}
	path, err := joinRecordingParts(context.Background(), b, &fakeRewriter{}, s, t.TempDir(), 1<<20, noDeadline)
	if err != nil || path != "" || len(b.listed) != 0 {
		t.Fatalf("path=%q err=%v listed=%v", path, err, b.listed)
	}
}

func TestJoinRecordingParts_NoPartsLeavesTheNormalFailure(t *testing.T) {
	installJoinSeams(t)
	s := joinSubject()
	b := &fakeJoinBlobs{objects: map[string][]byte{}, uploaded: map[string][]byte{}}
	path, err := joinRecordingParts(context.Background(), b, &fakeRewriter{}, s, t.TempDir(), 1<<20, noDeadline)
	if err != nil || path != "" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestJoinRecordingParts_OnePartIsPlacedWithoutAJoin(t *testing.T) {
	call := installJoinSeams(t)
	s := joinSubject()
	b := &fakeJoinBlobs{objects: map[string][]byte{
		processing.RecordingPartKey(s.assetPrefix(), 2): ftypBytes('2', 100),
	}, uploaded: map[string][]byte{}}
	path, err := joinRecordingParts(context.Background(), b, &fakeRewriter{}, s, t.TempDir(), 1<<20, noDeadline)
	if err != nil || path == "" || call.parts != nil {
		t.Fatalf("path=%q err=%v joined=%v", path, err, call.parts)
	}
	if got := b.uploaded[s.StorageKey]; len(got) != 100 || got[99] != '2' {
		t.Fatalf("uploaded = %d bytes", len(got))
	}
}

func TestJoinRecordingParts_NonMP4PartLeftOut(t *testing.T) {
	call := installJoinSeams(t)
	s := joinSubject()
	p := s.assetPrefix()
	b := &fakeJoinBlobs{objects: map[string][]byte{
		processing.RecordingPartKey(p, 1): ftypBytes('1', 100),
		processing.RecordingPartKey(p, 2): []byte("this is not a video at all"),
		processing.RecordingPartKey(p, 3): ftypBytes('3', 100),
	}, uploaded: map[string][]byte{}}
	if _, err := joinRecordingParts(context.Background(), b, &fakeRewriter{}, s, t.TempDir(), 1<<20, noDeadline); err != nil {
		t.Fatal(err)
	}
	if string(call.tags) != "13" {
		t.Fatalf("joined %q, want parts 1 and 3", call.tags)
	}
}

func TestJoinRecordingParts_OverCapIsTerminal(t *testing.T) {
	installJoinSeams(t)
	s := joinSubject()
	p := s.assetPrefix()
	b := &fakeJoinBlobs{objects: map[string][]byte{
		processing.RecordingPartKey(p, 1): ftypBytes('1', 100),
		processing.RecordingPartKey(p, 2): ftypBytes('2', 100),
	}, uploaded: map[string][]byte{}}
	_, err := joinRecordingParts(context.Background(), b, &fakeRewriter{}, s, t.TempDir(), 150, noDeadline)
	if !isPermanentTranscodeFailure(err) {
		t.Fatalf("err = %v, want terminal", err)
	}
}

func TestRecordingPartKeys(t *testing.T) {
	got := recordingPartKeys([]string{"a/parts/010.mp4", "a/parts/002.mp4", "a/parts/x/003.mp4", "a/parts/notes.txt", "a/original"}, "a/parts/")
	if strings.Join(got, ",") != "a/parts/002.mp4,a/parts/010.mp4" {
		t.Fatalf("keys = %v", got)
	}
}
