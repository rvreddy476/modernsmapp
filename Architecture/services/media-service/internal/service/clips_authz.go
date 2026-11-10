package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Clip authorization.
//
// WRITE. POST /v1/clips/:postId checked that the caller owned the media in
// the sequence but never that the post was the caller's, so a caller could
// write (or pre-claim) the clip sequence of a post that was not theirs.
// media-service does not own posts, so it asks post-service which posts
// attach each clip's media (GET /v1/internal/posts/by-media/:mediaId, the
// route search-service already uses). Post-service only lets an author attach
// media that author uploaded, so "this post attaches media I uploaded" is the
// proof of authorship available from here. Anything short of a plain yes —
// no lookup wired, lookup failing, the post not attaching the media — refuses.
//
// READ. GET /v1/clips/:postId returned the sequence of any post id,
// private and draft posts included. A clip row is a pointer to a media
// asset, so each is answered to the asset's audience: the same
// AuthorizeMediaRead gate the caption reads use. Clips whose asset the
// viewer may not read are dropped, so a denial reads as "no clips".

// ErrPostNotAuthored means the post could not be shown to be the actor's.
// It wraps ErrNotMediaOwner so handlers answer 403.
var ErrPostNotAuthored = fmt.Errorf("%w: the post does not attach this media", ErrNotMediaOwner)

// PostMediaLinks answers which posts attach a media asset.
type PostMediaLinks interface {
	PostIDsByMedia(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error)
}

// WithPostMediaLinks wires the post lookup SaveMediaClips needs. Without it
// every clip write is refused.
func (s *Service) WithPostMediaLinks(l PostMediaLinks) *Service {
	s.postLinks = l
	return s
}

// assertClipsOnPost requires every distinct media asset in clips to be
// attached to postID, as post-service reports it. Fails closed.
func assertClipsOnPost(ctx context.Context, links PostMediaLinks, postID uuid.UUID, clips []postgres.MediaClip) error {
	seen := map[uuid.UUID]bool{}
	for _, clip := range clips {
		if seen[clip.MediaAssetID] {
			continue
		}
		seen[clip.MediaAssetID] = true
		if links == nil {
			return fmt.Errorf("%w: post lookup not configured", delivery.ErrDeliveryUnresolved)
		}
		ids, err := links.PostIDsByMedia(ctx, clip.MediaAssetID)
		if err != nil {
			return fmt.Errorf("%w: post lookup: %v", delivery.ErrDeliveryUnresolved, err)
		}
		found := false
		for _, id := range ids {
			if id == postID {
				found = true
				break
			}
		}
		if !found {
			return ErrPostNotAuthored
		}
	}
	return nil
}

// ViewerClips is GET /v1/clips/:postId: the post's clips whose media viewerID
// may read. authorize is AuthorizeMediaRead in production.
func (s *Service) ViewerClips(ctx context.Context, viewerID, postID uuid.UUID) ([]postgres.MediaClip, error) {
	clips, err := s.pgStore.GetMediaClips(ctx, postID)
	if err != nil {
		return nil, err
	}
	return filterReadableClips(ctx, s.AuthorizeMediaRead, viewerID, clips)
}

func filterReadableClips(
	ctx context.Context,
	authorize func(ctx context.Context, viewerID, mediaID uuid.UUID) error,
	viewerID uuid.UUID,
	clips []postgres.MediaClip,
) ([]postgres.MediaClip, error) {
	verdict := map[uuid.UUID]bool{}
	out := make([]postgres.MediaClip, 0, len(clips))
	for _, clip := range clips {
		ok, known := verdict[clip.MediaAssetID]
		if !known {
			if err := authorize(ctx, viewerID, clip.MediaAssetID); err != nil {
				if !errors.Is(err, delivery.ErrDeliveryDenied) {
					return nil, err
				}
				ok = false
			} else {
				ok = true
			}
			verdict[clip.MediaAssetID] = ok
		}
		if ok {
			out = append(out, clip)
		}
	}
	return out, nil
}

// HTTPPostMediaLinks reads post-service's by-media route.
type HTTPPostMediaLinks struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// NewHTTPPostMediaLinks asks post-service at baseURL.
func NewHTTPPostMediaLinks(baseURL, internalKey string, client *http.Client) *HTTPPostMediaLinks {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return &HTTPPostMediaLinks{baseURL: strings.TrimRight(baseURL, "/"), internalKey: internalKey, client: client}
}

// PostIDsByMedia returns the ids of posts attaching mediaID. Every failure,
// including a body that is not the contract, is an error — never "no posts".
func (h *HTTPPostMediaLinks) PostIDsByMedia(ctx context.Context, mediaID uuid.UUID) ([]uuid.UUID, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		h.baseURL+"/v1/internal/posts/by-media/"+url.PathEscape(mediaID.String()), nil)
	if err != nil {
		return nil, err
	}
	if h.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", h.internalKey)
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("post-service by-media: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("post-service by-media returned %d", resp.StatusCode)
	}
	var wire struct {
		PostIDs *[]uuid.UUID `json:"post_ids"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&wire); err != nil {
		return nil, fmt.Errorf("post-service by-media body: %w", err)
	}
	if wire.PostIDs == nil {
		return nil, errors.New("post-service by-media answered without post_ids")
	}
	return *wire.PostIDs, nil
}
