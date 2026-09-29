package processing

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// HLS ladder: repackage, don't re-encode (2026-09-29).
//
// The worker used to encode every video twice: once into the MP4
// renditions (TranscodeVideo / TranscodeReel) and then AGAIN from the
// original into each HLS rung. On the dev worker (1 CPU) a 24-minute upload
// took about ten hours, and every other upload queued behind it.
//
// Every HLS rung (360p, 720p, 1080p) has an MP4 rendition of the same
// height from the first stage, so the rung is now a stream copy of that
// file into MPEG-TS segments: seconds of I/O instead of a full encode. The
// MP4 renditions force a keyframe every keyframeIntervalSeconds, which is
// what lets the copy cut hlsSegmentSeconds segments.
//
// A rung falls back to the old encode from the original when there is no
// rendition for it (a source below 360p still gets a 360p rung), when the
// copy fails, or when the copy's segments come out too long to be the
// aligned ladder we asked for. The fallback is logged per rung.
//
// File names, playlist names and master.m3u8 are unchanged, so the serving
// routes and the players do not know the difference.

// hlsMaxCopiedSegmentSeconds rejects a copied rung whose keyframes were
// not where they should be. With a keyframe every 2 s and -hls_time 6 the
// copy cuts at 6 s; a segment longer than one extra keyframe interval means
// the rendition was not encoded with forced keyframes, and a player would
// see coarse seeking and stalls on rung switches.
const hlsMaxCopiedSegmentSeconds = float64(hlsSegmentSeconds + keyframeIntervalSeconds)

// ffmpegRunner runs ffmpeg with args and returns its combined output. The
// pipeline takes it as a parameter so the rung decisions are testable
// without ffmpeg.
type ffmpegRunner func(ctx context.Context, args []string) ([]byte, error)

func execFFmpeg(ctx context.Context, args []string) ([]byte, error) {
	return exec.CommandContext(ctx, "ffmpeg", args...).CombinedOutput()
}

type hlsRungMethod string

const (
	hlsRungCopied  hlsRungMethod = "copy"
	hlsRungEncoded hlsRungMethod = "encode"
)

// hlsRungResult records how one rung was produced.
type hlsRungResult struct {
	Quality string
	Method  hlsRungMethod
	// FallbackReason says why a rung was encoded from the original while
	// the plan carried renditions; empty for a copied rung, and for every
	// rung of a plan without renditions.
	FallbackReason string
	// BandwidthBPS is the BANDWIDTH written to the master playlist.
	BandwidthBPS int
}

// nominalHLSBandwidth is what the master declared before segments were
// measured; it remains the fallback when a playlist cannot be read.
var nominalHLSBandwidth = map[string]int{"360p": 800000, "720p": 2500000, "1080p": 5000000}

var hlsResolutions = map[string]string{"360p": "640x360", "720p": "1280x720", "1080p": "1920x1080"}

// MP4Renditions maps each MP4 rendition in outputs to its local file, keyed
// by quality name, for HLSPlan.Renditions.
func MP4Renditions(outputs []TranscodeOutput) map[string]string {
	out := map[string]string{}
	for _, o := range outputs {
		if o.Mime == "video/mp4" && o.FilePath != "" {
			out[o.Name] = o.FilePath
		}
	}
	return out
}

// hlsRenditionFor returns the MP4 rendition a rung can be copied from: the
// plan names one for the rung's quality and it is a non-empty file.
func hlsRenditionFor(plan HLSPlan, v HLSVariant) (string, bool) {
	p := plan.Renditions[v.Quality]
	if p == "" {
		return "", false
	}
	st, err := os.Stat(p)
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 {
		return "", false
	}
	return p, true
}

