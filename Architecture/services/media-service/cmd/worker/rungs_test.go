package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/google/uuid"
)

// ── fakes ─────────────────────────────────────────────────────────────

type memBlobs struct {
	mu        sync.Mutex
	objs      map[string][]byte
	failPut   func(key string) error
	putOrder  []string
	statCalls int
}

func newMemBlobs() *memBlobs { return &memBlobs{objs: map[string][]byte{}} }

func (b *memBlobs) put(key string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.failPut != nil {
		if err := b.failPut(key); err != nil {
			return err
		}
	}
	b.objs[key] = data
	b.putOrder = append(b.putOrder, key)
	return nil
}
func (b *memBlobs) UploadFile(_ context.Context, key, path, _ string) (int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return int64(len(data)), b.put(key, data)
}
func (b *memBlobs) UploadObject(_ context.Context, key string, data []byte, _ string) error {
	return b.put(key, data)
}
func (b *memBlobs) DownloadObject(_ context.Context, key string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	d, ok := b.objs[key]
	if !ok {
		return nil, blob.ErrObjectNotFound
	}
	return d, nil
}
func (b *memBlobs) StatObject(_ context.Context, key string) (blob.ObjectInfo, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.statCalls++
	d, ok := b.objs[key]
	if !ok {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(d))}, nil
}

type fakeJobs struct {
	mu       sync.Mutex
	statuses map[uuid.UUID]string
	msgs     map[uuid.UUID]string
}

func (j *fakeJobs) UpdateTranscodingJob(_ context.Context, id uuid.UUID, status string, _ *string, _ *int64, msg *string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.statuses == nil {
		j.statuses, j.msgs = map[uuid.UUID]string{}, map[uuid.UUID]string{}
	}
	j.statuses[id] = status
	if msg != nil {
		j.msgs[id] = *msg
	}
	return nil
}

type fakeEncoder struct {
	t        *testing.T
	encoded  []string
	hls      []string
	failRung string
	// onEncode runs before each encode (e.g. to flip a drain).
	onEncode func(name string)
}

func (e *fakeEncoder) Encode(_ context.Context, _, dir string, r processing.Rung, _ *processing.VideoMeta, _ bool) (processing.TranscodeOutput, error) {
	// lv-13: no earlier rendition may still be on the scratch volume.
	if left, _ := filepath.Glob(filepath.Join(dir, "*.mp4")); len(left) > 0 {
		e.t.Fatalf("encoding %s with earlier renditions still on scratch: %v", r.Name, left)
	}
	if e.onEncode != nil {
		e.onEncode(r.Name)
	}
	e.encoded = append(e.encoded, r.Name)
	if r.Name == e.failRung {
		return processing.TranscodeOutput{}, errors.New("ffmpeg 1080p: exit status 1: Conversion failed!")
	}
	if r.Kind == processing.RungThumb {
		p := filepath.Join(dir, "thumb_150.jpg")
		_ = os.WriteFile(p, []byte("jpeg"), 0o600)
		return processing.TranscodeOutput{Name: r.Name, FilePath: p, Width: 150, Height: 150, Mime: "image/jpeg"}, nil
	}
	p := filepath.Join(dir, r.Name+".mp4")
	_ = os.WriteFile(p, []byte("mp4-"+r.Name), 0o600)
	return processing.TranscodeOutput{Name: r.Name, FilePath: p, Height: r.Height, Mime: "video/mp4"}, nil
}

func (e *fakeEncoder) HLS(_ context.Context, _, rendition, dir string, _ processing.HLSPlan, v processing.HLSVariant) (processing.HLSRungOutput, error) {
	e.hls = append(e.hls, v.Quality+"<"+filepath.Base(rendition))
	pl := filepath.Join(dir, v.Quality+".m3u8")
	seg := filepath.Join(dir, v.Quality+"_000.ts")
	_ = os.WriteFile(pl, []byte("#EXTM3U"), 0o600)
	_ = os.WriteFile(seg, []byte("ts"), 0o600)
	return processing.HLSRungOutput{Quality: v.Quality, Files: []string{pl, seg}, BandwidthBPS: 1000}, nil
}

func (e *fakeEncoder) Storyboard(_ context.Context, _, dir string, _ float64) ([]processing.TranscodeOutput, error) {
	e.encoded = append(e.encoded, storyboardUnit)
	p := filepath.Join(dir, "storyboard.jpg")
	_ = os.WriteFile(p, []byte("sb"), 0o600)
	return []processing.TranscodeOutput{{Name: "storyboard_jpg", ObjectName: "storyboard.jpg", FilePath: p, Mime: "image/jpeg"}}, nil
}

func testRungRun(t *testing.T, height int) *rungRun {
	id := uuid.New()
	return &rungRun{
		mediaID: id, baseKey: "user/u/" + id.String(), generation: 2, sourceETag: "etag-1",
		input: "/in/original", scratch: t.TempDir(), meta: &processing.VideoMeta{Height: height, Width: height * 16 / 9, DurationFloat: 3600},
		jobIDs: map[string]uuid.UUID{},
	}
}

