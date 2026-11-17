package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/buckket/go-blurhash"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Durable rungs (long-video audit lv-5, lv-13, lv-15; reels-34; 2026-11-17).
//
// THE PROBLEM. Every rendition was encoded into one temp directory, nothing
// was uploaded until the last rung was done, and any interruption (a worker
// rollout, a node drain, an OOM kill, an in-process retry after a scanner
// outage) threw all of it away: the next run started again from the first
// rung. For a multi-hour video that is hours of encode per interruption,
// and a scanner outage re-encoded every queued video up to 32 times. All
// rungs also sat on the scratch volume together until the job ended, and a
// rung that failed was dropped without a log line, its job row left
// 'processing' for ever.
//
// NOW. One UNIT at a time — an MP4 rung together with its HLS rung, the
// storyboard, or an HLS rung that has no MP4 rendition:
//
//  1. encode it into scratch;
//  2. upload its objects (the MP4 and, for an HLS-ladder height, its HLS
//     playlist and segments);
//  3. write its MARKER, <asset>/rungs/<unit>.json, ONLY after every upload
//     of the unit succeeded;
//  4. delete its local files before the next unit starts.
//
// A later run (a redelivery after an interruption, or an in-process retry)
// REUSES a unit whose marker names the same media id, generation, source
// ETag and encode recipe, and whose objects still exist at the recorded
// size; it encodes only the first unit that is missing and everything after.
// Mere presence of an object proves nothing (an upload cut off half way, or
// a different source); the marker is the proof.
//
// A unit that fails is logged with the ffmpeg line, counted, and its job row
// marked failed; the video still goes ready with the rungs it has.

var rungFailures = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "media_transcode_rung_failures_total",
	Help: "Renditions that failed to encode and were left out of a transcode, by rung.",
}, []string{"rung"})

var rungOutcomes = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "media_transcode_rungs_total",
	Help: "Transcode units by outcome: encoded now or reused from an earlier run's durable marker.",
}, []string{"outcome"})

// errWorkerDraining: the worker is shutting down and the next unit was not
// started. Transient: the finished units are durable and the next run
// resumes from here.
var errWorkerDraining = errors.New("worker draining: stopped before the next rendition; finished renditions are kept")

const rungMarkerVersion = 1

// rungMarker is the durable record that one unit is complete.
type rungMarker struct {
	Version    int             `json:"v"`
	MediaID    string          `json:"media_id"`
	Generation int64           `json:"generation"`
	SourceETag string          `json:"source_etag"`
	Recipe     string          `json:"recipe"`
	Unit       string          `json:"unit"`
	Variants   []markerVariant `json:"variants,omitempty"`
	HLS        *markerHLS      `json:"hls,omitempty"`
	BlurHash   string          `json:"blurhash,omitempty"`
}

