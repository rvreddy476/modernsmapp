package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Reels audit 2026-10-30: delivery of video originals, blob reclaim, and
// sounds that may no longer go on new posts.

// ── finding 4: who gets a video's original ───────────────────────────

func TestBatchNeverSignsAVideoOriginalForOthers(t *testing.T) {
	uploader := uuid.New()
	v := &postgres.MediaAsset{FileType: "video", UploaderID: uploader, ProcessingStatus: "ready"}
	for name, viewer := range map[string]uuid.UUID{"signed out": uuid.Nil, "another user": uuid.New(), "the uploader": uploader} {
		if batchIncludesOriginal(v, viewer) {
			t.Fatalf("%s got a ready video's original from the batch", name)
		}
	}
	processing := &postgres.MediaAsset{FileType: "video", UploaderID: uploader, ProcessingStatus: "processing"}
	if !batchIncludesOriginal(processing, uploader) {
		t.Fatal("the uploader lost the instant-publish preview of their own upload")
	}
	if batchIncludesOriginal(processing, uuid.New()) || batchIncludesOriginal(processing, uuid.Nil) {
		t.Fatal("someone else got a processing video's original")
	}
	if !batchIncludesOriginal(&postgres.MediaAsset{FileType: "image"}, uuid.Nil) {
		t.Fatal("an image lost its (EXIF-stripped) original")
	}
}

type statReader struct {
	info  blob.ObjectInfo
	err   error
	stats int
}

func (s *statReader) StatObject(context.Context, string) (blob.ObjectInfo, error) {
	s.stats++
	return s.info, s.err
}
func (s *statReader) OpenObjectRange(context.Context, string, int64, int64) (io.ReadCloser, error) {
	return nil, errors.New("not used")
}

func videoMedia(uploader uuid.UUID, etag string, created time.Time) *postgres.MediaAsset {
	id := uuid.New()
	return &postgres.MediaAsset{ID: id, FileType: "video", UploaderID: uploader, OriginalETag: etag,
		StorageKey: fmt.Sprintf("user/%s/%s/original", uploader, id), CreatedAt: created}
}

func TestVideoOriginalIsTheUploadersAlone(t *testing.T) {
	uploader := uuid.New()
	r := &statReader{info: blob.ObjectInfo{ETag: "e1"}}
	s := (&Service{}).WithObjectReader(r)
	m := videoMedia(uploader, "e1", time.Now())
	if !s.originalServable(context.Background(), m, uploader) {
		t.Fatal("the uploader was refused their own unchanged original")
	}
	for name, viewer := range map[string]uuid.UUID{"signed out": uuid.Nil, "another user": uuid.New()} {
		if s.originalServable(context.Background(), m, viewer) {
			t.Fatalf("%s was handed a video original", name)
		}
	}
	if !s.originalServable(context.Background(), &postgres.MediaAsset{FileType: "image"}, uuid.Nil) {
		t.Fatal("image originals are decided by the gate, not here")
	}
	legacy := videoMedia(uploader, "", time.Now())
	if !s.originalServable(context.Background(), legacy, uploader) {
		t.Fatal("an upload confirmed before identity was recorded is still its uploader's")
	}
}

// Finding 6: an original replaced after confirm is refused.
func TestReplacedOriginalIsRefused(t *testing.T) {
	uploader := uuid.New()
	r := &statReader{info: blob.ObjectInfo{ETag: "replaced"}}
	s := (&Service{}).WithObjectReader(r)
	if s.originalServable(context.Background(), videoMedia(uploader, "confirmed", time.Now()), uploader) {
		t.Fatal("a replaced original was served")
	}
	unreadable := (&Service{}).WithObjectReader(&statReader{err: errors.New("s3 down")})
	if unreadable.originalServable(context.Background(), videoMedia(uploader, "confirmed", time.Now()), uploader) {
		t.Fatal("an unverifiable original was served")
	}
}