func fresh(r *rungRun, t *testing.T) *rungRun {
	c := *r
	c.scratch = t.TempDir()
	return &c
}

// ── lv-5: finished rungs are durable and reused ──────────────────────

func TestFirstRunEncodesEveryUnitAndRecordsEach(t *testing.T) {
	r := testRungRun(t, 1080)
	b, enc := newMemBlobs(), &fakeEncoder{t: t}
	res, err := runRungs(context.Background(), r, b, &fakeJobs{}, enc)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"thumb_150", "360p", "480p", "720p", "1080p", storyboardUnit}
	if !slices.Equal(enc.encoded, want) {
		t.Fatalf("encoded %v, want %v", enc.encoded, want)
	}
	// Each HLS rung is a copy of its own rendition.
	if !slices.Equal(enc.hls, []string{"360p<360p.mp4", "720p<720p.mp4", "1080p<1080p.mp4"}) {
		t.Fatalf("hls %v", enc.hls)
	}
	if len(res.hls) != 3 || len(res.variants) != 6 {
		t.Fatalf("hls=%v variants=%d", res.hls, len(res.variants))
	}
	// A marker is written only after the unit's uploads.
	idx := func(k string) int { return slices.Index(b.putOrder, k) }
	for _, u := range []string{"360p", "720p"} {
		m, mp4, pl := idx(r.markerKey(u)), idx(r.baseKey+"/"+u), idx(r.hlsPrefix()+u+".m3u8")
		if m < 0 || mp4 < 0 || pl < 0 || m < mp4 || m < pl {
			t.Fatalf("%s marker at %d, mp4 at %d, playlist at %d: marker must come last", u, m, mp4, pl)
		}
	}
	// lv-13: nothing is left on scratch.
	if left, _ := filepath.Glob(filepath.Join(r.scratch, "*.mp4")); len(left) > 0 {
		t.Fatalf("renditions left on scratch: %v", left)
	}
}

// reels-34 and lv-5: a re-run (redelivery after a rollout, or the retry
// after a scanner outage) encodes nothing that an earlier run finished.
func TestReRunReusesEveryFinishedUnit(t *testing.T) {
	r := testRungRun(t, 1080)
	b := newMemBlobs()
	first, err := runRungs(context.Background(), r, b, &fakeJobs{}, &fakeEncoder{t: t})
	if err != nil {
		t.Fatal(err)
	}
	enc := &fakeEncoder{t: t}
	jobs := &fakeJobs{}
	r2 := fresh(r, t)
	r2.jobIDs = map[string]uuid.UUID{"720p": uuid.New()}
	second, err := runRungs(context.Background(), r2, b, jobs, enc)
	if err != nil {
		t.Fatal(err)
	}
	if len(enc.encoded) != 0 || len(enc.hls) != 0 {
		t.Fatalf("re-run encoded %v / hls %v, want nothing", enc.encoded, enc.hls)
	}
	if len(second.variants) != len(first.variants) || len(second.hls) != len(first.hls) || len(second.hlsKeys) != len(first.hlsKeys) {
		t.Fatalf("re-run result differs: %d/%d variants, %v/%v hls", len(second.variants), len(first.variants), second.hls, first.hls)
	}
	if jobs.statuses[r2.jobIDs["720p"]] != "completed" {
		t.Fatalf("reused rung's job row = %q, want completed", jobs.statuses[r2.jobIDs["720p"]])
	}
}

// An interrupted run resumes from the first missing unit.
func TestInterruptedRunResumesFromTheFirstMissingRung(t *testing.T) {
	r := testRungRun(t, 1080)
	b := newMemBlobs()
	b.failPut = func(key string) error {
		if strings.HasSuffix(key, "/720p") {
			return errors.New("connection reset")
		}
		return nil
	}
	if _, err := runRungs(context.Background(), r, b, &fakeJobs{}, &fakeEncoder{t: t}); err == nil {
		t.Fatal("upload failure not returned")
	}
	b.failPut = nil
	enc := &fakeEncoder{t: t}
	if _, err := runRungs(context.Background(), fresh(r, t), b, &fakeJobs{}, enc); err != nil {
		t.Fatal(err)
	}
	if want := []string{"720p", "1080p", storyboardUnit}; !slices.Equal(enc.encoded, want) {
		t.Fatalf("resumed run encoded %v, want %v", enc.encoded, want)
	}
}

