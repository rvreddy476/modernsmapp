// Package media verifies that a media id a client hands to commerce-service
// actually belongs to that client.
//
// ─── THE HOLE THIS CLOSES ───────────────────────────────────────────────
//
// Commerce stores media as a bare UUID on twelve columns — a product's primary
// image, a variant image, a seller's logo and banner, and every row of
// `seller_documents`, which is where a seller's PAN card, Aadhaar and
// cancelled cheque live. Every one of those ids arrives in a request body.
// Nothing checked them.
//
// So a seller could:
//
//   - attach a competitor's product photography to their own listing, by
//     reading the media id out of the competitor's public product JSON;
//   - attach another person's uploaded PAN card or Aadhaar as their own KYC
//     document, and have it reviewed and approved under their seller account;
//   - attach a media id that had been REJECTED by moderation, or one still
//     uploading, and have the catalogue reference an asset that will never
//     render or that was removed for cause.
//
// The KYC case is the serious one. `seller_documents` is the evidence a human
// reviewer looks at before approving a seller to take money. Letting a seller
// point that row at someone else's document makes the review meaningless.
//
// ─── WHY A CLIENT AND NOT A JOIN ────────────────────────────────────────
//
// `media_assets` belongs to media-service and lives in its database.
// commerce-service has no access to it and should not: that is the service
// boundary. media-service already publishes everything needed on
// `GET /v1/media/{id}` — `uploader_id`, `processing_status`,
// `moderation_status`, `file_type` — and already enforces exactly this triple
// internally for chat attachments (`chat_attachment.go`: `uploader_id=$3 AND
// processing_status='ready' AND moderation_status='passed'`). This applies the
// same rule at the commerce boundary rather than inventing a second one.
//
// ─── FAIL CLOSED ────────────────────────────────────────────────────────
//
// If media-service cannot be reached, verification FAILS. A product creation
// blocked by a media-service outage is an availability cost; a KYC document
// accepted without a check because a health probe was flapping is a permanent
// one. `ErrMediaUnavailable` is distinct from `ErrNotYourMedia` so the edge can
// answer 503 rather than 403 — the seller is told to retry, not that they did
// something wrong.
package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrMediaNotFound means media-service has no such asset.
	ErrMediaNotFound = errors.New("commerce: no such media")

	// ErrNotYourMedia means the asset exists and belongs to someone else.
	ErrNotYourMedia = errors.New("commerce: that media was uploaded by another account")

	// ErrMediaNotReady means the upload has not finished processing.
	ErrMediaNotReady = errors.New("commerce: that media is not ready yet")

	// ErrMediaNotPassed means moderation has not passed the asset.
	ErrMediaNotPassed = errors.New("commerce: that media has not passed moderation")

	// ErrMediaWrongKind means an image was required and a video supplied, or
	// the reverse.
	ErrMediaWrongKind = errors.New("commerce: that media is the wrong kind")

	// ErrMediaUnavailable means media-service could not be reached or did not
	// answer usefully. It is deliberately NOT one of the refusals above: the
	// caller did nothing wrong and the edge must say 503, not 403.
	ErrMediaUnavailable = errors.New("commerce: media-service is unavailable")
)

// Asset is the subset of media-service's MediaAsset that commerce needs.
//
// Deliberately narrow. Decoding the whole DTO would couple this client to
// every field media-service adds, and none of the rest decides anything here.
type Asset struct {
	ID               uuid.UUID `json:"id"`
	UploaderID       uuid.UUID `json:"uploader_id"`
	FileType         string    `json:"file_type"`
	ProcessingStatus string    `json:"processing_status"`
	ModerationStatus string    `json:"moderation_status"`
}

// Client talks to media-service.
type Client struct {
	baseURL     string
	internalKey string
	http        *http.Client
}

