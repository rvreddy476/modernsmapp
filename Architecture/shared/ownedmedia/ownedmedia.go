// Package ownedmedia validates private, processed document uploads server-side.
package ownedmedia

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrUnavailable = errors.New("document storage is unavailable; try again shortly")
var ErrInvalid = errors.New("upload your own processed document image")

type Verifier interface {
	VerifyOwnedImage(context.Context, uuid.UUID, uuid.UUID) error
}

// VideoVerifier reads server-measured duration, never a browser assertion.
type VideoVerifier interface {
	VerifyOwnedIntroduction(context.Context, uuid.UUID, uuid.UUID) error
}

func VerifyIntroduction(ctx context.Context, verifier Verifier, owner, id uuid.UUID) error {
	v, ok := verifier.(VideoVerifier)
	if !ok {
		return ErrUnavailable
	}
	return v.VerifyOwnedIntroduction(ctx, owner, id)
}

type Func func(context.Context, uuid.UUID, uuid.UUID) error

func (f Func) VerifyOwnedImage(ctx context.Context, owner, id uuid.UUID) error {
	return f(ctx, owner, id)
}

type Client struct {
	base, key string
	http      *http.Client
}

func New(base, key string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), key: key, http: &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func (c *Client) VerifyOwnedImage(ctx context.Context, owner, id uuid.UUID) error {
	return c.verify(ctx, owner, id, "image")
}

func (c *Client) VerifyOwnedIntroduction(ctx context.Context, owner, id uuid.UUID) error {
	return c.verify(ctx, owner, id, "video")
}

func (c *Client) verify(ctx context.Context, owner, id uuid.UUID, kind string) error {
	if c == nil || c.base == "" || c.key == "" {
		return ErrUnavailable
	}
	if owner == uuid.Nil || id == uuid.Nil {
		return ErrInvalid
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/v1/media/"+id.String(), nil)
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("X-Internal-Service-Key", c.key)
	// A keyed service read does not need to impersonate an end user. The
	// returned owner is checked against the authenticated application caller.
	resp, err := c.http.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return ErrInvalid
	}
	if resp.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return ErrUnavailable
	}
	var env struct {
		Data struct {
			ID         uuid.UUID `json:"id"`
			Owner      uuid.UUID `json:"uploader_id"`
			Kind       string    `json:"file_type"`
			Processing string    `json:"processing_status"`
			Moderation string    `json:"moderation_status"`
			Key        string    `json:"storage_key"`
			Purpose    string    `json:"upload_purpose"`
			DurationMS *int64    `json:"duration_ms"`
			Size       int64     `json:"file_size_bytes"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return ErrUnavailable
	}
	a := env.Data
	if a.ID != id || a.Owner != owner || a.Kind != kind || a.Processing != "ready" || a.Moderation != "passed" || a.Key == "" || strings.HasPrefix(a.Key, "public/") || a.Purpose != "" {
		return ErrInvalid
	}
	if kind == "video" && (a.DurationMS == nil || *a.DurationMS <= 0 || *a.DurationMS > 60_000 || a.Size <= 0 || a.Size > 64<<20) {
		return ErrInvalid
	}
	return nil
}
