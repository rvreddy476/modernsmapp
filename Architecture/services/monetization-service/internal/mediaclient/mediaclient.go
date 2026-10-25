// Package mediaclient is monetization-service's keyed client for
// media-service's claim routes (channel-service's internal/mediaclient,
// claims only):
//
//	POST /v1/media/internal/claims           {claimant, kind, ref, media_ids}
//	POST /v1/media/internal/claims/release   {claimant, kind, ref, media_ids? | except_media_ids?}
//
// with the internal service key and no end-user identity headers. A claim is
// media-service's record that a row here (a fundraiser's cover, an invoice's
// PDF) holds an asset; while it exists the asset's owner cannot delete it.
//
// Today nothing in this service writes those columns (store.ErrMediaClaimRequired
// refuses it); the client serves cmd/media-claims, which claims what legacy
// rows hold, and is what a future writer claims through first.
//
// # Fail closed
//
// Anything but a 200, or a 404 carrying media-service's MEDIA_NOT_FOUND code,
// is ErrUnavailable. A bare 404 is a media-service without the claim routes,
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

// Claimant is how monetization-service names itself to media-service.
const Claimant = "monetization-service"

// The kinds of row that hold media here, as media-service registers them.
const (
	KindFundraiserCover = "fundraiser_cover" // fundraisers.cover_media_id, ref = fundraiser id
	KindInvoicePDF      = "invoice_pdf"      // invoices.pdf_media_id, ref = invoice id
)

// DefaultTimeout bounds one call.
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

// New builds a client. A blank base URL or key yields nil: media-service
// refuses an unkeyed claim, and a nil client answers ErrUnavailable.
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" || internalKey == "" {
		return nil
	}
	return &Client{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: DefaultTimeout}}
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