// DefaultTimeout bounds the verification call.
//
// This runs inside product creation and seller onboarding, both of which a
// human is waiting on. A media-service that is slow rather than down must not
// turn into a request that hangs until the gateway gives up.
const DefaultTimeout = 3 * time.Second

// New builds a client. A blank base URL yields nil, and the caller decides
// what that means for its environment — see cmd/server.
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{
		baseURL:     baseURL,
		internalKey: internalKey,
		http:        &http.Client{Timeout: DefaultTimeout},
	}
}

// Get fetches one asset.
func (c *Client) Get(ctx context.Context, mediaID uuid.UUID) (*Asset, error) {
	url := fmt.Sprintf("%s/v1/media/%s", c.baseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrMediaNotFound
	case resp.StatusCode != http.StatusOK:
		// Anything else — 500, 502, a proxy error page — is unavailability,
		// not a verdict about the caller.
		return nil, fmt.Errorf("%w: status %d", ErrMediaUnavailable, resp.StatusCode)
	}

	// Bounded: a malfunctioning upstream must not be able to make this
	// process allocate without limit.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}

	// media-service wraps every response in the shared API envelope.
	var env struct {
		Data Asset `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("%w: decoding the response: %v", ErrMediaUnavailable, err)
	}
	if env.Data.ID == uuid.Nil {
		// A 200 whose body has no asset in it. Treating this as "verified"
		// would make any upstream shape change silently disable the check.
		return nil, fmt.Errorf("%w: the response contained no media asset", ErrMediaUnavailable)
	}
	return &env.Data, nil
}

// Kind narrows what a media reference is allowed to be. The empty Kind
// accepts any type.
type Kind string

const (
	KindAny      Kind = ""
	KindImage    Kind = "image"
	KindVideo    Kind = "video"
	KindDocument Kind = "document"
)

// VerifyOwned is the whole point of this package: the asset must exist, belong
// to this uploader, have finished processing, have passed moderation, and be
// the kind the caller expects.
//
// All five, every time. A partial check is the shape that let a rejected asset
// onto a listing.
func (c *Client) VerifyOwned(ctx context.Context, mediaID, uploaderID uuid.UUID, kind Kind) error {
	asset, err := c.Get(ctx, mediaID)
	if err != nil {
		return err
	}
	if asset.UploaderID != uploaderID {
		// The message never names the real uploader. Confirming who owns an
		// id would turn this refusal into an ownership oracle.
		return ErrNotYourMedia
	}
	if asset.ProcessingStatus != "ready" {
		return fmt.Errorf("%w (%s)", ErrMediaNotReady, asset.ProcessingStatus)
	}
	if asset.ModerationStatus != "passed" {
		return fmt.Errorf("%w (%s)", ErrMediaNotPassed, asset.ModerationStatus)
	}
	if kind != KindAny && !strings.EqualFold(asset.FileType, string(kind)) {
		return fmt.Errorf("%w: got %s, want %s", ErrMediaWrongKind, asset.FileType, kind)
	}
	return nil
}

// VerifyAllOwned checks several ids, and reports the FIRST refusal.
//
// It stops at the first failure rather than gathering them, because the
// alternative — reporting which of a submitted batch belonged to someone else
// — is a bulk ownership oracle.
func (c *Client) VerifyAllOwned(ctx context.Context, uploaderID uuid.UUID, kind Kind, ids ...*uuid.UUID) error {
	for _, id := range ids {
		if id == nil || *id == uuid.Nil {
			continue
		}
		if err := c.VerifyOwned(ctx, *id, uploaderID, kind); err != nil {
			return err
		}
	}
	return nil
}

// ─── Resolving ids into URLs for display ───────────────────────────────
//
// Commerce hands clients a bare media UUID and nothing else, so no product
// screen could render an image: the Android `core:commerce` module has no
// dependency on `core:media`, which holds the resolver, and adding one would
// drag the whole ExoPlayer stack into a module that needs a URL. Resolving
// server-side fixes it once for every client, iOS included.
//
// ─── FAIL SOFT, unlike verification ─────────────────────────────────────
//
// VerifyOwned fails CLOSED: an unverifiable media reference must not be
// stored, because the alternative is somebody else's identity document
// attached to a seller's KYC.
//
// This fails SOFT: an unresolvable URL leaves the field empty and the client
// shows a placeholder. A product catalogue that will not load because the
// image service is down is a worse outcome than a catalogue of grey boxes,
// and nothing here decides authorisation or money.
//
// The asymmetry is deliberate and is the point: writes are about permission,
// reads are about decoration.

// Resolved is the display half of a media asset.
type Resolved struct {
	MediaID  uuid.UUID         `json:"media_id"`
	Variants map[string]string `json:"variants,omitempty"`
	Blurhash *string           `json:"blurhash,omitempty"`
	Width    *int              `json:"width,omitempty"`
	Height   *int              `json:"height,omitempty"`
}

// URL picks the best available variant for display, largest first, falling
// back to the original.
//
// The order matters: `original` last, because on a product grid it is the
// full-resolution upload and serving it to a list of twenty is how a phone on
// a train downloads forty megabytes to draw thumbnails.
func (r Resolved) URL() string {
	for _, k := range []string{"large", "medium", "small", "thumbnail", "original"} {
		if u := r.Variants[k]; u != "" {
			return u
		}
	}
	for _, u := range r.Variants { // any variant beats nothing
		if u != "" {
			return u
		}
	}
	return ""
}

// Thumbnail is the small variant, for grids and cart lines.
func (r Resolved) Thumbnail() string {
	for _, k := range []string{"thumbnail", "small", "medium"} {
		if u := r.Variants[k]; u != "" {
			return u
		}
	}
	return r.URL()
}

// BatchLimit is media-service's own cap on POST /v1/media/batch. Exceeding it
// is a 400 for the whole request, so ResolveURLs chunks rather than truncating
// — a silently dropped tail would be a product list with images on the first
// fifty rows and none after.
const BatchLimit = 50

// ResolveURLs turns media ids into display URLs.
//
// It never returns an error. Every failure mode — media-service down, a
// malformed response, an id media-service does not know — resolves to an
// absent entry, which the caller renders as a placeholder. See the fail-soft
// note above.
func (c *Client) ResolveURLs(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]Resolved {
	out := make(map[uuid.UUID]Resolved)
	if c == nil || len(ids) == 0 {
		return out
	}

	// Deduplicate: a page of twenty products from one seller shares a logo,
	// and asking for the same id twenty times wastes the batch budget.
	seen := make(map[uuid.UUID]bool, len(ids))
	unique := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id == uuid.Nil || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}

	for start := 0; start < len(unique); start += BatchLimit {
		end := start + BatchLimit
		if end > len(unique) {
			end = len(unique)
		}
		c.resolveChunk(ctx, unique[start:end], out)
	}
	return out
}

func (c *Client) resolveChunk(ctx context.Context, ids []uuid.UUID, into map[uuid.UUID]Resolved) {
	strs := make([]string, len(ids))
	for i, id := range ids {
		strs[i] = id.String()
	}
	body, err := json.Marshal(map[string]any{"ids": strs})
	if err != nil {
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/v1/media/batch", bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		slog.WarnContext(ctx, "commerce: media URLs could not be resolved; clients will show placeholders",
			"error", err, "count", len(ids))
		return
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		slog.WarnContext(ctx, "commerce: media URL resolution returned an unexpected status",
			"status", resp.StatusCode, "count", len(ids))
		return
	}

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return
	}
	var env struct {
		Data map[string]Resolved `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		slog.WarnContext(ctx, "commerce: media URL response could not be decoded", "error", err)
		return
	}
	for k, v := range env.Data {
		id, err := uuid.Parse(k)
		if err != nil {
			continue
		}
		v.MediaID = id
		into[id] = v
	}
}
