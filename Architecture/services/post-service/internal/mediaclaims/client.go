// Package mediaclaims is post-service's side of media-service's cross-service
// media claims (media-service migration 031, POST /v1/media/internal/claims).
//
// # Why post-service needs claims at all
//
// posts, reel_drafts, events and media_assets share one database, but three
// of the columns post-service writes media ids into carry NO foreign key:
//
//	posts.cover_media_id         (migration 006: plain UUID)
//	reel_drafts.media_id         (migration 006: plain UUID)
//	reel_drafts.cover_media_id   (migration 006: plain UUID)
//	events.cover_media_id        (migration 010: plain UUID)
//	post_drafts.payload          (migration 026: JSONB; its cover_media_id
//	                              key is a media id no column can constrain)
//	playlists.cover_url          (migration 012: TEXT, the serve path
//	                              /v1/media/<id>/serve since migration 081)
//
// and one that has a foreign key that no longer refuses anything:
//
//	video_series.cover_media_id  (migration 081 made it ON DELETE SET NULL,
//	                              so the owner's delete cleared the cover)
//
// A plain UUID write takes no lock on the media row, so the uploader's
// DELETE /v1/media/{id} could remove an asset one of these rows still shows.
// A claim is media-service's record of the reference: while it exists the
// owner delete answers 409 MEDIA_IN_USE.
//
// The rest of post-service's media references are already guarded by a real
// foreign key, which media-service turns into the same 409, so they take no
// claim: post_media.media_id and post_draft_media.media_id (migration 030,
// ON DELETE RESTRICT) and stories.media_id (migration 032, RESTRICT).
// Inserting a row with a foreign key takes KEY SHARE on the media row, which
// is exactly the lock a claim takes, so those are as race-safe as a claim.
//
// This file is the transport; the protocol (claim before the write, release
// on failure, release what the row stopped holding) is
// internal/service/media_claims.go.
package mediaclaims

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Claimant is how post-service names itself to media-service. media-service
// must register it, with every kind below, in its claimKinds table
// (media-service internal/service/media_claims.go); until it does, every
// claim answers 400 CLAIM_INVALID, which this client reports as
// ErrUnavailable (fail closed).
const Claimant = "post-service"

// The kinds of row that hold media here. ref is always the row's id.
const (
	// KindPostCover: posts.cover_media_id, ref = post id.
	KindPostCover = "post_cover"
	// KindReelDraft: reel_drafts.media_id and reel_drafts.cover_media_id
	// (one holder, up to two ids), ref = draft id.
	KindReelDraft = "reel_draft"
	// KindEventCover: events.cover_media_id, ref = event id.
	KindEventCover = "event_cover"
	// KindPostDraftCover: the cover_media_id key of a composer draft's
	// post_drafts.payload, ref = draft id. A draft publishes as a post with
	// the SAME id; the post's cover is then held under KindPostCover, a
	// different (kind, ref), so neither releases the other.
	KindPostDraftCover = "post_draft_cover"
	// KindVideoSeriesCover: video_series.cover_media_id, ref = series id
	// (founder, 31 Oct 2026: series covers are claimed like other media).
	KindVideoSeriesCover = "video_series_cover"
	// KindPlaylistCover: the media id in playlists.cover_url
	// (/v1/media/<id>/serve), ref = playlist id.
	KindPlaylistCover = "playlist_cover"
)

// Kinds is every kind post-service claims, in a stable order.
var Kinds = []string{KindPostCover, KindReelDraft, KindEventCover, KindPostDraftCover, KindVideoSeriesCover, KindPlaylistCover}

var (
	// ErrNotFound: media-service has no such asset (code MEDIA_NOT_FOUND).
	// Nothing was claimed; the caller must not write the reference.
	ErrNotFound = errors.New("media claims: no such media")
	// ErrUnavailable: media-service could not be asked, or did not answer
	// usefully (including a media-service that predates claims, or one that
	// does not know this claimant). Never a verdict about the asset.
	ErrUnavailable = errors.New("media claims: media-service is unavailable")
)

// DefaultTimeout bounds one call. A person is waiting on the write.
const DefaultTimeout = 3 * time.Second

const internalKeyHeader = "X-Internal-Service-Key"

// mediaNotFoundCode is media-service's code for "no such asset". A 404
// without it is a missing ROUTE, which is not a verdict about the asset.
const mediaNotFoundCode = "MEDIA_NOT_FOUND"

// Client talks to media-service's claim routes.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client against the same media-service address and internal
// key the rest of post-service uses (MEDIA_SERVICE_URL,
// INTERNAL_SERVICE_KEY). A blank URL or key yields nil: without the key
// media-service refuses the internal route, and every claim would fail.
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || internalKey == "" {
		return nil
	}
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http: &http.Client{
			Timeout: DefaultTimeout,
			// Never follow a redirect: it would resend the internal key.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

type claimRequest struct {
	Claimant       string      `json:"claimant"`
	Kind           string      `json:"kind"`
	Ref            string      `json:"ref"`
	MediaIDs       []uuid.UUID `json:"media_ids,omitempty"`
	ExceptMediaIDs []uuid.UUID `json:"except_media_ids,omitempty"`
}

// Claim records that row ref (of kind) holds every id. All or nothing:
// ErrNotFound means one id names no asset and nothing was claimed.
// Idempotent.
func (c *Client) Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error {
	if len(mediaIDs) == 0 {
		return nil
	}
	return c.post(ctx, "/v1/media/internal/claims", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), MediaIDs: mediaIDs,
	})
}

// Release removes row ref's claims on mediaIDs, or all of its claims of kind
// when mediaIDs is empty. Releasing what is not claimed succeeds.
func (c *Client) Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error {
	return c.post(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), MediaIDs: mediaIDs,
	})
}

// ReleaseExcept removes every claim row ref holds of kind EXCEPT those on
// keep ("this row holds exactly keep"), for the reconcile tool. keep must not
// be empty (an empty keep would be "release everything" sent by mistake);
// claim keep first.
func (c *Client) ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	if len(keep) == 0 {
		return fmt.Errorf("%w: ReleaseExcept needs a non-empty keep set; use Release", ErrUnavailable)
	}
	return c.post(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), ExceptMediaIDs: keep,
	})
}

func (c *Client) post(ctx context.Context, path string, body claimRequest) error {
	if c == nil {
		return fmt.Errorf("%w: no media-service claim client configured", ErrUnavailable)
	}
	raw, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(internalKeyHeader, c.internalKey)

	resp, err := c.http.Do(req) //nolint:gosec // G704: base URL is the configured media-service address; the path is fixed
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound && errorCode(respBody) == mediaNotFoundCode {
		return ErrNotFound
	}
	// The code, never the body: a body could echo something it should not.
	return fmt.Errorf("%w: %s answered %d %s", ErrUnavailable, path, resp.StatusCode, errorCode(respBody))
}

func errorCode(body []byte) string {
	var env struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &env) != nil {
		return ""
	}
	return env.Error.Code
}
