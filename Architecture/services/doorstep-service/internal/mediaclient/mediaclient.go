// Package mediaclient verifies that a media id a professional hands to
// doorstep-service (profile photo, selfie, police certificate, trade
// certificate) is their own, processed, moderation-passed upload of the
// expected kind.
//
// Copied from commerce-service internal/media (Get + VerifyOwned): the asset
// is read from media-service's GET /v1/media/{id} with the internal key (a
// trusted service caller), and must exist, belong to the caller, be ready,
// have passed moderation and be of an allowed file type. Without this a
// professional could attach someone else's police certificate to their own
// application, and the admin review would be meaningless.
//
// Fail closed: media-service unreachable is ErrUnavailable (the route answers
// 503), never "verified".
package mediaclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound    = errors.New("media: no such media")
	ErrNotYours    = errors.New("media: uploaded by another account")
	ErrNotReady    = errors.New("media: not ready yet")
	ErrNotPassed   = errors.New("media: has not passed moderation")
	ErrWrongKind   = errors.New("media: wrong kind")
	ErrUnavailable = errors.New("media: media-service is unavailable")
)

// Kind is media-service's file_type.
type Kind string

const (
	KindImage    Kind = "image"
	KindDocument Kind = "document"
)

// Verifier is what the service needs.
type Verifier interface {
	VerifyOwned(ctx context.Context, mediaID, owner uuid.UUID, kinds ...Kind) error
}

type asset struct {
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
	minter      TokenMinter
	photoMinter TokenMinter
}

// New builds a client; a blank base URL yields nil (the caller refuses).
func New(baseURL, internalKey string) *Client {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &Client{baseURL: baseURL, internalKey: internalKey, http: &http.Client{Timeout: 3 * time.Second}}
}

func (c *Client) get(ctx context.Context, id uuid.UUID) (*asset, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/media/%s", c.baseURL, id), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: unreachable", ErrUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, ErrUnavailable
	}
	var env struct {
		Data asset `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || env.Data.ID == uuid.Nil {
		return nil, fmt.Errorf("%w: no media asset in the response", ErrUnavailable)
	}
	return &env.Data, nil
}

// VerifyOwned checks existence, ownership, processing, moderation and kind
// (any of kinds; none = any). The refusal never names the real uploader.
func (c *Client) VerifyOwned(ctx context.Context, mediaID, owner uuid.UUID, kinds ...Kind) error {
	if c == nil {
		return ErrUnavailable
	}
	a, err := c.get(ctx, mediaID)
	if err != nil {
		return err
	}
	if a.UploaderID != owner {
		return ErrNotYours
	}
	if a.ProcessingStatus != "ready" {
		return ErrNotReady
	}
	if a.ModerationStatus != "passed" {
		return ErrNotPassed
	}
	if len(kinds) == 0 {
		return nil
	}
	for _, k := range kinds {
		if strings.EqualFold(a.FileType, string(k)) {
			return nil
		}
	}
	return ErrWrongKind
}

// ---- document image bytes (admin review) ----
//
// The admin console sees a professional's police certificate, trade
// certificate or selfie as BYTES, never a URL (the commerce KYC pattern):
//
//	admin console → admin-service → doorstep → media-service
//	                GET /v1/media/internal/:mediaId/image-bytes
//
// doorstep decides which media id from doorstep.pro_documents, never from
// the request. The call carries the internal key and, when doorstep has a
// signing key, a doorstep-service token (audience media, operation
// media:image-bytes.read); no user identity header ever.

// Image-bytes token shape media-service verifies.
const (
	ImageBytesAudience  = "media"
	ImageBytesOperation = "media:image-bytes.read"
	// MaxImageBytes caps one document image (media-service's own cap).
	MaxImageBytes = 15 << 20
)

// ErrNotAnImage: media-service answered with something that is not a raster
// image (SVG is refused by name: XML that can carry script).
var ErrNotAnImage = errors.New("media: not a raster image")

var rasterTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true, "image/gif": true, "image/avif": true}

// TokenMinter mints doorstep's service token for media-service (audience
// ImageBytesAudience, operation ImageBytesOperation).
type TokenMinter func() (string, error)

// ImageFetcher reads one image's display bytes.
type ImageFetcher interface {
	FetchImage(ctx context.Context, mediaID uuid.UUID) ([]byte, string, error)
}

const PhotoPrepareOperation = "media:doorstep-photo.prepare"

// VisitPhotoPreparer verifies ownership and atomically disables public delivery.
type VisitPhotoPreparer interface {
	PrepareVisitPhoto(context.Context, uuid.UUID, uuid.UUID) error
}

func (c *Client) WithPhotoScopeToken(m TokenMinter) *Client {
	if c != nil {
		c.photoMinter = m
	}
	return c
}

func (c *Client) PrepareVisitPhoto(ctx context.Context, id, owner uuid.UUID) error {
	if c == nil {
		return ErrUnavailable
	}
	body, _ := json.Marshal(map[string]uuid.UUID{"owner_id": owner})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/media/internal/"+id.String()+"/doorstep-photo", bytes.NewReader(body))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", c.internalKey)
	if c.photoMinter != nil {
		token, err := c.photoMinter()
		if err != nil {
			return ErrUnavailable
		}
		req.Header.Set("X-Service-Authorization", "Bearer "+token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return ErrUnavailable
}

// WithImageToken makes FetchImage present a doorstep-service token. nil
// leaves the internal key alone (accepted by media-service on local/dev
// only).
func (c *Client) WithImageToken(m TokenMinter) *Client {
	if c != nil {
		c.minter = m
	}
	return c
}

// FetchImage returns the display rendition of one image and its content
// type. Errors: ErrNotFound (missing, not an image, not ready: media-service
// never says which), ErrNotAnImage, ErrUnavailable (everything else,
// including a 401/403 deployment mismatch).
func (c *Client) FetchImage(ctx context.Context, mediaID uuid.UUID) ([]byte, string, error) {
	if c == nil {
		return nil, "", ErrUnavailable
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/v1/media/internal/%s/image-bytes", c.baseURL, mediaID), nil)
	if err != nil {
		return nil, "", ErrUnavailable
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if c.minter != nil {
		tok, err := c.minter()
		if err != nil {
			return nil, "", fmt.Errorf("%w: mint service token", ErrUnavailable)
		}
		req.Header.Set("X-Service-Authorization", "Bearer "+tok)
	}
	req.Header.Set("Accept", "image/*")
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: unreachable", ErrUnavailable)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !rasterTypes[strings.ToLower(ct)] {
		return nil, "", ErrNotAnImage
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxImageBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: read", ErrUnavailable)
	}
	if len(body) == 0 || len(body) > MaxImageBytes {
		return nil, "", ErrNotAnImage
	}
	return body, strings.ToLower(ct), nil
}
