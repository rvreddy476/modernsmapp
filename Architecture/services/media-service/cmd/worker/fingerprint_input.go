package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/atpost/media-service/internal/fingerprint"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
)

// The fingerprint input contract (plan 12.1), evaluated from one snapshot
// of the asset:
//
//  1. hls_rung — the lowest rung of the worker-generated master at
//     hls_master_key (lowest RESOLUTION height, ties by lowest BANDWIDTH),
//     its segments streamed in playlist order through the worker's own
//     blob client into ffmpeg's stdin. No presigned URL, ever.
//  2. mp4_variant — only with no hls_master_key (legacy): the smallest
//     video/mp4 variant by height, streamed to scratch disk.
//  3. original — only with neither: the original, under a size and
//     duration cap, streamed to scratch disk; otherwise skipped with
//     reason original_too_large.
//
// Every object key read is recorded with the ETag the store reported, so
// the stored fingerprint says exactly which bytes it hashed.

// fingerprintBlobs is what the input contract needs from the blob store.
type fingerprintBlobs interface {
	OpenObject(ctx context.Context, key string) (io.ReadCloser, blob.ObjectInfo, error)
	StatObject(ctx context.Context, key string) (blob.ObjectInfo, error)
}

// Input kinds (copyright_fingerprints.input_kind).
const (
	inputHLSRung    = "hls_rung"
	inputMP4Variant = "mp4_variant"
	inputOriginal   = "original"
)

// errSkipInput carries a skip reason for media_fingerprint_jobs.skip_reason.
type errSkipInput struct{ reason string }

func (e errSkipInput) Error() string { return "fingerprint input skipped: " + e.reason }

const (
	skipOriginalTooLarge = "original_too_large"
	skipNoInput          = "no_input"
)

// inputPlan is the chosen input.
type inputPlan struct {
	Kind string
	// Ref is the rung ("360p") or variant name; never a URL.
	Ref string
	// Segments (hls_rung): object keys in playlist order with their
	// EXTINF durations. Object (mp4_variant/original): one key.
	Segments  []hlsSegmentRef
	Object    string
	MasterKey string
}

type hlsSegmentRef struct {
	Key        string
	DurationMs int
}

// hlsRung is one entry of a master playlist.
type hlsRung struct {
	URI       string
	Bandwidth int
	Height    int
}