// A positive check after the upload URL expired is remembered; one inside
// the window is not (the object can still change).
func TestOriginalVerificationIsRememberedOnlyAfterTheUploadWindow(t *testing.T) {
	uploader := uuid.New()
	r := &statReader{info: blob.ObjectInfo{ETag: "e1"}}
	s := (&Service{}).WithObjectReader(r)
	fresh := videoMedia(uploader, "e1", time.Now())
	for i := 0; i < 3; i++ {
		s.originalServable(context.Background(), fresh, uploader)
	}
	if r.stats != 3 {
		t.Fatalf("inside the upload window: %d stats, want one per read", r.stats)
	}
	r.stats = 0
	old := videoMedia(uploader, "e1", time.Now().Add(-time.Hour))
	for i := 0; i < 3; i++ {
		if !s.originalServable(context.Background(), old, uploader) {
			t.Fatal("refused")
		}
	}
	if r.stats != 1 {
		t.Fatalf("after the upload window: %d stats, want 1", r.stats)
	}
}

func TestViewerWithoutTheOriginalPlaysTheBestRendition(t *testing.T) {
	urls := map[string]string{"360p": "u360", "720p": "u720", "thumb_150": "t"}
	if url, kind := choosePlayback("video", "", urls); url != "u720" || kind != PlaybackKindOriginal {
		t.Fatalf("got (%q, %q), want the 720p rendition as a progressive file", url, kind)
	}
	if url, _ := choosePlayback("video", "", map[string]string{"original": "o", "720p": "u720"}); url != "o" {
		t.Fatal("the uploader's original no longer comes first")
	}
	key := bestRenditionKey([]postgres.MediaVariant{{Name: "thumb_150", ObjectKey: "t"}, {Name: "480p", ObjectKey: "k480"}, {Name: "1080p", ObjectKey: "k1080"}})
	if key != "k480" {
		t.Fatalf("best rendition %q", key)
	}
	if bestRenditionKey([]postgres.MediaVariant{{Name: "thumb_150", ObjectKey: "t"}}) != "" {
		t.Fatal("a thumbnail is not a rendition")
	}
}

func TestSameMIME(t *testing.T) {
	if !sameMIME("video/mp4", "VIDEO/MP4; codecs=avc1") || sameMIME("text/html", "video/mp4") || sameMIME("", "") {
		t.Fatal("sameMIME")
	}
}

// ── finding 8: reclaim ────────────────────────────────────────────────

type reclaimFake struct {
	objects  map[string]bool
	failKey  string
	failures map[string]string
	cleared  []string
}

