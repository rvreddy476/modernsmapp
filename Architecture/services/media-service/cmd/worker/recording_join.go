package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/google/uuid"
)

// Joined live recordings (16 Nov 2026).
//
// A live stream whose encoder reconnected is imported as several parts:
// the import (internal/service/recording_import.go importParts) copies them
// to <asset prefix>parts/NNN.mp4 and registers the asset with NO original.
// Before the normal pipeline, a transcode whose original is absent looks
// there; when it finds parts it downloads them in key (= part) order,
// joins them (processing.JoinVideoParts: stream copy when ffprobe shows
// identical parameters, else a re-encode), uploads the result as the
// original, records its ETag and size, and removes the part copies. The
// transcode then runs on the joined file exactly as for an upload.
//
//   - Original present (one-part import, any upload, or a re-run after a
//     join already succeeded): nothing here runs; no listing either.
//   - Original absent and no parts: nothing here; the pipeline's own stat
//     fails the transcode as before.
//   - A part copy missing, not MP4, or unreadable by ffprobe: left out and
//     logged. None usable: a terminal failure.
//
// The raw parts in the recordings bucket are never touched here.

// joinBlobs is what the join needs from the media bucket.
type joinBlobs interface {
	StatObject(ctx context.Context, objectKey string) (blob.ObjectInfo, error)
	ListObjectKeys(ctx context.Context, prefix string) ([]string, error)
	DownloadObjectToFileIfMatch(ctx context.Context, objectKey, path, etag string) (int64, error)
	UploadFileInfo(ctx context.Context, objectKey, path, contentType string) (blob.ObjectInfo, error)
	DeleteObject(ctx context.Context, objectKey string) error
}

// originalRewriter records the joined original's ETag and size.
type originalRewriter interface {
	RecordOriginalRewrite(ctx context.Context, id uuid.UUID, etag string, sizeBytes int64) error
}

// Seams over processing (tests fake them; production runs ffprobe/ffmpeg).
var (
	probeJoinPart  = processing.ProbeJoinPart
	joinVideoParts = processing.JoinVideoParts
)