// parseHLSMaster reads a master playlist strictly: every variant needs a
// RESOLUTION and a URI line. Anything else is an error, not a guess.
func parseHLSMaster(r io.Reader) ([]hlsRung, error) {
	sc := bufio.NewScanner(r)
	var out []hlsRung
	var pending *hlsRung
	first := true
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if first {
			first = false
			if line != "#EXTM3U" {
				return nil, fmt.Errorf("master playlist does not start with #EXTM3U")
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			attrs := parseAttributes(strings.TrimPrefix(line, "#EXT-X-STREAM-INF:"))
			rung := hlsRung{}
			if bw, err := strconv.Atoi(attrs["BANDWIDTH"]); err == nil {
				rung.Bandwidth = bw
			} else {
				return nil, fmt.Errorf("variant without BANDWIDTH: %q", line)
			}
			res := attrs["RESOLUTION"]
			wh := strings.SplitN(res, "x", 2)
			if len(wh) != 2 {
				return nil, fmt.Errorf("variant without RESOLUTION: %q", line)
			}
			h, err := strconv.Atoi(wh[1])
			if err != nil || h <= 0 {
				return nil, fmt.Errorf("variant with bad RESOLUTION: %q", line)
			}
			rung.Height = h
			pending = &rung
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		default:
			if pending == nil {
				return nil, fmt.Errorf("URI %q without a preceding #EXT-X-STREAM-INF", line)
			}
			if strings.Contains(line, "://") || strings.HasPrefix(line, "/") {
				return nil, fmt.Errorf("variant URI %q is not relative", line)
			}
			pending.URI = line
			out = append(out, *pending)
			pending = nil
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if pending != nil {
		return nil, fmt.Errorf("variant %+v has no URI", *pending)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("master playlist lists no variants")
	}
	return out, nil
}

func parseAttributes(s string) map[string]string {
	out := map[string]string{}
	for _, kv := range strings.Split(s, ",") {
		if i := strings.IndexByte(kv, '='); i > 0 {
			out[strings.TrimSpace(kv[:i])] = strings.Trim(strings.TrimSpace(kv[i+1:]), `"`)
		}
	}
	return out
}

// lowestRung picks the rung with the lowest height, breaking ties by the
// lowest bandwidth. Selecting by resolution keeps the choice stable if
// BANDWIDTH starts being derived from measured segment peaks.
func lowestRung(rungs []hlsRung) hlsRung {
	sorted := append([]hlsRung(nil), rungs...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Height != sorted[j].Height {
			return sorted[i].Height < sorted[j].Height
		}
		return sorted[i].Bandwidth < sorted[j].Bandwidth
	})
	return sorted[0]
}

// parseHLSMedia reads a media playlist's segments and durations.
func parseHLSMedia(r io.Reader) ([]hlsSegmentRef, error) {
	sc := bufio.NewScanner(r)
	var out []hlsSegmentRef
	pendingMs, havePending := 0, false
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			d, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("bad EXTINF %q", line)
			}
			pendingMs, havePending = int(d*1000+0.5), true
		case line == "" || strings.HasPrefix(line, "#"):
			continue
		default:
			if !havePending {
				return nil, fmt.Errorf("segment %q without #EXTINF", line)
			}
			if strings.Contains(line, "://") || strings.HasPrefix(line, "/") {
				return nil, fmt.Errorf("segment URI %q is not relative", line)
			}
			out = append(out, hlsSegmentRef{Key: line, DurationMs: pendingMs})
			havePending = false
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("media playlist has no segments")
	}
	return out, nil
}

// inputCaps bound the original fallback.
type inputCaps struct {
	OriginalMaxBytes int64
	OriginalMaxMs    int
}

// selectInput evaluates the contract. Playlists are read through the blob
// client; nothing is buffered but the playlists themselves.
func selectInput(ctx context.Context, blobs fingerprintBlobs, st *postgres.MediaGenerationState, variants []postgres.MediaVariant, caps inputCaps) (*inputPlan, error) {
	if st.HLSMasterKey != "" {
		rc, _, err := blobs.OpenObject(ctx, st.HLSMasterKey)
		if err != nil {
			return nil, fmt.Errorf("open HLS master: %w", err)
		}
		rungs, err := parseHLSMaster(io.LimitReader(rc, 64<<10))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("parse HLS master %s: %w", st.HLSMasterKey, err)
		}
		rung := lowestRung(rungs)
		dir := path.Dir(st.HLSMasterKey)
		playlistKey := path.Join(dir, rung.URI)
		rc, _, err = blobs.OpenObject(ctx, playlistKey)
		if err != nil {
			return nil, fmt.Errorf("open HLS rung playlist: %w", err)
		}
		segs, err := parseHLSMedia(io.LimitReader(rc, 4<<20))
		_ = rc.Close()
		if err != nil {
			return nil, fmt.Errorf("parse HLS rung %s: %w", playlistKey, err)
		}
		segDir := path.Dir(playlistKey)
		for i := range segs {
			segs[i].Key = path.Join(segDir, segs[i].Key)
		}
		return &inputPlan{Kind: inputHLSRung, Ref: strings.TrimSuffix(rung.URI, path.Ext(rung.URI)),
			Segments: segs, MasterKey: st.HLSMasterKey}, nil
	}

	// Fallback A: the smallest MP4 variant by height.
	var best *postgres.MediaVariant
	for i := range variants {
		v := &variants[i]
		if v.Mime != "video/mp4" || v.ObjectKey == "" {
			continue
		}
		if best == nil || heightOf(v) < heightOf(best) {
			best = v
		}
	}
	if best != nil {
		return &inputPlan{Kind: inputMP4Variant, Ref: best.Name, Object: best.ObjectKey}, nil
	}

	// Fallback B: the original, under the caps.
	if st.StorageKey == "" {
		return nil, errSkipInput{skipNoInput}
	}
	if caps.OriginalMaxMs > 0 && st.DurationMs > caps.OriginalMaxMs {
		return nil, errSkipInput{skipOriginalTooLarge}
	}
	info, err := blobs.StatObject(ctx, st.StorageKey)
	if err != nil {
		return nil, fmt.Errorf("stat original: %w", err)
	}
	if caps.OriginalMaxBytes > 0 && info.Size > caps.OriginalMaxBytes {
		return nil, errSkipInput{skipOriginalTooLarge}
	}
	return &inputPlan{Kind: inputOriginal, Ref: "original", Object: st.StorageKey}, nil
}