type markerVariant struct {
	Name      string `json:"name"`
	ObjectKey string `json:"object_key"`
	Mime      string `json:"mime"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Size      int64  `json:"size"`
}

type markerHLS struct {
	Quality      string `json:"quality"`
	BandwidthBPS int    `json:"bandwidth_bps"`
	// Keys: the playlist first, then its segments.
	Keys []string `json:"keys"`
}

// rungBlobs is the object store surface the pipeline needs (*blob.Store).
type rungBlobs interface {
	UploadFile(ctx context.Context, objectKey, path, contentType string) (int64, error)
	UploadObject(ctx context.Context, objectKey string, data []byte, contentType string) error
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
	StatObject(ctx context.Context, objectKey string) (blob.ObjectInfo, error)
}

// rungJobs is the job-row update the pipeline needs.
type rungJobs interface {
	UpdateTranscodingJob(ctx context.Context, jobID uuid.UUID, status string, outputURL *string, outputSizeBytes *int64, errorMessage *string) error
}

// rungEncoder makes one unit's local files (processing in production).
type rungEncoder interface {
	Encode(ctx context.Context, input, dir string, r processing.Rung, meta *processing.VideoMeta, reel bool) (processing.TranscodeOutput, error)
	HLS(ctx context.Context, input, rendition, dir string, plan processing.HLSPlan, v processing.HLSVariant) (processing.HLSRungOutput, error)
	// Storyboard returns no outputs and no error for a video too short.
	Storyboard(ctx context.Context, input, dir string, durationSeconds float64) ([]processing.TranscodeOutput, error)
}

type ffmpegEncoder struct{}

func (ffmpegEncoder) Encode(ctx context.Context, input, dir string, r processing.Rung, meta *processing.VideoMeta, reel bool) (processing.TranscodeOutput, error) {
	return processing.EncodeRung(ctx, input, dir, r, meta, reel)
}
func (ffmpegEncoder) HLS(ctx context.Context, input, rendition, dir string, plan processing.HLSPlan, v processing.HLSVariant) (processing.HLSRungOutput, error) {
	return processing.GenerateHLSRung(ctx, input, rendition, dir, plan, v)
}
func (ffmpegEncoder) Storyboard(ctx context.Context, input, dir string, durationSeconds float64) ([]processing.TranscodeOutput, error) {
	plan, ok := processing.PlanStoryboard(durationSeconds)
	if !ok {
		return nil, nil
	}
	return processing.GenerateStoryboard(ctx, input, dir, plan)
}

// rungRun is one transcode's rung work.
type rungRun struct {
	mediaID    uuid.UUID
	baseKey    string // "user/<uploader>/<id>"
	generation int64
	sourceETag string
	input      string
	scratch    string // this job's temp dir
	reel       bool
	meta       *processing.VideoMeta
	jobIDs     map[string]uuid.UUID // rung name → its transcoding_jobs row
	// stopping reports a SIGTERM: no new unit is started once it is true.
	stopping func() bool
}

// rungResult is what the rest of the transcode needs.
type rungResult struct {
	variants []postgres.MediaVariant
	hls      []processing.HLSMasterRung // ladder order
	hlsKeys  map[string]bool            // every HLS object the master references
	blurHash string
	encoded  []string
	reused   []string
	failed   []string
}

const storyboardUnit = "storyboard"

func hlsOnlyUnit(q string) string { return "hls_" + q }

func (r *rungRun) markerKey(unit string) string { return r.baseKey + "/rungs/" + unit + ".json" }
func (r *rungRun) hlsPrefix() string            { return r.baseKey + "/hls/" }

// runRungs produces (or reuses) every unit of the transcode.
func runRungs(ctx context.Context, r *rungRun, blobs rungBlobs, jobs rungJobs, enc rungEncoder) (*rungResult, error) {
	res := &rungResult{hlsKeys: map[string]bool{}}
	hlsPlan := processing.HLSPlan{Reel: r.reel, SourceHeight: r.meta.Height}
	ladder := processing.HLSRungsFor(hlsPlan)
	hlsWanted := map[string]processing.HLSVariant{}
	for _, v := range ladder {
		hlsWanted[v.Quality] = v
	}
	hlsDone := map[string]processing.HLSMasterRung{}
	hlsDir := filepath.Join(r.scratch, "hls")
	if err := os.MkdirAll(hlsDir, 0o700); err != nil {
		return nil, fmt.Errorf("create HLS scratch: %w", err)
	}

	// 1. The rendition rungs, each with its HLS rung when the ladder has
	//    that height.
	for _, rung := range processing.RungPlan(r.meta, r.reel) {
		v, wantHLS := hlsWanted[rung.Name]
		recipe := processing.RungRecipe(rung, r.reel)
		if wantHLS {
			recipe += "+hls"
		}
		if m, ok := r.reusable(ctx, blobs, rung.Name, recipe, wantHLS); ok {
			res.absorb(m, hlsDone)
			res.reused = append(res.reused, rung.Name)
			rungOutcomes.WithLabelValues("reused").Inc()
			if err := markJobDone(ctx, jobs, r.jobIDs[rung.Name], m.Variants); err != nil {
				return nil, err
			}
			continue
		}
		if r.stopping != nil && r.stopping() {
			return nil, errWorkerDraining
		}
		out, err := enc.Encode(ctx, r.input, r.scratch, rung, r.meta, r.reel)
		if err != nil {
			if ctx.Err() != nil {
				return nil, err
			}
			if ferr := r.recordRungFailure(ctx, jobs, rung.Name, err); ferr != nil {
				return nil, ferr
			}
			res.failed = append(res.failed, rung.Name)
			continue
		}
		m := r.newMarker(rung.Name, recipe)
		mv, err := r.uploadVariant(ctx, blobs, out)
		if err != nil {
			removeLocal(out.FilePath, true)
			return nil, err
		}
		m.Variants = []markerVariant{mv}
		if rung.Kind == processing.RungThumb {
			m.BlurHash = blurHashOf(out.FilePath)
		}
		var hlsErr error
		if wantHLS {
			m.HLS, hlsErr = r.produceHLS(ctx, blobs, enc, hlsPlan, v, out.FilePath, hlsDir)
		}
		// lv-13: the rendition is uploaded (and copied into HLS); it does not
		// wait on the scratch volume for the rest of the ladder.
		removeLocal(out.FilePath, true)
		if hlsErr != nil {
			return nil, hlsErr
		}
		if err := r.writeMarker(ctx, blobs, m); err != nil {
			return nil, err
		}
		res.absorb(m, hlsDone)
		res.encoded = append(res.encoded, rung.Name)
		rungOutcomes.WithLabelValues("encoded").Inc()
		if err := markJobDone(ctx, jobs, r.jobIDs[rung.Name], m.Variants); err != nil {
			return nil, err
		}
	}

	// 2. The storyboard (seek preview). Its failure is logged and skipped:
	//    the video plays without a hover preview.
	sbRecipe := "storyboard/v1"
	if m, ok := r.reusable(ctx, blobs, storyboardUnit, sbRecipe, false); ok {
		res.absorb(m, hlsDone)
		res.reused = append(res.reused, storyboardUnit)
		rungOutcomes.WithLabelValues("reused").Inc()
	} else {
		if r.stopping != nil && r.stopping() {
			return nil, errWorkerDraining
		}
		outs, err := enc.Storyboard(ctx, r.input, r.scratch, r.meta.DurationFloat)
		switch {
		case err != nil && ctx.Err() != nil:
			return nil, err
		case err != nil:
			log.Printf("Warning: storyboard for media %s not generated: %v", r.mediaID, err)
		case len(outs) > 0:
			m := r.newMarker(storyboardUnit, sbRecipe)
			for _, o := range outs {
				mv, err := r.uploadVariant(ctx, blobs, o)
				removeLocal(o.FilePath, true)
				if err != nil {
					return nil, err
				}
				m.Variants = append(m.Variants, mv)
			}
			if err := r.writeMarker(ctx, blobs, m); err != nil {
				return nil, err
			}
			res.absorb(m, hlsDone)
			res.encoded = append(res.encoded, storyboardUnit)
			rungOutcomes.WithLabelValues("encoded").Inc()
		}
	}

	// 3. HLS rungs no rendition covered (a source under 360p still gets a
	//    360p rung; a rendition that failed) are encoded from the original.
	for _, v := range ladder {
		if _, ok := hlsDone[v.Quality]; ok {
			continue
		}
		unit := hlsOnlyUnit(v.Quality)
		recipe := fmt.Sprintf("hls/%s/%s/v1", v.Quality, v.VideoBitrate)
		if m, ok := r.reusable(ctx, blobs, unit, recipe, true); ok {
			res.absorb(m, hlsDone)
			res.reused = append(res.reused, unit)
			rungOutcomes.WithLabelValues("reused").Inc()
			continue
		}
		if r.stopping != nil && r.stopping() {
			return nil, errWorkerDraining
		}
		h, err := r.produceHLS(ctx, blobs, enc, hlsPlan, v, "", hlsDir)
		if err != nil {
			return nil, err
		}
		m := r.newMarker(unit, recipe)
		m.HLS = h
		if err := r.writeMarker(ctx, blobs, m); err != nil {
			return nil, err
		}
		res.absorb(m, hlsDone)
		res.encoded = append(res.encoded, unit)
		rungOutcomes.WithLabelValues("encoded").Inc()
	}

	for _, v := range ladder {
		if h, ok := hlsDone[v.Quality]; ok {
			res.hls = append(res.hls, h)
		}
	}
	return res, nil
}

func (res *rungResult) absorb(m *rungMarker, hlsDone map[string]processing.HLSMasterRung) {
	for _, v := range m.Variants {
		w, h, sz := v.Width, v.Height, v.Size
		res.variants = append(res.variants, postgres.MediaVariant{
			Name: v.Name, ObjectKey: v.ObjectKey, Mime: v.Mime, Width: &w, Height: &h, SizeBytes: &sz,
		})
	}
	if m.BlurHash != "" {
		res.blurHash = m.BlurHash
	}
	if m.HLS != nil {
		hlsDone[m.HLS.Quality] = processing.HLSMasterRung{Quality: m.HLS.Quality, BandwidthBPS: m.HLS.BandwidthBPS}
		for _, k := range m.HLS.Keys {
			res.hlsKeys[k] = true
		}
	}
}

func (r *rungRun) newMarker(unit, recipe string) *rungMarker {
	return &rungMarker{Version: rungMarkerVersion, MediaID: r.mediaID.String(), Generation: r.generation,
		SourceETag: r.sourceETag, Recipe: recipe, Unit: unit}
}

// reusable reads a unit's marker and decides whether it may stand in for
// encoding the unit again. Any doubt is "no": the unit is simply encoded.
func (r *rungRun) reusable(ctx context.Context, blobs rungBlobs, unit, recipe string, wantHLS bool) (*rungMarker, bool) {
	if r.sourceETag == "" {
		return nil, false // nothing to pin the source to
	}
	c, cancel := context.WithTimeout(ctx, s3MetadataCall)
	data, err := blobs.DownloadObject(c, r.markerKey(unit))
	cancel()
	if err != nil {
		return nil, false
	}
	var m rungMarker
	if json.Unmarshal(data, &m) != nil {
		return nil, false
	}
	if m.Version != rungMarkerVersion || m.MediaID != r.mediaID.String() || m.Generation != r.generation ||
		m.SourceETag != r.sourceETag || m.Recipe != recipe || m.Unit != unit {
		return nil, false
	}
	if (m.HLS != nil) != wantHLS || (m.HLS == nil && len(m.Variants) == 0) {
		return nil, false
	}
	prefix := r.baseKey + "/"
	for _, v := range m.Variants {
		if !strings.HasPrefix(v.ObjectKey, prefix) || !objectHasSize(ctx, blobs, v.ObjectKey, v.Size) {
			return nil, false
		}
	}
	if m.HLS != nil {
		if len(m.HLS.Keys) < 2 || !objectHasSize(ctx, blobs, m.HLS.Keys[0], -1) {
			return nil, false
		}
		for _, k := range m.HLS.Keys {
			if !strings.HasPrefix(k, r.hlsPrefix()) {
				return nil, false
			}
		}
	}
	log.Printf("Rendition %s of media %s reused from an earlier run (generation %d)", unit, r.mediaID, r.generation) //nolint:gosec // G706: unit is an internal ladder name; media id a parsed uuid
	return &m, true
}

// objectHasSize: the object exists and, when size >= 0, has that size.
func objectHasSize(ctx context.Context, blobs rungBlobs, key string, size int64) bool {
	c, cancel := context.WithTimeout(ctx, s3MetadataCall)
	defer cancel()
	info, err := blobs.StatObject(c, key)
	return err == nil && (size < 0 || info.Size == size)
}

func (r *rungRun) uploadVariant(ctx context.Context, blobs rungBlobs, out processing.TranscodeOutput) (markerVariant, error) {
	name := out.ObjectName
	if name == "" {
		name = out.Name
	}
	key := r.baseKey + "/" + name
	sz, err := uploadFileBounded(ctx, blobs, key, out.FilePath, out.Mime)
	if err != nil {
		return markerVariant{}, fmt.Errorf("upload transcode variant %s: %w", out.Name, err)
	}
	return markerVariant{Name: out.Name, ObjectKey: key, Mime: out.Mime, Width: out.Width, Height: out.Height, Size: sz}, nil
}

// produceHLS makes one HLS rung, uploads its playlist and segments, and
// deletes them locally. A failure is terminal unless the job was cancelled,
// as the all-at-once ladder's was.
func (r *rungRun) produceHLS(ctx context.Context, blobs rungBlobs, enc rungEncoder, plan processing.HLSPlan,
	v processing.HLSVariant, rendition, dir string) (*markerHLS, error) {
	out, err := enc.HLS(ctx, r.input, rendition, dir, plan, v)
	if err != nil {
		removeAll(out.Files)
		return nil, permanentUnlessCancelled(ctx, fmt.Errorf("generate HLS %s: %w", v.Quality, err))
	}
	defer removeAll(out.Files)
	h := &markerHLS{Quality: v.Quality, BandwidthBPS: out.BandwidthBPS}
	for _, f := range out.Files {
		key := r.hlsPrefix() + filepath.Base(f)
		ct := "video/MP2T"
		if strings.HasSuffix(f, ".m3u8") {
			ct = "application/x-mpegURL"
		}
		if _, err := uploadFileBounded(ctx, blobs, key, f, ct); err != nil {
			return nil, fmt.Errorf("upload HLS file %s: %w", key, err)
		}
		h.Keys = append(h.Keys, key)
	}
	return h, nil
}

// writeMarker records the unit complete. Only ever called after every
// upload of the unit returned.
func (r *rungRun) writeMarker(ctx context.Context, blobs rungBlobs, m *rungMarker) error {
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("encode rung marker: %w", err)
	}
	if err := uploadBytesBounded(ctx, blobs, r.markerKey(m.Unit), data, "application/json"); err != nil {
		return fmt.Errorf("record rendition %s complete: %w", m.Unit, err)
	}
	return nil
}

// recordRungFailure logs a failed rung (lv-15: it used to vanish without a
// line), counts it and marks its job row failed with the reason.
func (r *rungRun) recordRungFailure(ctx context.Context, jobs rungJobs, rung string, cause error) error {
	log.Printf("Rendition %s of media %s FAILED and is left out: %v", rung, r.mediaID, cause) //nolint:gosec // G706: rung is an internal ladder name; media id a parsed uuid
	rungFailures.WithLabelValues(rung).Inc()
	id, ok := r.jobIDs[rung]
	if !ok {
		return nil
	}
	msg := cause.Error()
	if err := jobs.UpdateTranscodingJob(ctx, id, "failed", nil, nil, &msg); err != nil {
		return fmt.Errorf("mark transcode job %s failed: %w", rung, err)
	}
	return nil
}

func markJobDone(ctx context.Context, jobs rungJobs, id uuid.UUID, vs []markerVariant) error {
	if id == uuid.Nil || len(vs) == 0 {
		return nil
	}
	key, sz := vs[0].ObjectKey, vs[0].Size
	if err := jobs.UpdateTranscodingJob(ctx, id, "completed", &key, &sz, nil); err != nil {
		return fmt.Errorf("mark transcode job %s complete: %w", vs[0].Name, err)
	}
	return nil
}

// removeLocal deletes a unit's local file when now is true.
func removeLocal(path string, now bool) {
	if now && path != "" {
		_ = os.Remove(path)
	}
}

func removeAll(paths []string) {
	for _, p := range paths {
		_ = os.Remove(p)
	}
}

// blurHashOf is the poster's blurhash, "" when it cannot be computed.
func blurHashOf(path string) string {
	data, err := os.ReadFile(path) //nolint:gosec // G304: a worker-built temp path
	if err != nil {
		return ""
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return ""
	}
	h, err := blurhash.Encode(4, 3, img)
	if err != nil {
		return ""
	}
	return h
}