// recordingPartKeys filters a listing to the part copies, in part order.
func recordingPartKeys(listed []string, partsPrefix string) []string {
	var out []string
	for _, k := range listed {
		rest, ok := strings.CutPrefix(k, partsPrefix)
		if !ok || rest == "" || strings.Contains(rest, "/") || !strings.HasSuffix(rest, ".mp4") {
			continue
		}
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// joinRecordingParts joins the asset's part copies into its original when
// the original is absent and parts exist (see above). It returns the local
// path of the joined original under tmpDir, or "" when nothing was joined
// (the caller downloads the original as usual).
func joinRecordingParts(parent context.Context, blobs joinBlobs, rewriter originalRewriter,
	subject *transcodeSubject, tmpDir string, maxBytes int64, setDeadline func(time.Duration) context.Context) (string, error) {
	if !strings.HasSuffix(subject.StorageKey, "/original") {
		return "", nil
	}
	statCtx, cancel := context.WithTimeout(parent, s3MetadataCall)
	_, err := blobs.StatObject(statCtx, subject.StorageKey)
	cancel()
	if err == nil {
		return "", nil
	}
	if !errors.Is(err, blob.ErrObjectNotFound) {
		return "", fmt.Errorf("stat original: %w", err)
	}
	partsPrefix := subject.assetPrefix() + processing.RecordingPartsDir
	listCtx, cancel := context.WithTimeout(parent, s3MetadataCall)
	listed, err := blobs.ListObjectKeys(listCtx, partsPrefix)
	cancel()
	if err != nil {
		return "", fmt.Errorf("list recording parts: %w", err)
	}
	keys := recordingPartKeys(listed, partsPrefix)
	if len(keys) == 0 {
		return "", nil
	}
	if len(keys) > processing.MaxJoinParts {
		return "", permanentTranscode(fmt.Errorf("media %s has %d recording parts, at most %d", subject.ID, len(keys), processing.MaxJoinParts))
	}

	// Sizes first: the total is one original's worth of scratch and cap.
	type part struct {
		key  string
		info blob.ObjectInfo
	}
	var parts []part
	var total int64
	for _, k := range keys {
		sctx, cancel := context.WithTimeout(parent, s3MetadataCall)
		info, err := blobs.StatObject(sctx, k)
		cancel()
		if errors.Is(err, blob.ErrObjectNotFound) {
			log.Printf("Warning: recording part %s of media %s is missing; joining the rest", k, subject.ID) //nolint:gosec // G706: a listed object key and a parsed uuid
			continue
		}
		if err != nil {
			return "", fmt.Errorf("stat recording part: %w", err)
		}
		parts = append(parts, part{key: k, info: info})
		total += info.Size
	}
	if len(parts) == 0 {
		return "", permanentTranscode(fmt.Errorf("media %s: none of its %d recording parts is in storage", subject.ID, len(keys)))
	}
	space, spaceKnown := scratchSpaceOf(tmpDir)
	if err := checkOriginalBeforeDownload(blob.ObjectInfo{Size: total}, &transcodeSubject{ID: subject.ID}, maxBytes, space, spaceKnown); err != nil {
		return "", err
	}

	ctx := setDeadline(s3Timeout(total) + time.Duration(len(parts))*(s3CallBase+processing.ProbeTimeout) + 10*time.Minute)
	partsDir := filepath.Join(tmpDir, "parts")
	if err := os.MkdirAll(partsDir, 0o700); err != nil {
		return "", fmt.Errorf("create parts dir: %w", err)
	}
	defer os.RemoveAll(partsDir)
	var joinable []processing.JoinPart
	var duration float64
	for i, p := range parts {
		local := filepath.Join(partsDir, fmt.Sprintf("%03d.mp4", i+1))
		dctx, cancel := context.WithTimeout(ctx, s3Timeout(p.info.Size))
		_, err := blobs.DownloadObjectToFileIfMatch(dctx, p.key, local, p.info.ETag)
		cancel()
		if errors.Is(err, blob.ErrObjectNotFound) {
			log.Printf("Warning: recording part %s of media %s vanished; joining the rest", p.key, subject.ID) //nolint:gosec // G706: a listed object key and a parsed uuid
			continue
		}
		if err != nil {
			return "", classifyDownloadError(parent, err)
		}
		if kind, err := processing.SniffVideoFile(local); err != nil || kind != "mov" {
			log.Printf("Warning: recording part %s of media %s is not MP4; left out", p.key, subject.ID) //nolint:gosec // G706: a listed object key and a parsed uuid
			continue
		}
		streams, err := probeJoinPart(ctx, local)
		if err != nil {
			if parent.Err() != nil {
				return "", err
			}
			log.Printf("Warning: recording part %s of media %s not probed; left out: %v", p.key, subject.ID, err) //nolint:gosec // G706: a listed object key, a parsed uuid and an ffprobe error
			continue
		}
		if streams.Video == nil {
			log.Printf("Warning: recording part %s of media %s has no video; left out", p.key, subject.ID) //nolint:gosec // G706: a listed object key and a parsed uuid
			continue
		}
		joinable = append(joinable, processing.JoinPart{Path: local, Streams: streams})
		duration += streams.DurationSec
	}
	if len(joinable) == 0 {
		return "", permanentTranscode(fmt.Errorf("media %s: no usable recording part (%w)", subject.ID, processing.ErrNoJoinableParts))
	}

	ctx = setDeadline(2*processing.FFmpegCallTimeout(duration) + 2*s3Timeout(total) + 10*time.Minute)
	outPath := filepath.Join(tmpDir, "original")
	mode := processing.JoinMode("single part, no join")
	if len(joinable) == 1 {
		if err := os.Rename(joinable[0].Path, outPath); err != nil {
			return "", fmt.Errorf("place the one recording part: %w", err)
		}
	} else {
		mode, err = joinVideoParts(ctx, joinable, tmpDir, outPath)
		if err != nil {
			return "", permanentUnlessCancelled(parent, fmt.Errorf("join recording parts: %w", err))
		}
	}
	_ = os.RemoveAll(partsDir) // scratch back before the transcode

	fi, err := os.Stat(outPath)
	if err != nil {
		return "", fmt.Errorf("stat joined original: %w", err)
	}
	uctx, cancel := context.WithTimeout(ctx, s3Timeout(fi.Size()))
	up, err := blobs.UploadFileInfo(uctx, subject.StorageKey, outPath, subject.MimeType)
	cancel()
	if err != nil {
		return "", fmt.Errorf("upload joined original: %w", err)
	}
	if err := rewriter.RecordOriginalRewrite(ctx, subject.ID, up.ETag, up.Size); err != nil {
		return "", fmt.Errorf("record joined original: %w", err)
	}
	// The copies are this asset's own; the original now stands for them.
	for _, p := range parts {
		dctx, cancel := context.WithTimeout(parent, s3MetadataCall)
		if err := blobs.DeleteObject(dctx, p.key); err != nil {
			log.Printf("Warning: recording part copy %s of media %s not removed: %v", p.key, subject.ID, err) //nolint:gosec // G706: a listed object key, a parsed uuid and a store error
		}
		cancel()
	}
	log.Printf("Joined %d of %d recording parts of media %s (%s, %d bytes)", //nolint:gosec // G706: counts, a parsed uuid, a fixed mode and a size
		len(joinable), len(keys), subject.ID, mode, up.Size)
	return outPath, nil
}