// The marker is the proof, not the object: an object with no marker (an
// upload cut off before its marker), a marker whose object is gone or the
// wrong size, or a marker for other source bytes or another generation, is
// encoded again.
func TestOnlyAValidMarkerAllowsReuse(t *testing.T) {
	cases := map[string]func(r *rungRun, b *memBlobs){
		"object without marker": func(r *rungRun, b *memBlobs) { delete(b.objs, r.markerKey("360p")) },
		"object missing":        func(r *rungRun, b *memBlobs) { delete(b.objs, r.baseKey+"/360p") },
		"object truncated":      func(r *rungRun, b *memBlobs) { b.objs[r.baseKey+"/360p"] = []byte("m") },
		"playlist missing":      func(r *rungRun, b *memBlobs) { delete(b.objs, r.hlsPrefix()+"360p.m3u8") },
		"other source bytes":    func(r *rungRun, _ *memBlobs) { r.sourceETag = "etag-2" },
		"other generation":      func(r *rungRun, _ *memBlobs) { r.generation = 3 },
		"corrupt marker":        func(r *rungRun, b *memBlobs) { b.objs[r.markerKey("360p")] = []byte("{") },
	}
	for name, spoil := range cases {
		t.Run(name, func(t *testing.T) {
			r := testRungRun(t, 360)
			b := newMemBlobs()
			if _, err := runRungs(context.Background(), r, b, &fakeJobs{}, &fakeEncoder{t: t}); err != nil {
				t.Fatal(err)
			}
			r2 := fresh(r, t)
			spoil(r2, b)
			enc := &fakeEncoder{t: t}
			if _, err := runRungs(context.Background(), r2, b, &fakeJobs{}, enc); err != nil {
				t.Fatal(err)
			}
			if !slices.Contains(enc.encoded, "360p") {
				t.Fatalf("360p reused after %s (encoded %v)", name, enc.encoded)
			}
		})
	}
}

// ── lv-5: SIGTERM finishes the rung in hand and starts no other ──────

func TestDrainStopsBeforeTheNextRungAndKeepsTheFinishedOnes(t *testing.T) {
	r := testRungRun(t, 1080)
	b := newMemBlobs()
	draining := false
	r.stopping = func() bool { return draining }
	enc := &fakeEncoder{t: t, onEncode: func(name string) {
		if name == "480p" {
			draining = true // SIGTERM while 480p encodes
		}
	}}
	_, err := runRungs(context.Background(), r, b, &fakeJobs{}, enc)
	if !errors.Is(err, errWorkerDraining) {
		t.Fatalf("err = %v, want errWorkerDraining", err)
	}
	if !slices.Equal(enc.encoded, []string{"thumb_150", "360p", "480p"}) {
		t.Fatalf("encoded %v: the rung in hand must finish and no other start", enc.encoded)
	}
	if _, ok := b.objs[r.markerKey("480p")]; !ok {
		t.Fatal("the rung finished during the drain was not recorded")
	}
	enc2 := &fakeEncoder{t: t}
	next := fresh(r, t)
	next.stopping = nil // another worker
	if _, err := runRungs(context.Background(), next, b, &fakeJobs{}, enc2); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(enc2.encoded, []string{"720p", "1080p", storyboardUnit}) {
		t.Fatalf("next owner encoded %v, want to resume at 720p", enc2.encoded)
	}
}

// ── lv-15 / task 5: a failed rung is logged, counted and its row failed ─

func TestFailedRungIsRecordedNotDropped(t *testing.T) {
	r := testRungRun(t, 1080)
	r.jobIDs = map[string]uuid.UUID{"1080p": uuid.New(), "720p": uuid.New()}
	jobs := &fakeJobs{}
	before := counterValue(rungFailures.WithLabelValues("1080p"))
	enc := &fakeEncoder{t: t, failRung: "1080p"}
	res, err := runRungs(context.Background(), r, newMemBlobs(), jobs, enc)
	if err != nil {
		t.Fatalf("err = %v: the video still goes ready with fewer rungs", err)
	}
	id := r.jobIDs["1080p"]
	if jobs.statuses[id] != "failed" || !strings.Contains(jobs.msgs[id], "Conversion failed") {
		t.Fatalf("1080p row = %q %q, want failed with the ffmpeg line", jobs.statuses[id], jobs.msgs[id])
	}
	if jobs.statuses[r.jobIDs["720p"]] != "completed" {
		t.Fatalf("720p row = %q", jobs.statuses[r.jobIDs["720p"]])
	}
	if got := counterValue(rungFailures.WithLabelValues("1080p")) - before; got != 1 {
		t.Fatalf("rung failure counter moved %v, want 1", got)
	}
	if !slices.Equal(res.failed, []string{"1080p"}) {
		t.Fatalf("failed = %v", res.failed)
	}
	// The 1080p HLS rung falls back to an encode from the original.
	if !slices.Contains(enc.hls, "1080p<.") {
		t.Fatalf("hls %v: 1080p rung not encoded from the original", enc.hls)
	}
}

func TestRungMarkerKeysStayUnderTheAsset(t *testing.T) {
	r := testRungRun(t, 360)
	if k := r.markerKey("360p"); !strings.HasPrefix(k, r.baseKey+"/rungs/") || strings.Contains(k, "/hls/") {
		t.Fatalf("marker key %s", k)
	}
	_ = fmt.Sprint()
}
