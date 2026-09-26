package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
)

/*
	Anonymous-scoped assets: pictures and videos attached to anonymous group
	posts.

	Two things must not reach a viewer who is not the uploader:

	  1. The asset's RECORD. GET /v1/media/:id returns uploader_id and the
	     storage keys, and every key is user/<uploader>/<media>/…. So, like a
	     dating photo, an anonymous asset's record is its uploader's alone;
	     everyone else gets the not-found every denied read gives.

	  2. The DELIVERY URL. Every other asset is served by redirecting to a
	     signed object URL — whose path is that same user-keyed storage key.
	     An anonymous asset is instead streamed by this service, with Range
	     support so video still seeks, and its HLS playlists point segments
	     back at this service (/hls-seg/) instead of at signed object URLs.

	The audience decision itself does not change: the same content
	authorities (group-service among them) decide who may have the bytes.
*/

// ErrNotAnonymousScope tells the handler to take the ordinary redirect path.
var ErrNotAnonymousScope = errors.New("media is not anonymous-scoped")

// AnonymousScopeDenies mirrors DatingScopeDenies for the record read: an
// anonymous asset's metadata is its uploader's alone.
func AnonymousScopeDenies(m *postgres.MediaAsset, viewerID uuid.UUID) bool {
	if m == nil || m.AccessScope != postgres.AccessScopeAnonymous {
		return false
	}
	return viewerID == uuid.Nil || viewerID != m.UploaderID
}

// MarkAnonymous scopes an asset; (false, nil) when there is no such asset.
func (s *Service) MarkAnonymous(ctx context.Context, mediaID uuid.UUID) (bool, error) {
	return s.pgStore.MarkAnonymous(ctx, mediaID)
}

// StreamResult is one authorized read of an anonymous object, possibly a
// byte range of it.
type StreamResult struct {
	Data        []byte
	ContentType string
	Size        int64
	Start, End  int64
	Partial     bool
}

// StreamAnonymous serves the original or a named variant of an anonymous
// asset. It answers ErrNotAnonymousScope for any other asset so the caller
// can fall through to the redirect path unchanged.
func (s *Service) StreamAnonymous(ctx context.Context, viewerID, mediaID uuid.UUID, variant, rangeHeader string) (*StreamResult, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if media == nil || media.AccessScope != postgres.AccessScopeAnonymous {
		return nil, ErrNotAnonymousScope
	}
	if err := authorizeCaptionRead(ctx, s.gate, media, viewerID); err != nil {
		return nil, err
	}
	key := media.StorageKey
	contentType := media.MimeType
	if variant != "" && variant != "original" {
		variants, err := s.pgStore.GetVariants(ctx, mediaID)
		if err != nil {
			return nil, err
		}
		key = ""
		for _, v := range variants {
			if v.Name == variant {
				key = v.ObjectKey
				if v.Mime != "" {
					contentType = v.Mime
				}
				break
			}
		}
		if key == "" {
			return nil, fmt.Errorf("variant %q not found", variant)
		}
	}
	return s.readObjectRange(ctx, key, contentType, rangeHeader)
}