func (f *reclaimFake) DeleteObject(_ context.Context, key string) error {
	if key == f.failKey {
		return errors.New("s3 503")
	}
	delete(f.objects, key)
	return nil
}
func (f *reclaimFake) ListObjectKeys(_ context.Context, prefix string) ([]string, error) {
	var out []string
	for k := range f.objects {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out, nil
}
func (f *reclaimFake) RecordBlobReclaimFailure(_ context.Context, key, reason string) error {
	f.failures[key] = reason
	return nil
}
func (f *reclaimFake) ClearBlobReclaim(_ context.Context, key string) error {
	f.cleared = append(f.cleared, key)
	return nil
}

func TestReclaimDeletesTheWholeAssetPrefix(t *testing.T) {
	u, m := uuid.NewString(), uuid.NewString()
	prefix := "user/" + u + "/" + m + "/"
	other := "user/" + u + "/" + uuid.NewString() + "/original"
	f := &reclaimFake{objects: map[string]bool{
		prefix + "original": true, prefix + "hls/master.m3u8": true, prefix + "hls/720p_000.ts": true,
		prefix + "storyboard.jpg": true, other: true,
	}, failures: map[string]string{}}
	reclaimKeys(context.Background(), f, f, []string{prefix + "original", prefix})
	if len(f.objects) != 1 || !f.objects[other] {
		t.Fatalf("left %v, want only the other asset's object", f.objects)
	}
	if len(f.cleared) != 2 || len(f.failures) != 0 {
		t.Fatalf("cleared %v failures %v", f.cleared, f.failures)
	}
}

func TestReclaimRefusesABroadPrefix(t *testing.T) {
	u := uuid.NewString()
	f := &reclaimFake{objects: map[string]bool{"user/" + u + "/a/original": true}, failures: map[string]string{}}
	for _, broad := range []string{"user/", "user/" + u + "/", "/", "user/x/y/"} {
		reclaimKeys(context.Background(), f, f, []string{broad})
		if _, ok := f.failures[broad]; !ok {
			t.Fatalf("%q was not refused", broad)
		}
	}
	if len(f.objects) != 1 || len(f.cleared) != 0 {
		t.Fatalf("a broad prefix deleted something: %v cleared %v", f.objects, f.cleared)
	}
}

func TestReclaimFailureLeavesTheRowForTheSweeper(t *testing.T) {
	u, m := uuid.NewString(), uuid.NewString()
	prefix := "user/" + u + "/" + m + "/"
	f := &reclaimFake{objects: map[string]bool{prefix + "hls/a.ts": true, prefix + "720p": true},
		failKey: prefix + "hls/a.ts", failures: map[string]string{}}
	reclaimKeys(context.Background(), f, f, []string{prefix})
	if _, ok := f.failures[prefix]; !ok || len(f.cleared) != 0 {
		t.Fatalf("a partly failed prefix was cleared: failures %v cleared %v", f.failures, f.cleared)
	}
	if f.objects[prefix+"720p"] {
		t.Fatal("the deletable object was left behind")
	}
}

// ── finding 13: sounds ────────────────────────────────────────────────

type reelSoundStore struct {
	tracks map[uuid.UUID]*postgres.AudioTrack
	uses   int
}

func (s *reelSoundStore) GetMediaWithVariants(context.Context, uuid.UUID) (*postgres.MediaAsset, error) {
	return nil, errors.New("not used")
}
func (s *reelSoundStore) GetTranscodingJobs(context.Context, uuid.UUID) ([]postgres.TranscodingJob, error) {
	return nil, nil
}
func (s *reelSoundStore) GetAudioTrack(_ context.Context, id uuid.UUID) (*postgres.AudioTrack, error) {
	t, ok := s.tracks[id]
	if !ok {
		return nil, errors.New("no rows")
	}
	cp := *t
	return &cp, nil
}
func (s *reelSoundStore) SearchAudioTracks(context.Context, string, int, int) ([]postgres.AudioTrack, error) {
	var out []postgres.AudioTrack
	for _, t := range s.tracks {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
	return out, nil
}
func (s *reelSoundStore) IncrementAudioUsageCount(context.Context, uuid.UUID) error {
	s.uses++
	return nil
}

func librarySound(title, block string) *postgres.AudioTrack {
	lib := uuid.New()
	return &postgres.AudioTrack{ID: uuid.New(), LibraryID: &lib, Title: title, Status: "ready", ReuseBlock: block}
}

func TestUnavailableLibrarySoundCannotBeUsedButStillPlays(t *testing.T) {
	ctx := context.Background()
	viewer := uuid.New()
	for _, block := range []string{postgres.SoundReuseWithdrawn, postgres.SoundReuseInactive, postgres.SoundReuseLicenseExpired} {
		tr := librarySound("S", block)
		store := &reelSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{tr.ID: tr}}
		r := NewRecordReads(store, nil, nil)
		if err := r.UseAudioTrackAsViewer(ctx, viewer, tr.ID); !errors.Is(err, ErrSoundUnavailableForNewPosts) {
			t.Fatalf("%s: use = %v, want ErrSoundUnavailableForNewPosts", block, err)
		}
		if store.uses != 0 {
			t.Fatalf("%s: a refused use was counted", block)
		}
		got, err := r.AudioTrackForViewer(ctx, viewer, tr.ID)
		if err != nil || got.AvailableForNewPosts() {
			t.Fatalf("%s: the sound must still answer (existing posts play it), as unavailable: %v %v", block, got, err)
		}
	}
	ok := librarySound("OK", "")
	store := &reelSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{ok.ID: ok}}
	if err := NewRecordReads(store, nil, nil).UseAudioTrackAsViewer(ctx, viewer, ok.ID); err != nil || store.uses != 1 {
		t.Fatalf("an available sound: %v (uses %d)", err, store.uses)
	}
}