func heightOf(v *postgres.MediaVariant) int {
	if v.Height == nil {
		return 1 << 30
	}
	return *v.Height
}

// frameExtractor is the extractor surface (fingerprint.Extractor, or a
// sink in tests).
type frameExtractor interface {
	FromFile(ctx context.Context, path string) (*fingerprint.Result, error)
	FromStream(ctx context.Context, format string, in io.Reader) (*fingerprint.Result, error)
}

// extractInput runs the extractor over the plan. progress is called with
// the milliseconds streamed so far (HLS only). Returns the result and the
// ETags of every object read.
func extractInput(ctx context.Context, blobs fingerprintBlobs, ex frameExtractor, plan *inputPlan, scratchDir string, progress func(ms int)) (*fingerprint.Result, map[string]string, error) {
	etags := map[string]string{}
	switch plan.Kind {
	case inputHLSRung:
		if plan.MasterKey != "" {
			if info, err := blobs.StatObject(ctx, plan.MasterKey); err == nil {
				etags[plan.MasterKey] = info.ETag
			}
		}
		pr, pw := io.Pipe()
		streamErr := make(chan error, 1)
		go func() {
			defer close(streamErr)
			streamed := 0
			for _, seg := range plan.Segments {
				rc, info, err := blobs.OpenObject(ctx, seg.Key)
				if err != nil {
					pw.CloseWithError(err)
					streamErr <- err
					return
				}
				etags[seg.Key] = info.ETag
				_, err = io.Copy(pw, rc)
				_ = rc.Close()
				if err != nil {
					pw.CloseWithError(err)
					streamErr <- err
					return
				}
				streamed += seg.DurationMs
				if progress != nil {
					progress(streamed)
				}
			}
			_ = pw.Close()
		}()
		res, err := ex.FromStream(ctx, "mpegts", pr)
		_ = pr.Close() // unblocks the streamer if ffmpeg stopped early
		if serr := <-streamErr; serr != nil && err == nil {
			return nil, nil, fmt.Errorf("stream HLS segments: %w", serr)
		}
		if err != nil {
			return nil, nil, err
		}
		return res, etags, nil

	case inputMP4Variant, inputOriginal:
		rc, info, err := blobs.OpenObject(ctx, plan.Object)
		if err != nil {
			return nil, nil, fmt.Errorf("open %s: %w", plan.Kind, err)
		}
		etags[plan.Object] = info.ETag
		f, err := os.CreateTemp(scratchDir, "fingerprint-*")
		if err != nil {
			_ = rc.Close()
			return nil, nil, fmt.Errorf("scratch file: %w", err)
		}
		tmp := f.Name()
		defer os.Remove(tmp)
		_, err = io.Copy(f, rc)
		_ = rc.Close()
		if cerr := f.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return nil, nil, fmt.Errorf("stream %s to scratch: %w", plan.Kind, err)
		}
		res, err := ex.FromFile(ctx, tmp)
		if err != nil {
			return nil, nil, err
		}
		return res, etags, nil
	}
	return nil, nil, errors.New("unknown input kind " + plan.Kind)
}
