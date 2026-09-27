package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/media-service/internal/dubbing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

func TestCheckAudioDurationTolerance(t *testing.T) {
	cases := []struct {
		audio, video int
		ok           bool
	}{
		{30_000, 30_000, true},
		{32_900, 30_000, true},  // 2.9 s off, tolerance 3 s
		{33_100, 30_000, false}, // 3.1 s off
		{26_900, 30_000, false},
		{300_000, 290_000, true},  // 10 s off on a 290 s video: 5 % = 14.5 s
		{300_000, 280_000, false}, // 20 s off: 5 % = 14 s
		{10_000, 0, true},         // unknown video duration is not compared
	}
	for _, c := range cases {
		err := checkAudioDuration(c.audio, c.video)
		if (err == nil) != c.ok {
			t.Errorf("audio %d video %d: ok=%v err=%v", c.audio, c.video, c.ok, err)
		}
		if err != nil && !errors.Is(err, ErrAudioDurationMismatch) {
			t.Errorf("wrong error type: %v", err)
		}
	}
}

func TestVideoRungsPrefersLadderBestFirstElseOriginal(t *testing.T) {
	w := 720
	media := &postgres.MediaAsset{StorageKey: "orig", Width: &w, Variants: []postgres.MediaVariant{
		{Name: "360p", Mime: "video/mp4", ObjectKey: "k360"},
		{Name: "thumb_150", Mime: "image/jpeg", ObjectKey: "thumb"},
		{Name: "720p", Mime: "video/mp4", ObjectKey: "k720"},
		{Name: "dub_hi_720p", Mime: "video/mp4", ObjectKey: "dub"}, // never a mux input
		{Name: "480p", Mime: "video/mp4", ObjectKey: "k480"},
	}}
	got := videoRungs(media)
	names := make([]string, len(got))
	for i, r := range got {
		names[i] = r.Name
	}
	if strings.Join(names, ",") != "720p,480p,360p" {
		t.Errorf("ladder: %v", names)
	}

	only := videoRungs(&postgres.MediaAsset{StorageKey: "orig", Width: &w})
	if len(only) != 1 || only[0].Name != "original" || only[0].ObjectKey != "orig" || only[0].Width == nil {
		t.Errorf("no ladder → original: %+v", only)
	}
}

func TestLiveDubRungsBestFirst(t *testing.T) {
	media := &postgres.MediaAsset{Variants: []postgres.MediaVariant{
		{Name: "dub_hi_360p"}, {Name: "dub_hi_720p"}, {Name: "dub_ta_720p"}, {Name: "720p"},
	}}
	if got := strings.Join(liveDubRungs(media, "hi"), ","); got != "720p,360p" {
		t.Errorf("hi: %s", got)
	}
	if got := liveDubRungs(media, "te"); len(got) != 0 {
		t.Errorf("te: %v", got)
	}
}

func TestDubVariantNameAndKeys(t *testing.T) {
	if DubVariantName("pt-br", "720p") != "dub_pt-br_720p" {
		t.Errorf("variant name: %s", DubVariantName("pt-br", "720p"))
	}
	uid, mid := uuid.New(), uuid.New()
	if got := audioTrackSourceKey(uid, mid, "hi"); got != fmt.Sprintf("user/%s/%s/dub/hi/source", uid, mid) {
		t.Errorf("source key: %s", got)
	}
	if got := audioTrackRungKey(uid, mid, "hi", "480p"); got != fmt.Sprintf("user/%s/%s/dub/hi/480p", uid, mid) {
		t.Errorf("rung key: %s", got)
	}
}

func TestLanguageNormalisationAndLabel(t *testing.T) {
	lang, err := normalizeAudioLanguage(" pt-BR ")
	if err != nil || lang != "pt-br" {
		t.Errorf("pt-BR: %q %v", lang, err)
	}
	if _, err := normalizeAudioLanguage("en_US"); !errors.Is(err, ErrAudioTrackInvalid) {
		t.Errorf("underscore must be rejected: %v", err)
	}
	if LanguageLabel("hi") != "Hindi" || LanguageLabel("xx") != "xx" {
		t.Errorf("labels: %s %s", LanguageLabel("hi"), LanguageLabel("xx"))
	}
	label, err := normalizeAudioLabel("", "ta")
	if err != nil || label != "Tamil" {
		t.Errorf("default label: %q %v", label, err)
	}
}

// ── the worker's process() with fakes ───────────────────────────────────

type workerStore struct {
	media    *postgres.MediaAsset
	variants []postgres.MediaVariant
	srcKey   string
	srcToken *string
}