func TestSearchLeavesOutSoundsUnavailableForNewPosts(t *testing.T) {
	ok, gone := librarySound("A ok", ""), librarySound("B expired", postgres.SoundReuseLicenseExpired)
	store := &reelSoundStore{tracks: map[uuid.UUID]*postgres.AudioTrack{ok.ID: ok, gone.ID: gone}}
	got, err := NewRecordReads(store, nil, nil).SearchAudioForViewer(context.Background(), uuid.Nil, "x", 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != ok.ID {
		t.Fatalf("search answered %v", got)
	}
}

func TestLibraryUseRefusesAMaterialisedSoundPastItsLicence(t *testing.T) {
	active := uuid.New()
	store := &fakeLibraryStore{
		rows: map[uuid.UUID]*postgres.AudioLibraryTrack{
			active: {ID: active, Title: "T", AudioURL: "library/t.m4a", IsActive: true},
		},
		tracks: map[uuid.UUID]*postgres.AudioTrack{},
	}
	l := active
	store.tracks[active] = &postgres.AudioTrack{ID: uuid.New(), LibraryID: &l, AudioKey: "library/t.m4a",
		Status: "ready", ReuseBlock: postgres.SoundReuseLicenseExpired}
	if got, err := NewLibrarySounds(store, nil).Use(context.Background(), uuid.New(), active); !errors.Is(err, ErrLibrarySoundNotFound) || got != nil {
		t.Fatalf("Use = %v, %v; want ErrLibrarySoundNotFound", got, err)
	}
}

func TestSoundWireCarriesAvailability(t *testing.T) {
	tr := librarySound("S", postgres.SoundReuseWithdrawn)
	raw, err := tr.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"available_for_new_posts":false`) || !strings.Contains(string(raw), `"unavailable_reason":"withdrawn"`) {
		t.Fatalf("wire %s", raw)
	}
	ok, _ := librarySound("S", "").MarshalJSON()
	if !strings.Contains(string(ok), `"available_for_new_posts":true`) || strings.Contains(string(ok), "unavailable_reason") {
		t.Fatalf("wire %s", ok)
	}
}

// Findings 5 and 7: confirm checks the stored type and the bytes against
// the declaration.
func TestConfirmChecksStoredTypeAndContainer(t *testing.T) {
	v := &postgres.MediaAsset{FileType: "video", MimeType: "video/mp4"}
	if !uploadedTypeMatches(v, blob.ObjectInfo{ContentType: "video/mp4"}) {
		t.Fatal("the declared type was refused")
	}
	for _, ct := range []string{"text/html", "application/octet-stream", ""} {
		if uploadedTypeMatches(v, blob.ObjectInfo{ContentType: ct}) {
			t.Fatalf("stored %q accepted for a video/mp4 upload", ct)
		}
	}
	mp4 := []byte("\x00\x00\x00\x18ftypisom\x00\x00\x02\x00")
	avi := []byte("RIFF\x00\x00\x00\x00AVI LIST")
	if !uploadHeaderValid("video", "video/mp4", mp4) {
		t.Fatal("an MP4 refused")
	}
	if uploadHeaderValid("video", "video/mp4", avi) {
		t.Fatal("AVI bytes accepted as video/mp4")
	}
	if uploadHeaderValid("video", "video/webm", mp4) {
		t.Fatal("MP4 bytes accepted as video/webm")
	}
	if !uploadHeaderValid("image", "image/jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}) {
		t.Fatal("a JPEG refused")
	}
}
