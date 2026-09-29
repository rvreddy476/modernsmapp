package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/fingerprint"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
)

type fakeBlobs struct {
	objects map[string]string
	opened  []string
}

func (f *fakeBlobs) OpenObject(_ context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error) {
	body, ok := f.objects[key]
	if !ok {
		return nil, blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	f.opened = append(f.opened, key)
	return io.NopCloser(strings.NewReader(body)), blob.ObjectInfo{Size: int64(len(body)), ETag: "etag-" + key}, nil
}

func (f *fakeBlobs) StatObject(_ context.Context, key string) (blob.ObjectInfo, error) {
	body, ok := f.objects[key]
	if !ok {
		return blob.ObjectInfo{}, blob.ErrObjectNotFound
	}
	return blob.ObjectInfo{Size: int64(len(body)), ETag: "etag-" + key}, nil
}

const masterPlaylist = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-STREAM-INF:BANDWIDTH=5000000,RESOLUTION=1920x1080
1080p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=700000,RESOLUTION=640x360
360p.m3u8
#EXT-X-STREAM-INF:BANDWIDTH=2500000,RESOLUTION=1280x720
720p.m3u8
`

const rungPlaylist = `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:0
#EXT-X-PLAYLIST-TYPE:VOD
#EXTINF:6.000000,
360p_000.ts
#EXTINF:6.000000,
360p_001.ts
#EXTINF:2.500000,
360p_002.ts
#EXT-X-ENDLIST
`

func TestParseHLSMasterPicksLowestHeight(t *testing.T) {
	rungs, err := parseHLSMaster(strings.NewReader(masterPlaylist))
	if err != nil {
		t.Fatal(err)
	}
	if len(rungs) != 3 {
		t.Fatalf("%d rungs", len(rungs))
	}
	if r := lowestRung(rungs); r.URI != "360p.m3u8" || r.Height != 360 {
		t.Fatalf("lowest rung %+v", r)
	}
	// Ties on height break by bandwidth; a lower BANDWIDTH on a taller rung
	// does not win (selection is by resolution, plan 12.1).
	tie := []hlsRung{{URI: "a.m3u8", Bandwidth: 900, Height: 360}, {URI: "b.m3u8", Bandwidth: 800, Height: 360}, {URI: "c.m3u8", Bandwidth: 100, Height: 720}}
	if r := lowestRung(tie); r.URI != "b.m3u8" {
		t.Fatalf("tie-break %+v", r)
	}
}

func TestParseHLSMasterIsStrict(t *testing.T) {
	bad := map[string]string{
		"no header":       "#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=640x360\n360p.m3u8\n",
		"no resolution":   "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\n360p.m3u8\n",
		"no bandwidth":    "#EXTM3U\n#EXT-X-STREAM-INF:RESOLUTION=640x360\n360p.m3u8\n",
		"absolute uri":    "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=640x360\nhttps://x/360p.m3u8\n",
		"uri without inf": "#EXTM3U\n360p.m3u8\n",
		"inf without uri": "#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1,RESOLUTION=640x360\n",
		"empty":           "#EXTM3U\n",
	}
	for name, body := range bad {
		if _, err := parseHLSMaster(strings.NewReader(body)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestParseHLSMediaSegments(t *testing.T) {
	segs, err := parseHLSMedia(strings.NewReader(rungPlaylist))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 3 || segs[0].Key != "360p_000.ts" || segs[2].DurationMs != 2500 {
		t.Fatalf("segments %+v", segs)
	}
	if _, err := parseHLSMedia(strings.NewReader("#EXTM3U\n360p_000.ts\n")); err == nil {
		t.Fatal("segment without EXTINF accepted")
	}
	if _, err := parseHLSMedia(strings.NewReader("#EXTM3U\n#EXTINF:6,\n/abs/360p_000.ts\n")); err == nil {
		t.Fatal("absolute segment accepted")
	}
}

func h(v int) *int { return &v }

// T15-1 / T15-3: the contract prefers the HLS rung, then the smallest MP4,
// then the original under the cap, else skipped.
func TestSelectInputContract(t *testing.T) {
	ctx := context.Background()
	base := "user/u/m"
	blobs := &fakeBlobs{objects: map[string]string{
		base + "/hls/master.m3u8": masterPlaylist,
		base + "/hls/360p.m3u8":   rungPlaylist,
		base + "/hls/360p_000.ts": "aaa", base + "/hls/360p_001.ts": "bbb", base + "/hls/360p_002.ts": "cc",
		base + "/original": strings.Repeat("x", 1000),
	}}
	caps := inputCaps{OriginalMaxBytes: 2000, OriginalMaxMs: 3 * 3600 * 1000}
	variants := []postgres.MediaVariant{
		{Name: "720p", Mime: "video/mp4", Height: h(720), ObjectKey: base + "/720p"},
		{Name: "480p", Mime: "video/mp4", Height: h(480), ObjectKey: base + "/480p"},
		{Name: "thumb_150", Mime: "image/jpeg", Height: h(150), ObjectKey: base + "/thumb_150"},
	}
	st := &postgres.MediaGenerationState{HLSMasterKey: base + "/hls/master.m3u8", StorageKey: base + "/original", DurationMs: 14_500}

	plan, err := selectInput(ctx, blobs, st, variants, caps)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Kind != inputHLSRung || plan.Ref != "360p" || len(plan.Segments) != 3 || plan.Segments[1].Key != base+"/hls/360p_001.ts" {
		t.Fatalf("plan %+v", plan)
	}

	// No master: the smallest MP4 by height, never the thumbnail.
	st.HLSMasterKey = ""
	plan, err = selectInput(ctx, blobs, st, variants, caps)
	if err != nil || plan.Kind != inputMP4Variant || plan.Ref != "480p" {
		t.Fatalf("mp4 fallback: %+v %v", plan, err)
	}

	// No variants: the original, under the caps.
	plan, err = selectInput(ctx, blobs, st, nil, caps)
	if err != nil || plan.Kind != inputOriginal || plan.Object != base+"/original" {
		t.Fatalf("original fallback: %+v %v", plan, err)
	}
	// Over the byte cap → skipped with the reason.
	_, err = selectInput(ctx, blobs, st, nil, inputCaps{OriginalMaxBytes: 500})
	var skip errSkipInput
	if !errors.As(err, &skip) || skip.reason != skipOriginalTooLarge {
		t.Fatalf("over byte cap: %v", err)
	}
	// Over the duration cap → skipped.
	st.DurationMs = 4 * 3600 * 1000
	_, err = selectInput(ctx, blobs, st, nil, caps)
	if !errors.As(err, &skip) || skip.reason != skipOriginalTooLarge {
		t.Fatalf("over duration cap: %v", err)
	}
	// Nothing at all → skipped no_input.
	st.StorageKey = ""
	_, err = selectInput(ctx, blobs, st, nil, caps)
	if !errors.As(err, &skip) || skip.reason != skipNoInput {
		t.Fatalf("no input: %v", err)
	}
	// A master that does not parse is an error, not a silent fallback.
	blobs.objects[base+"/hls/master.m3u8"] = "#EXTM3U\n"
	st.HLSMasterKey = base + "/hls/master.m3u8"
	if _, err := selectInput(ctx, blobs, st, variants, caps); err == nil {
		t.Fatal("bad master accepted")
	}
}

// T15-4: the HLS path streams segments through the blob client in
// playlist order and records their ETags; no URL is involved anywhere.
func TestExtractInputStreamsSegmentsInOrder(t *testing.T) {
	base := "user/u/m"
	blobs := &fakeBlobs{objects: map[string]string{
		base + "/hls/master.m3u8": masterPlaylist,
		base + "/hls/360p_000.ts": "AAA", base + "/hls/360p_001.ts": "BBB", base + "/hls/360p_002.ts": "CC",
	}}
	plan := &inputPlan{Kind: inputHLSRung, Ref: "360p", MasterKey: base + "/hls/master.m3u8", Segments: []hlsSegmentRef{
		{Key: base + "/hls/360p_000.ts", DurationMs: 6000}, {Key: base + "/hls/360p_001.ts", DurationMs: 6000}, {Key: base + "/hls/360p_002.ts", DurationMs: 2500}}}
	// A sink in place of ffmpeg: it reads the whole stream and reports the
	// bytes it saw, so the real extractInput's ordering and bookkeeping run.
	sink := &sinkExtractor{}
	var progress []int
	_, etags, err := extractInput(context.Background(), blobs, sink, plan, t.TempDir(), func(ms int) { progress = append(progress, ms) })
	if err != nil {
		t.Fatal(err)
	}
	if sink.format != "mpegts" || sink.streamed.String() != "AAABBBCC" {
		t.Fatalf("streamed %q as %q", sink.streamed.String(), sink.format)
	}
	if got := strings.Join(blobs.opened, ","); got != base+"/hls/360p_000.ts,"+base+"/hls/360p_001.ts,"+base+"/hls/360p_002.ts" {
		t.Fatalf("open order %s", got)
	}
	if len(progress) != 3 || progress[2] != 14_500 {
		t.Fatalf("progress %v", progress)
	}
	if got := blobs.opened; len(got) != 3 || got[0] != base+"/hls/360p_000.ts" || got[2] != base+"/hls/360p_002.ts" {
		t.Fatalf("open order %v", got)
	}
	for _, k := range []string{base + "/hls/master.m3u8", base + "/hls/360p_000.ts", base + "/hls/360p_002.ts"} {
		if etags[k] != "etag-"+k {
			t.Fatalf("etag of %s = %q", k, etags[k])
		}
	}
	for k := range etags {
		if strings.Contains(k, "://") || strings.Contains(k, "?") {
			t.Fatalf("a URL leaked into input_etags: %s", k)
		}
	}
}

// sinkExtractor stands in for ffmpeg: it drains what it is given.
type sinkExtractor struct {
	format   string
	streamed bytes.Buffer
	file     string
}

func (s *sinkExtractor) FromFile(_ context.Context, path string) (*fingerprint.Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	s.file = path
	s.streamed.Write(b)
	return &fingerprint.Result{MeasuredMs: len(b) * fingerprint.FrameMs}, nil
}

func (s *sinkExtractor) FromStream(_ context.Context, format string, in io.Reader) (*fingerprint.Result, error) {
	s.format = format
	n, err := io.Copy(&s.streamed, in)
	if err != nil {
		return nil, err
	}
	return &fingerprint.Result{MeasuredMs: int(n) * fingerprint.FrameMs}, nil
}

// The MP4 and original paths stream to a scratch file that is removed
// afterwards; the object's ETag is recorded.
func TestExtractInputFilePathsUseScratchAndCleanUp(t *testing.T) {
	blobs := &fakeBlobs{objects: map[string]string{"user/u/m/480p": "MP4BYTES"}}
	sink := &sinkExtractor{}
	scratch := t.TempDir()
	res, etags, err := extractInput(context.Background(), blobs, sink, &inputPlan{Kind: inputMP4Variant, Ref: "480p", Object: "user/u/m/480p"}, scratch, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sink.streamed.String() != "MP4BYTES" || res.MeasuredMs != 8*fingerprint.FrameMs {
		t.Fatalf("streamed %q, measured %d", sink.streamed.String(), res.MeasuredMs)
	}
	if !strings.HasPrefix(sink.file, scratch) {
		t.Fatalf("scratch file %s not under %s", sink.file, scratch)
	}
	if _, err := os.Stat(sink.file); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("scratch file not removed: %v", err)
	}
	if etags["user/u/m/480p"] != "etag-user/u/m/480p" {
		t.Fatalf("etags %v", etags)
	}
	// A segment that vanishes mid-stream fails the job rather than hashing
	// a shorter video.
	blobs = &fakeBlobs{objects: map[string]string{"a": "AAA"}}
	_, _, err = extractInput(context.Background(), blobs, &sinkExtractor{}, &inputPlan{Kind: inputHLSRung, Ref: "360p",
		Segments: []hlsSegmentRef{{Key: "a", DurationMs: 6000}, {Key: "missing", DurationMs: 6000}}}, scratch, nil)
	if err == nil {
		t.Fatal("a missing segment must fail the extraction")
	}
}
