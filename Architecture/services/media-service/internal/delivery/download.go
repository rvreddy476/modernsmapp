package delivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// MTube download (2026-09-27) — GET /v1/media/:id/download.
//
// OWNER ONLY since 2026-10-02 (service/download.go): a viewer never receives
// a file, so the route no longer asks the DownloadAuthorizer below and
// posts.allow_download opens it to nobody. The authorizer and
// AuthorizeDownload stay as the tested client of post-service's
// /v1/internal/media-download-allowed, consulted by no route today; what
// this file still does for the route is mark the signed URL as an
// attachment (SignDownload). The paragraphs below describe the 2026-09-27
// design.
//
// A download is a byte read with two differences from playback: the bytes
// leave the player (so the AUTHOR decides, per post, whether that is
// allowed — posts.allow_download), and the browser must be told to save
// rather than render (Content-Disposition: attachment). Neither is a
// media-service fact. The first is post-service's; the second is a
// property of the signed URL, and how it is expressed depends on who
// signs (S3/MinIO take a response-content-disposition query parameter,
// which CloudFront forwards when the cache policy passes it through).
//
// Same rules as every other delivery decision: fail closed on every
// uncertainty; an owner may always download their own upload, because the
// original was theirs before it was ever attached to a post.

// DownloadAuthorizer answers whether viewerID may download mediaID. nil is
// yes; ErrDeliveryDenied is a resolved no; anything else is unresolved.
type DownloadAuthorizer interface {
	AllowDownload(ctx context.Context, viewerID, mediaID string) error
}

// DownloadSigner is a URLSigner that can also mark the signed URL as an
// attachment. Both production signers implement it (the CloudFront signer
// below; the MinIO presigner in cmd/server).
type DownloadSigner interface {
	SignProtectedDownload(key string, ttl time.Duration, now time.Time, filename string) (string, error)
}

// ContentDispositionAttachment builds the header value the signed URL
// carries. The filename is restricted to a token-safe form: it is our own
// `<id>.mp4`, never caller input, but the guard keeps a future caller from
// smuggling a quote or a CRLF into a header.
func ContentDispositionAttachment(filename string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '-', r == '_', r == '.':
			return r
		}
		return '_'
	}, filename)
	if clean == "" {
		clean = "download"
	}
	return fmt.Sprintf("attachment; filename=\"%s\"", clean)
}

// HTTPDownloadAuthorizer asks post-service:
//
//	GET {POST_SERVICE_URL}/v1/internal/media-download-allowed?media_id=&viewer_id=
//	→ 200 {"allowed": true|false}   (bare or {"data":{...}} envelope)
//
// post-service joins posts.allow_download through post_media and runs the
// same visibility gate the media-access route uses. Unreferenced media, a
// post the viewer may not see, and a post whose author turned downloads
// off are all "allowed": false.
type HTTPDownloadAuthorizer struct {
	baseURL     string
	internalKey string
	client      *http.Client
}

// DownloadAllowedPath is the post-service route this asks.
const DownloadAllowedPath = "/v1/internal/media-download-allowed"

func NewHTTPDownloadAuthorizer(baseURL, internalKey string, client *http.Client) *HTTPDownloadAuthorizer {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Second}
	}
	return &HTTPDownloadAuthorizer{
		baseURL:     strings.TrimRight(baseURL, "/"),
		internalKey: internalKey,
		client:      client,
	}
}

func (a *HTTPDownloadAuthorizer) AllowDownload(ctx context.Context, viewerID, mediaID string) error {
	if a == nil || a.baseURL == "" {
		return fmt.Errorf("%w: no download authorizer configured", ErrDeliveryUnresolved)
	}
	if AnonymousViewer(viewerID) {
		// A download is never an anonymous read: the author's per-post
		// switch is an audience decision, and there is no audience without
		// a viewer.
		return fmt.Errorf("%w: no viewer", ErrDeliveryDenied)
	}
	q := url.Values{}
	q.Set("media_id", mediaID)
	q.Set("viewer_id", viewerID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.baseURL+DownloadAllowedPath+"?"+q.Encode(), nil)
	if err != nil {
		return fmt.Errorf("%w: build request: %v", ErrDeliveryUnresolved, err)
	}
	if a.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", a.internalKey)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrDeliveryUnresolved, err)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var out mediaAccessAnswer
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			return fmt.Errorf("%w: decode response: %v", ErrDeliveryUnresolved, err)
		}
		if !out.allowed() {
			return ErrDeliveryDenied
		}
		return nil
	case http.StatusNotFound, http.StatusForbidden:
		return ErrDeliveryDenied
	default:
		return fmt.Errorf("%w: download authority returned %d", ErrDeliveryUnresolved, resp.StatusCode)
	}
}

// WithDownloadAuthorizer wires the per-post download decision. Nil leaves
// every non-owner download unresolved (503), never allowed.
func (g *Gate) WithDownloadAuthorizer(a DownloadAuthorizer) *Gate {
	g.downloads = a
	return g
}

// AuthorizeDownload asks the download authority. The owner shortcut is
// the caller's (it has the asset row; this gate does not), so this is only
// ever reached for a viewer who is not the uploader.
func (g *Gate) AuthorizeDownload(ctx context.Context, viewerID, mediaID string) error {
	if g == nil {
		return fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	if g.downloads == nil {
		return fmt.Errorf("%w: no download authorizer configured", ErrDeliveryUnresolved)
	}
	return g.downloads.AllowDownload(ctx, viewerID, mediaID)
}

// SignDownload signs objectKey as an attachment named filename. The caller
// MUST have decided the download is allowed first (owner, or
// AuthorizeDownload); this signs, it does not decide. A public-class key is
// still signed: a stable URL cannot carry a disposition.
func (g *Gate) SignDownload(objectKey, filename string) (string, error) {
	if g == nil || g.signer == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", ErrDeliveryUnresolved)
	}
	ds, ok := g.signer.(DownloadSigner)
	if !ok {
		return "", fmt.Errorf("%w: signer cannot mark a URL as an attachment", ErrDeliveryUnresolved)
	}
	return ds.SignProtectedDownload(objectKey, MaxProtectedTTL, time.Now(), filename)
}

// SignProtectedDownload is SignProtected with `response-content-disposition`
// in the resource. CloudFront's canned policy covers the base URL INCLUDING
// its query string (everything but Expires/Signature/Key-Pair-Id), so the
// disposition is part of what is signed and cannot be edited off the URL.
// The distribution's cache policy must forward that query parameter to S3
// for the header to take effect.
func (s *Signer) SignProtectedDownload(key string, ttl time.Duration, now time.Time, filename string) (string, error) {
	// Unlike SignProtected this accepts a public-class key too: a download
	// of a public object still needs the disposition, so it is signed like
	// a protected one rather than handed out as a stable URL.
	return s.signResource(s.cdnBaseURL+"/"+key+"?"+dispositionQuery(filename), ttl, now)
}

func dispositionQuery(filename string) string {
	q := url.Values{}
	q.Set("response-content-disposition", ContentDispositionAttachment(filename))
	return q.Encode()
}