func (s *workerStore) GetMediaWithVariants(context.Context, uuid.UUID) (*postgres.MediaAsset, error) {
	cp := *s.media
	return &cp, nil
}
func (s *workerStore) InsertVariants(_ context.Context, vs []postgres.MediaVariant) error {
	s.variants = append(s.variants, vs...)
	return nil
}
func (s *workerStore) ReplaceMediaAudioTrack(context.Context, *postgres.MediaAudioTrack) ([]string, error) {
	return nil, nil
}
func (s *workerStore) ListMediaAudioTracks(context.Context, uuid.UUID) ([]postgres.MediaAudioTrack, error) {
	return nil, nil
}
func (s *workerStore) CountMediaAudioTracks(context.Context, uuid.UUID) (int, error) { return 0, nil }
func (s *workerStore) GetMediaAudioTrack(context.Context, uuid.UUID, uuid.UUID) (*postgres.MediaAudioTrack, error) {
	return nil, postgres.ErrAudioTrackNotFound
}
func (s *workerStore) DeleteMediaAudioTrack(context.Context, uuid.UUID, uuid.UUID) ([]string, error) {
	return nil, nil
}
func (s *workerStore) ClaimMediaAudioTracks(context.Context, time.Duration, int) ([]postgres.MediaAudioTrack, error) {
	return nil, nil
}
func (s *workerStore) SetMediaAudioTrackSourceKey(_ context.Context, _ uuid.UUID, key string, token *string) error {
	s.srcKey, s.srcToken = key, token
	return nil
}
func (s *workerStore) CompleteMediaAudioTrack(context.Context, uuid.UUID, []string, *string) error {
	return nil
}
func (s *workerStore) FailMediaAudioTrack(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (s *workerStore) ReleaseMediaAudioTrack(context.Context, uuid.UUID, string, *string) error {
	return nil
}
func (s *workerStore) RequeueMediaAudioTrack(context.Context, uuid.UUID) error { return nil }

type workerBlobs struct{ objects map[string][]byte }

func (b *workerBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	d, ok := b.objects[key]
	if !ok {
		return nil, fmt.Errorf("no object %s", key)
	}
	return d, nil
}
func (b *workerBlobs) UploadObject(_ context.Context, key string, data []byte, _ string) error {
	b.objects[key] = data
	return nil
}
func (b *workerBlobs) DeleteObject(_ context.Context, key string) error {
	delete(b.objects, key)
	return nil
}

func newWorkerFixture(t *testing.T) (*AudioTracks, *workerStore, *workerBlobs, *postgres.MediaAsset) {
	t.Helper()
	uid, mid := uuid.New(), uuid.New()
	w720, h720, w360, h360, dur := 720, 1280, 360, 640, 12_000
	media := &postgres.MediaAsset{
		ID: mid, UploaderID: uid, FileType: "video", ProcessingStatus: "ready", DurationMs: &dur,
		StorageKey: fmt.Sprintf("user/%s/%s/original", uid, mid),
		Variants: []postgres.MediaVariant{
			{MediaAssetID: mid, Name: "360p", Mime: "video/mp4", ObjectKey: "v/360", Width: &w360, Height: &h360},
			{MediaAssetID: mid, Name: "720p", Mime: "video/mp4", ObjectKey: "v/720", Width: &w720, Height: &h720},
			{MediaAssetID: mid, Name: "thumb_150", Mime: "image/jpeg", ObjectKey: "v/thumb"},
		},
	}
	store := &workerStore{media: media}
	blobs := &workerBlobs{objects: map[string][]byte{
		"v/360": []byte("VIDEO360"), "v/720": []byte("VIDEO720"),
	}}
	a := NewAudioTracks(store, blobs, func(context.Context, uuid.UUID, uuid.UUID) error { return nil }, nil).
		WithMux(func(_ context.Context, videoPath, audioPath, outPath string) error {
			v, _ := os.ReadFile(videoPath)
			s, _ := os.ReadFile(audioPath)
			return os.WriteFile(outPath, []byte("MUX("+string(v)+"+"+string(s)+")"), 0o600)
		}).
		WithAudioExtractor(func(_ context.Context, videoPath, outDir string) (string, error) {
			p := outDir + "/extracted.m4a"
			return p, os.WriteFile(p, []byte("EXTRACTED"), 0o600)
		})
	return a, store, blobs, media
}

func TestProcessUploadedTrackMuxesEveryRungUnderTheVideoPrefix(t *testing.T) {
	a, store, blobs, media := newWorkerFixture(t)
	srcKey := audioTrackSourceKey(media.UploaderID, media.ID, "hi")
	blobs.objects[srcKey] = []byte("HINDI")
	token := "tok"
	track := &postgres.MediaAudioTrack{ID: uuid.New(), MediaAssetID: media.ID, Language: "hi",
		Source: "uploaded", SourceKey: &srcKey, Status: "processing", ClaimToken: &token}

	rungs, err := a.process(context.Background(), track)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if strings.Join(rungs, ",") != "720p,360p" {
		t.Errorf("rungs best-first: %v", rungs)
	}
	for _, rung := range []string{"720p", "360p"} {
		key := fmt.Sprintf("user/%s/%s/dub/hi/%s", media.UploaderID, media.ID, rung)
		want := "MUX(VIDEO" + strings.TrimSuffix(rung, "p") + "+HINDI)"
		if got := string(blobs.objects[key]); got != want {
			t.Errorf("object %s: %q want %q", key, got, want)
		}
	}
	if len(store.variants) != 2 {
		t.Fatalf("variants: %+v", store.variants)
	}
	v := store.variants[0]
	if v.Name != "dub_hi_720p" || v.Mime != "video/mp4" || v.Width == nil || *v.Width != 720 ||
		v.ObjectKey != fmt.Sprintf("user/%s/%s/dub/hi/720p", media.UploaderID, media.ID) || v.SizeBytes == nil || *v.SizeBytes == 0 {
		t.Errorf("720p variant row: %+v", v)
	}
	if store.variants[1].Name != "dub_hi_360p" {
		t.Errorf("360p variant row: %+v", store.variants[1])
	}
	if store.srcKey != "" {
		t.Errorf("an uploaded track must not rewrite its source key")
	}
}

func TestProcessGeneratedTrackRunsTheDubberThenMuxes(t *testing.T) {
	a, store, blobs, media := newWorkerFixture(t)
	a.WithDubber(dubbing.StubDubber{})
	token := "tok"
	src := "en"
	track := &postgres.MediaAudioTrack{ID: uuid.New(), MediaAssetID: media.ID, Language: "es",
		Source: "generated", SourceLanguage: &src, Status: "processing", ClaimToken: &token}

	rungs, err := a.process(context.Background(), track)
	if err != nil {
		t.Fatalf("process: %v", err)
	}
	if len(rungs) != 2 {
		t.Errorf("rungs: %v", rungs)
	}
	wantSrc := audioTrackSourceKey(media.UploaderID, media.ID, "es")
	if string(blobs.objects[wantSrc]) != "EXTRACTED" {
		t.Errorf("generated source must be stored at %s (stub copies the extracted audio): %v", wantSrc, blobs.objects)
	}
	if store.srcKey != wantSrc || store.srcToken == nil || *store.srcToken != "tok" {
		t.Errorf("source key must be recorded under the claim token: %q %v", store.srcKey, store.srcToken)
	}
	if got := string(blobs.objects[fmt.Sprintf("user/%s/%s/dub/es/720p", media.UploaderID, media.ID)]); got != "MUX(VIDEO720+EXTRACTED)" {
		t.Errorf("720p dub: %q", got)
	}
}

func TestProcessGeneratedTrackWithoutDubberFails(t *testing.T) {
	a, _, _, media := newWorkerFixture(t)
	track := &postgres.MediaAudioTrack{ID: uuid.New(), MediaAssetID: media.ID, Language: "es", Source: "generated", Status: "processing"}
	if _, err := a.process(context.Background(), track); !errors.Is(err, ErrDubbingUnavailable) {
		t.Errorf("want ErrDubbingUnavailable, got %v", err)
	}
}

func TestProcessUsesTheOriginalWhenThereIsNoLadder(t *testing.T) {
	a, store, blobs, media := newWorkerFixture(t)
	media.Variants = nil
	blobs.objects[media.StorageKey] = []byte("ORIG")
	srcKey := audioTrackSourceKey(media.UploaderID, media.ID, "hi")
	blobs.objects[srcKey] = []byte("HINDI")
	track := &postgres.MediaAudioTrack{ID: uuid.New(), MediaAssetID: media.ID, Language: "hi", Source: "uploaded", SourceKey: &srcKey, Status: "processing"}
	rungs, err := a.process(context.Background(), track)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(rungs, ",") != "original" || len(store.variants) != 1 || store.variants[0].Name != "dub_hi_original" {
		t.Errorf("no ladder: rungs %v variants %+v", rungs, store.variants)
	}
}