// StreamAnonymousSegment serves one HLS segment of an anonymous asset. The
// name is a basename from a playlist this service rewrote; anything with a
// path in it is refused before the bucket is touched.
func (s *Service) StreamAnonymousSegment(ctx context.Context, viewerID, mediaID uuid.UUID, name, rangeHeader string) (*StreamResult, error) {
	if !validHLSName(name) {
		return nil, fmt.Errorf("invalid HLS segment reference %q", name)
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if media == nil || media.AccessScope != postgres.AccessScopeAnonymous {
		return nil, ErrNotAnonymousScope
	}
	if media.HLSMasterKey == "" {
		return nil, delivery.ErrDeliveryDenied
	}
	if err := authorizeCaptionRead(ctx, s.gate, media, viewerID); err != nil {
		return nil, err
	}
	key := strings.TrimSuffix(media.HLSMasterKey, "master.m3u8") + name
	return s.readObjectRange(ctx, key, "video/mp2t", rangeHeader)
}

func validHLSName(name string) bool {
	return name != "" && name != "." && name != ".." && !strings.ContainsAny(name, `/\\`)
}

func (s *Service) readObjectRange(ctx context.Context, key, contentType, rangeHeader string) (*StreamResult, error) {
	info, err := s.blobStore.StatObject(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("stat object: %w", err)
	}
	if contentType == "" {
		contentType = info.ContentType
	}
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	res := &StreamResult{ContentType: contentType, Size: info.Size}
	start, end, ranged, err := parseByteRange(rangeHeader, info.Size)
	if err != nil {
		return nil, err
	}
	if !ranged {
		data, err := s.blobStore.DownloadObject(ctx, key)
		if err != nil {
			return nil, fmt.Errorf("read object: %w", err)
		}
		res.Data = data
		res.Start, res.End = 0, info.Size-1
		return res, nil
	}
	data, err := s.blobStore.ReadObjectRange(ctx, key, start, end)
	if err != nil {
		return nil, fmt.Errorf("read object range: %w", err)
	}
	res.Data, res.Start, res.End, res.Partial = data, start, end, true
	return res, nil
}

// ErrRangeNotSatisfiable maps to 416.
var ErrRangeNotSatisfiable = errors.New("range not satisfiable")

// parseByteRange reads a single `bytes=start-end` range against size.
// ranged is false for no header. Only one range is honoured — what browsers
// and players send; a multi-range request is served whole.
func parseByteRange(header string, size int64) (start, end int64, ranged bool, err error) {
	header = strings.TrimSpace(header)
	if header == "" || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, nil
	}
	spec := strings.TrimPrefix(header, "bytes=")
	dash := strings.IndexByte(spec, '-')
	if dash < 0 {
		return 0, 0, false, nil
	}
	a, b := strings.TrimSpace(spec[:dash]), strings.TrimSpace(spec[dash+1:])
	switch {
	case a == "" && b == "": // "bytes=-"
		return 0, 0, false, nil
	case a == "": // suffix: last N bytes
		n, perr := strconv.ParseInt(b, 10, 64)
		if perr != nil || n <= 0 {
			return 0, 0, false, nil
		}
		if n > size {
			n = size
		}
		return size - n, size - 1, true, nil
	default:
		s, perr := strconv.ParseInt(a, 10, 64)
		if perr != nil || s < 0 {
			return 0, 0, false, nil
		}
		if s >= size {
			return 0, 0, false, ErrRangeNotSatisfiable
		}
		e := size - 1
		if b != "" {
			if v, perr := strconv.ParseInt(b, 10, 64); perr == nil && v >= s {
				if v < e {
					e = v
				}
			}
		}
		return s, e, true, nil
	}
}

// hlsSegmentURL is where an anonymous asset's playlist sends a player for a
// segment: back through this service, never a signed object URL.
func hlsSegmentURL(mediaID uuid.UUID, name string) string {
	return fmt.Sprintf("/v1/media/%s/hls-seg/%s", mediaID, name)
}

/*
	rewriteHLS turns a stored playlist into the graph a player follows. A
	master's children always point at this service's playlist route. A
	child's segments point at signed object URLs (urls) for an ordinary
	asset, and at this service's segment route for an anonymous one — the
	one place where the two kinds of asset differ on the wire.
*/
func rewriteHLS(lines []string, playlist string, mediaID uuid.UUID, urls map[string]string, anonymous bool) ([]byte, error) {
	out := make([]string, len(lines))
	copy(out, lines)
	for i, line := range out {
		name := strings.TrimSpace(line)
		if name == "" || strings.HasPrefix(name, "#") {
			continue
		}
		if playlist == "master.m3u8" {
			if !strings.HasSuffix(name, ".m3u8") || strings.ContainsAny(name, `/\\`) {
				return nil, fmt.Errorf("invalid HLS child playlist reference %q", name)
			}
			out[i] = hlsPlaylistURL(mediaID, name)
			continue
		}
		if anonymous {
			if !validHLSName(name) {
				return nil, fmt.Errorf("invalid HLS segment reference %q", name)
			}
			out[i] = hlsSegmentURL(mediaID, name)
			continue
		}
		signed, ok := urls[name]
		if !ok || signed == "" {
			return nil, fmt.Errorf("missing signed URL for HLS segment %q", name)
		}
		out[i] = signed
	}
	return []byte(strings.Join(out, "\n")), nil
}
