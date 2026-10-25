// Package mediaclient is profile-service's transport for media-service's
// keyed claim routes (POST /v1/media/internal/claims and /claims/release).
//
// # Why claims
//
// profile.profiles.avatar_media_id and cover_media_id name assets that live in
// media-service's database, with no foreign key and no lock on the asset row.
// internal/http/media_authority.go checks an asset is the owner's ready,
// approved image before it is attached, but nothing stopped the owner deleting
// it afterwards (DELETE /v1/media/{id}), leaving the profile naming nothing. A
// claim is media-service's record that a profile row holds the asset; while it
// exists the owner delete is refused (409 MEDIA_IN_USE).
//
// The protocol (claim before the write, release after the row stops naming
// the asset) is the service's, in internal/service/media_claims.go; this is
// the transport. It mirrors channel-service's internal/mediaclient.
//
// # Fail closed
//
// Anything but a 200 is ErrUnavailable, except a 404 carrying media-service's
// MEDIA_NOT_FOUND code, which is ErrNotFound. A caller that cannot claim must
// not write the reference.
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
	// ErrNotFound means media-service has no such asset (or a deleted one):
	// the claim was refused and nothing was recorded.
	ErrNotFound = errors.New("media: no such media")

	// ErrUnavailable means media-service could not be asked or did not answer
	// usefully. Never a verdict about the caller.
	ErrUnavailable = errors.New("media: media-service is unavailable")
)

// Claimant is how profile-service names itself to media-service.
const Claimant = "profile-service"

// The kinds of row that hold media here, as media-service registers them for
// Claimant. The ref of both is the profile's user id.
const (
	KindProfileAvatar = "profile_avatar" // profile.profiles.avatar_media_id
	KindProfileCover  = "profile_cover"  // profile.profiles.cover_media_id
)

// DefaultTimeout bounds one call. A user is waiting on the save.
const DefaultTimeout = 3 * time.Second

// internalKeyHeader is the credential media-service's trustedServiceCaller
// reads.
const internalKeyHeader = "X-Internal-Service-Key"

// mediaNotFoundCode is media-service's code for "no such asset". A 404 without
// it is a missing ROUTE (a media-service that predates claims), which is not a
// verdict about the asset and must not be read as one.
const mediaNotFoundCode = "MEDIA_NOT_FOUND"

// Client talks to media-service's claim routes.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// New builds a client. A blank base URL or a blank internal key yields nil:
// media-service refuses the claim routes to an unkeyed caller.
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || internalKey == "" {
		return nil
	}
	return &Client{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: DefaultTimeout}}
}

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

// Release removes the row ref's claims on mediaIDs, or all of its claims of
// kind when mediaIDs is empty. Releasing what is not claimed succeeds.
func (c *Client) Release(ctx context.Context, kind string, ref uuid.UUID, mediaIDs []uuid.UUID) error {
	return c.postClaim(ctx, "/v1/media/internal/claims/release", claimRequest{
		Claimant: Claimant, Kind: kind, Ref: ref.String(), MediaIDs: mediaIDs,
	})
}

// ReleaseExcept removes every claim the row ref holds of kind EXCEPT those on
// keep ("this row holds exactly keep"), for the reconcile tool. keep must not
// be empty; claim keep first.
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