// hlsCopyArgs repackages an MP4 rendition into one HLS rung without
// touching a pixel. ffmpeg inserts the h264 Annex-B conversion the TS
// muxer needs on its own.
func hlsCopyArgs(renditionPath, outputM3U8, segmentPattern string) []string {
	return []string{
		"-y", "-i", renditionPath,
		"-c", "copy",
		"-f", "hls",
		"-hls_time", strconv.Itoa(hlsSegmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segmentPattern,
		outputM3U8,
	}
}

// hlsEncodeArgs is the original per-rung encode from the source, kept
// byte-for-byte as the fallback.
func hlsEncodeArgs(inputPath, outputM3U8, segmentPattern string, v HLSVariant, preset string) []string {
	return []string{
		"-i", inputPath,
		"-vf", fmt.Sprintf("scale=-2:%d", v.Height),
		"-c:v", "libx264", "-preset", preset,
		"-b:v", v.VideoBitrate,
		"-c:a", "aac", "-b:a", v.AudioBitrate,
		"-hls_time", strconv.Itoa(hlsSegmentSeconds),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segmentPattern,
		outputM3U8,
		"-y",
	}
}

func generateHLS(ctx context.Context, inputPath, outputDir string, plan HLSPlan, run ffmpegRunner) (masterPlaylistPath string, variantPaths []string, results []hlsRungResult, err error) {
	variants := hlsVariantsFor(plan)
	preset := hlsPreset(plan)
	for _, v := range variants {
		outputM3U8 := filepath.Join(outputDir, v.Quality+".m3u8")
		segmentPattern := filepath.Join(outputDir, v.Quality+"_%03d.ts")
		res := hlsRungResult{Quality: v.Quality, Method: hlsRungEncoded}

		if src, ok := hlsRenditionFor(plan, v); ok {
			reason := copyHLSRung(ctx, run, src, outputM3U8, segmentPattern)
			if reason == "" {
				res.Method = hlsRungCopied
			} else {
				if ctx.Err() != nil {
					return "", nil, results, ctx.Err()
				}
				res.FallbackReason = reason
				log.Printf("HLS %s: stream copy of %s rejected (%s); encoding from the original",
					v.Quality, filepath.Base(src), reason)
				// A failed copy can leave a partial playlist and segments;
				// the encode must not inherit a stale tail.
				removeHLSRungFiles(outputDir, v.Quality)
			}
		} else if len(plan.Renditions) > 0 {
			res.FallbackReason = "no MP4 rendition of this height"
			log.Printf("HLS %s: no MP4 rendition of this height; encoding from the original", v.Quality)
		}

		if res.Method == hlsRungEncoded {
			if out, runErr := run(ctx, hlsEncodeArgs(inputPath, outputM3U8, segmentPattern, v, preset)); runErr != nil {
				return "", nil, results, fmt.Errorf("ffmpeg HLS %s failed: %w\n%s", v.Quality, runErr, out)
			}
		}

		variantPaths = append(variantPaths, outputM3U8)
		tsFiles, _ := filepath.Glob(filepath.Join(outputDir, v.Quality+"_*.ts"))
		variantPaths = append(variantPaths, tsFiles...)

		res.BandwidthBPS = nominalHLSBandwidth[v.Quality]
		if bps, ok := peakSegmentBandwidth(outputM3U8); ok {
			res.BandwidthBPS = bps
		}
		results = append(results, res)
	}

	masterPlaylistPath = filepath.Join(outputDir, "master.m3u8")
	if err := os.WriteFile(masterPlaylistPath, []byte(hlsMasterPlaylist(results)), 0644); err != nil {
		return "", nil, results, fmt.Errorf("write HLS master: %w", err)
	}
	return masterPlaylistPath, variantPaths, results, nil
}

// hlsMasterPlaylist lists only the rungs that were produced: a master
// naming a variant that does not exist sends the player to a 404
// mid-stream.
func hlsMasterPlaylist(results []hlsRungResult) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, r := range results {
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,RESOLUTION=%s\n%s.m3u8\n",
			r.BandwidthBPS, hlsResolutions[r.Quality], r.Quality)
	}
	return b.String()
}

// copyHLSRung stream-copies a rendition into a rung and returns why the
// result is unusable, or "" when it is good.
func copyHLSRung(ctx context.Context, run ffmpegRunner, renditionPath, outputM3U8, segmentPattern string) string {
	if out, err := run(ctx, hlsCopyArgs(renditionPath, outputM3U8, segmentPattern)); err != nil {
		return fmt.Sprintf("ffmpeg: %v: %s", err, lastLine(out))
	}
	segs, err := readHLSPlaylist(outputM3U8)
	if err != nil {
		return fmt.Sprintf("read playlist: %v", err)
	}
	if len(segs) == 0 {
		return "playlist has no segments"
	}
	for _, s := range segs {
		if s.Duration > hlsMaxCopiedSegmentSeconds {
			return fmt.Sprintf("segment %s is %.2fs, over %.0fs: keyframes not aligned", s.URI, s.Duration, hlsMaxCopiedSegmentSeconds)
		}
	}
	return ""
}

type hlsSegment struct {
	URI      string
	Duration float64
}

// readHLSPlaylist reads the #EXTINF durations and URIs of a media playlist.
func readHLSPlaylist(path string) ([]hlsSegment, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var segs []hlsSegment
	pending, havePending := 0.0, false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "#EXTINF:"):
			v := strings.TrimPrefix(line, "#EXTINF:")
			if i := strings.IndexByte(v, ','); i >= 0 {
				v = v[:i]
			}
			d, perr := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if perr != nil {
				return nil, fmt.Errorf("bad EXTINF %q", line)
			}
			pending, havePending = d, true
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if havePending {
				segs = append(segs, hlsSegment{URI: line, Duration: pending})
				havePending = false
			}
		}
	}
	return segs, sc.Err()
}

// peakSegmentBandwidth measures a rung's BANDWIDTH as HLS defines it: the
// peak bitrate over its segments. The copied rungs are CRF encodes, so the
// old fixed ladder numbers no longer describe them, and a player that trusts
// a BANDWIDTH below the real rate picks a rung it cannot sustain.
// A segment under a second (the tail) is skipped when longer ones exist:
// one keyframe over a sliver of time would read as an absurd peak.
func peakSegmentBandwidth(playlistPath string) (int, bool) {
	segs, err := readHLSPlaylist(playlistPath)
	if err != nil || len(segs) == 0 {
		return 0, false
	}
	dir := filepath.Dir(playlistPath)
	peak, peakShort := 0.0, 0.0
	for _, s := range segs {
		if s.Duration <= 0 {
			continue
		}
		segPath := s.URI
		if !filepath.IsAbs(segPath) {
			segPath = filepath.Join(dir, segPath)
		}
		st, err := os.Stat(segPath)
		if err != nil {
			return 0, false
		}
		bps := float64(st.Size()) * 8 / s.Duration
		if s.Duration < 1 {
			peakShort = math.Max(peakShort, bps)
			continue
		}
		peak = math.Max(peak, bps)
	}
	if peak == 0 {
		peak = peakShort
	}
	if peak == 0 {
		return 0, false
	}
	return int(math.Ceil(peak)), true
}

func removeHLSRungFiles(outputDir, quality string) {
	_ = os.Remove(filepath.Join(outputDir, quality+".m3u8"))
	segs, _ := filepath.Glob(filepath.Join(outputDir, quality+"_*.ts"))
	for _, s := range segs {
		_ = os.Remove(s)
	}
}

func lastLine(out []byte) string {
	s := strings.TrimSpace(string(out))
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return s
}
