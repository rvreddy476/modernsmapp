// Package mediaclient is live-service-v2's keyed client for the two
// media-service calls a stream cover needs: reading the asset's record (is it
// the host's own finished upload?) and media-service's claim routes (the
// stream holds it, so its owner cannot delete it from under the stream).
//
// It is channel-service's internal/mediaclient narrowed to this service:
//
//	GET  /v1/media/{id}                      uploader_id, processing/moderation status
//	POST /v1/media/internal/claims           {claimant, kind, ref, media_ids}
//	POST /v1/media/internal/claims/release   {claimant, kind, ref, media_ids? | except_media_ids?}
//
// with the internal service key and no end-user identity headers.
//
// # Fail closed
//
// Anything but a 200, or a 404 that names a missing asset, is ErrUnavailable:
// the caller did nothing wrong, and a cover must not be stored that nobody
// could check or claim. A 404 without media-service's MEDIA_NOT_FOUND code on
// the claim routes is a missing ROUTE (a media-service that predates claims),
// not a verdict about the asset.
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
	// ErrNotFound means media-service has no such asset (or will not admit
	// one exists to a service caller). Nothing was claimed.
	ErrNotFound = errors.New("media: no such media")

	// ErrUnavailable means media-service could not be asked or did not
	// answer usefully. Never a verdict about the caller.
	ErrUnavailable = errors.New("media: media-service is unavailable")
)

// Claimant is how live-service-v2 names itself to media-service.
const Claimant = "live-service-v2"

// KindLiveCover is live_streams.cover_media_id; ref = the stream id.
const KindLiveCover = "live_cover"

// Asset is the subset of media-service's record the cover check reads.
type Asset struct {
	ID               uuid.UUID `json:"id"`
	UploaderID       uuid.UUID `json:"uploader_id"`
	FileType         string    `json:"file_type"`
	ProcessingStatus string    `json:"processing_status"`
	ModerationStatus string    `json:"moderation_status"`
}

// DefaultTimeout bounds one call. A host is waiting on the save.
const DefaultTimeout = 3 * time.Second

const internalKeyHeader = "X-Internal-Service-Key"

// mediaNotFoundCode is media-service's code for "no such asset".
const mediaNotFoundCode = "MEDIA_NOT_FOUND"

// Client talks to media-service.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client. A blank base URL or key yields nil: without the key
// media-service answers as it would to an anonymous viewer, which would turn
// every lookup into a misleading "not found".
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
	// A 200 with no asset (or a different one) is not a verdict.
	if env.Data.ID != mediaID {
		return nil, fmt.Errorf("%w: the response did not contain the requested asset", ErrUnavailable)
	}
	return &env.Data, nil
}

type claimRequest struct {
	Claimant       string      `json:"claimant"`
	Kind           string      `json:"kind"`
	Ref            string      `json:"ref"`
	MediaIDs       []uuid.UUID `json:"media_ids,omitempty"`
	ExceptMediaIDs []uuid.UUID `json:"except_media_ids,omitempty"`
}

// Claim records that the row ref (of kind) holds every id. All or nothing:
// ErrNotFound means one id names no asset and nothing was claimed. Idempotent.
func (c *Client) Claim(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error {
	return c.post(ctx, "/v1/media/internal/claims", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref, MediaIDs: mediaIDs,
	})
}

// Release removes the row ref's claims on mediaIDs, or all of its claims when
// mediaIDs is empty. Releasing what is not claimed succeeds.
func (c *Client) Release(ctx context.Context, kind, ref string, mediaIDs []uuid.UUID) error {
	return c.post(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref, MediaIDs: mediaIDs,
	})
}

// ReleaseExcept removes every claim the row ref holds except those on keep
// ("this row holds exactly keep"), for the reconcile tool. keep must not be
// empty; claim keep first.
func (c *Client) ReleaseExcept(ctx context.Context, kind, ref string, keep []uuid.UUID) error {
	if len(keep) == 0 {
		return fmt.Errorf("%w: ReleaseExcept needs a non-empty keep set; use Release", ErrUnavailable)
	}
	return c.post(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref, ExceptMediaIDs: keep,
	})
}

func (c *Client) post(ctx context.Context, path string, body claimRequest) error {
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
