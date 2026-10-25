// Package mediaclient reads a media asset's record from media-service so
// channel-service can decide whether a channel update may reference it.
//
// # Why a client and not a join
//
// `media_assets` lives in media-service's database; channel-service has no
// access to it and should not. media-service already answers a keyed service
// caller on GET /v1/media/{id} with `uploader_id`, `processing_status` and
// `moderation_status` (handler.go GetMedia → MediaForService), and
// commerce-service (internal/media) and doorstep-service (internal/mediaclient)
// already read it this way. This is the same call, narrowed to the fields the
// channel-update decision uses. No new media-service route for the read.
//
// Claim and Release (bottom of this file) are the other half: media-service's
// keyed claim routes, so an asset a channel row holds cannot be deleted by its
// owner while the row still shows it.
//
// # Fail closed
//
// Anything but a 200 with an asset in it, or a 404, is ErrUnavailable: the
// caller did nothing wrong, and the update must not be stored with a media id
// nobody could check. The decision itself (owner, upload finished, not
// rejected) is the service's, in internal/service/update_media.go.
package mediaclient

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

var (
	// ErrNotFound means media-service has no such asset, or will not admit
	// one exists to a service caller (dating / anonymous scopes answer 404).
	ErrNotFound = errors.New("media: no such media")

	// ErrUnavailable means media-service could not be asked or did not answer
	// usefully. Never a verdict about the caller.
	ErrUnavailable = errors.New("media: media-service is unavailable")
)

// Asset is the subset of media-service's record the update gate reads.
type Asset struct {
	ID               uuid.UUID `json:"id"`
	UploaderID       uuid.UUID `json:"uploader_id"`
	FileType         string    `json:"file_type"`
	ProcessingStatus string    `json:"processing_status"`
	ModerationStatus string    `json:"moderation_status"`
}

// DefaultTimeout bounds one lookup. A channel admin is waiting on the post.
const DefaultTimeout = 3 * time.Second

// internalKeyHeader is the credential media-service's trustedServiceCaller
// reads.
const internalKeyHeader = "X-Internal-Service-Key"

// Client talks to media-service.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client. A blank base URL or a blank internal key yields nil:
// without the key media-service answers as it would to an anonymous viewer,
// which would turn every lookup into a misleading "not found".
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || internalKey == "" {
		return nil
	}
	return &Client{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: DefaultTimeout}}
}

// Get fetches one asset's record.
func (c *Client) Get(ctx context.Context, mediaID uuid.UUID) (*Asset, error) {
	if c == nil {
		return nil, fmt.Errorf("%w: no media-service client configured", ErrUnavailable)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/media/%s", c.baseURL, mediaID), nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	req.Header.Set(internalKeyHeader, c.internalKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var env struct {
		Data Asset `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: decoding the response: %v", ErrUnavailable, err)
	}
	// A 200 with no asset (or a different one) in it is not a verdict. Taking
	// it as one would let an upstream shape change silently disable the gate.
	if env.Data.ID != mediaID {
		return nil, fmt.Errorf("%w: the response did not contain the requested asset", ErrUnavailable)
	}
	return &env.Data, nil
}

// ── Claims (media-service media_claims, POST /v1/media/internal/claims) ──────
//
// channel-service stores media ids in its own tables, which media-service
// cannot see. A claim is media-service's record that one of those rows holds
// an asset; while it exists the owner cannot delete the asset out from under
// the row. The protocol (claim before writing, release after detaching) is the
// service's, in internal/service/media_claims.go; this is the transport.

// Claimant is how channel-service names itself to media-service.
const Claimant = "channel-service"

// The kinds of row that hold media here, as media-service registers them.
const (
	KindChannelUpdate = "channel_update" // channel_updates.media_ids, ref = update id
	KindChannelAvatar = "channel_avatar" // broadcast_channels.avatar_media_id, ref = channel id
	KindChannelBanner = "channel_banner" // broadcast_channels.banner_media_id, ref = channel id
)

// mediaNotFoundCode is media-service's code for "no such asset". A 404 without
// it is a missing ROUTE (a media-service that predates claims), which is not a
// verdict about the asset and must not be read as one.
const mediaNotFoundCode = "MEDIA_NOT_FOUND"

type claimRequest struct {
	Claimant string      `json:"claimant"`
	Kind     string      `json:"kind"`
	Ref      string      `json:"ref"`
	MediaIDs []uuid.UUID `json:"media_ids,omitempty"`
	// ExceptMediaIDs (release only): release everything but these.
	ExceptMediaIDs []uuid.UUID `json:"except_media_ids,omitempty"`
}

// Claim records that the row ref (of kind) holds every id. All or nothing:
// ErrNotFound means one id names no asset and nothing was claimed. Idempotent.
func (c *Client) Claim(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error {
	return c.postClaim(ctx, "/v1/media/internal/claims", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), MediaIDs: mediaIDs,
	})
}

// Release removes the row ref's claims on mediaIDs, or all of its claims when
// mediaIDs is empty. Releasing what is not claimed succeeds.
func (c *Client) Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error {
	return c.postClaim(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), MediaIDs: mediaIDs,
	})
}

// ReleaseExcept removes every claim the row ref holds EXCEPT those on keep
// ("this row holds exactly keep"), for the reconcile tool. keep must not be
// empty; claim keep first.
func (c *Client) ReleaseExcept(ctx context.Context, kind string, ref uuid.UUID, keep []uuid.UUID) error {
	if len(keep) == 0 {
		return fmt.Errorf("%w: ReleaseExcept needs a non-empty keep set; use Release", ErrUnavailable)
	}
	return c.postClaim(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), ExceptMediaIDs: keep,
	})
}

func (c *Client) postClaim(ctx context.Context, path string, body claimRequest) error {
	if c == nil {
		return fmt.Errorf("%w: no media-service client configured", ErrUnavailable)
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

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusOK {
		return nil
	}
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusNotFound {
		var env struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		if json.Unmarshal(respBody, &env) == nil && env.Error.Code == mediaNotFoundCode {
			return ErrNotFound
		}
	}
	return fmt.Errorf("%w: %s status %d", ErrUnavailable, path, resp.StatusCode)
}
