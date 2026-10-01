package media

// Reading a KYC document's pixels, for the admin console and nobody else.
//
// ─── WHAT THIS IS FOR ───────────────────────────────────────────────────
//
// Seller approval is manual (founder, 1 Oct 2026), so the reviewer has to SEE
// the PAN card, the cancelled cheque and the rest. Until this existed no route
// returned a seller's documents to anyone: media-service's delivery gate lets
// only the uploader see such an image, which is right for every other viewer.
//
// The chain is bytes end to end, never a URL:
//
//	admin console → admin-service → commerce (this client) → media-service
//	                GET /v1/media/internal/:mediaId/image-bytes
//
// A signed URL would be a credential that outlives the view, lands in browser
// history and can be pasted anywhere. Bytes streamed through the services
// that checked the permission and wrote the audit row cannot.
//
// ─── WHO MAY CALL IT ────────────────────────────────────────────────────
//
// Only the commerce admin route that serves one document of one seller to
// an admin-service token carrying commerce:kyc.verify. Nothing in the
// buyer or seller surfaces calls this, and nothing should: the decision about
// WHICH media id to fetch is made by commerce from `seller_documents`, never
// taken from a request.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/shared/servicetoken"
	"github.com/google/uuid"
)

// MaxImageBytes caps one document image. media-service caps the same route at
// the same size; this is the commerce side of that promise, so a misbehaving
// upstream cannot make an admin request allocate without limit.
const MaxImageBytes = 15 << 20

// ImageFetchTimeout bounds one image read, body included. media-service
// decodes, orients and re-encodes the image (stripping EXIF/GPS) before it
// answers, and moves up to 15 MB; DefaultTimeout's 3 s is for 1 KB of JSON.
const ImageFetchTimeout = 15 * time.Second

// Service-token shape for the image route, as media-service verifies it:
// issuer commerce-service, audience media, operation media:image-bytes.read.
// Outside local/dev media answers 401 SERVICE_TOKEN_REQUIRED without it.
const (
	ImageBytesAudience  = "media"
	ImageBytesOperation = "media:image-bytes.read"
	tokenSubject        = "commerce-service"
	imageTokenTTL       = time.Minute
)

var (
	// ErrImageTooLarge means the upstream image exceeds MaxImageBytes.
	ErrImageTooLarge = errors.New("commerce: the document image is larger than the permitted maximum")

	// ErrNotAnImage means media-service answered with a body that is not one
	// of the raster image types a KYC document can be. SVG is refused by
	// name: it is XML that can carry script, and it is never what a seller
	// uploaded (media-service accepts JPEG, PNG and WebP for documents).
	ErrNotAnImage = errors.New("commerce: media-service did not return a raster image")
)

// allowedImageTypes are the content types this client passes on.
var allowedImageTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
	"image/avif": true,
}

// ServiceTokenSigner builds commerce's signer for media-service from the same
// key commerce signs its payments calls with (COMMERCE_SERVICE_TOKEN_KEY /
// _KID). Both blank yields nil, nil: no signing key is issued on the local
// stack, and the client then presents the internal key alone.
func ServiceTokenSigner(kid, keyB64 string) (*servicetoken.Signer, error) {
	kid, keyB64 = strings.TrimSpace(kid), strings.TrimSpace(keyB64)
	if kid == "" || keyB64 == "" {
		return nil, nil
	}
	return servicetoken.NewSignerFromBase64(tokenSubject, kid, keyB64)
}

// WithServiceToken makes the image route present a commerce service token in
// X-Service-Authorization, beside the internal key the /v1/media/internal
// group requires. nil leaves the client on the internal key alone (the local
// dev stack, where commerce has no signing key registered with media).
func (c *Client) WithServiceToken(signer *servicetoken.Signer) *Client {
	if c != nil {
		c.signer = signer
	}
	return c
}

// FetchImageBytes streams the display rendition of one image asset.
//
// The returned reader yields at most MaxImageBytes and fails with
// ErrImageTooLarge past that, so a caller that copies it to a client never
// sends a silently truncated image. The caller closes it.
//
// Errors: ErrMediaNotFound (media-service says 404 — it answers that for any
// asset that is missing, not an image, or not ready, and never says which);
// ErrNotAnImage; ErrImageTooLarge; ErrMediaUnavailable for everything else,
// including a nil client.
func (c *Client) FetchImageBytes(ctx context.Context, mediaID uuid.UUID) (io.ReadCloser, string, error) {
	if c == nil {
		return nil, "", fmt.Errorf("%w: no media-service client is configured", ErrMediaUnavailable)
	}
	if mediaID == uuid.Nil {
		return nil, "", ErrMediaNotFound
	}
	url := fmt.Sprintf("%s/v1/media/internal/%s/image-bytes", c.baseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	if c.signer != nil {
		tok, err := c.signer.Mint(ImageBytesAudience, tokenSubject, []string{ImageBytesOperation}, nil, imageTokenTTL)
		if err != nil {
			return nil, "", fmt.Errorf("%w: minting the service token: %v", ErrMediaUnavailable, err)
		}
		req.Header.Set(serviceAuthHeader, "Bearer "+tok)
	}
	req.Header.Set("Accept", "image/*")
	// NO user identity on this call. media-service refuses the route with
	// 403 USER_CALLER_REFUSED if X-User-Id, X-Verified-User-Id, X-Scopes or
	// X-Admin-Role is present: the caller is commerce acting as a service,
	// never a user. resolveChunk forwards the viewer; this deliberately does
	// not, and the request is built fresh so nothing can be inherited.

	resp, err := c.imageHTTP().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	ok := false
	defer func() {
		if !ok {
			resp.Body.Close() //nolint:errcheck
		}
	}()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrMediaNotFound
	case resp.StatusCode != http.StatusOK:
		// 401/403 here is a deployment mismatch (key or caller registration),
		// not a verdict about the document; the edge says 503 either way.
		return nil, "", fmt.Errorf("%w: status %d", ErrMediaUnavailable, resp.StatusCode)
	}

	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || !allowedImageTypes[strings.ToLower(ct)] {
		return nil, "", fmt.Errorf("%w (%q)", ErrNotAnImage, resp.Header.Get("Content-Type"))
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > MaxImageBytes {
			return nil, "", ErrImageTooLarge
		}
	}

	slog.InfoContext(ctx, "commerce: KYC document image fetched from media-service", "media_id", mediaID)
	ok = true
	return &cappedBody{rc: resp.Body, left: MaxImageBytes}, strings.ToLower(ct), nil
}

// serviceAuthHeader is the header every service in this estate reads a
// service token from (payments, dating, commerce's own admin routes).
const serviceAuthHeader = "X-Service-Authorization"

func (c *Client) imageHTTP() *http.Client {
	if c.bytesHTTP != nil {
		return c.bytesHTTP
	}
	return &http.Client{Timeout: ImageFetchTimeout}
}

// cappedBody reads at most `left` bytes and then fails, rather than ending
// early: io.LimitReader would report a clean EOF on a truncated image.
type cappedBody struct {
	rc   io.ReadCloser
	left int64
}

func (b *cappedBody) Read(p []byte) (int, error) {
	if b.left <= 0 {
		// One probe byte: an image of exactly MaxImageBytes ends here with EOF.
		var probe [1]byte
		n, err := b.rc.Read(probe[:])
		if n > 0 {
			return 0, ErrImageTooLarge
		}
		return 0, err
	}
	if int64(len(p)) > b.left {
		p = p[:b.left]
	}
	n, err := b.rc.Read(p)
	b.left -= int64(n)
	return n, err
}

func (b *cappedBody) Close() error { return b.rc.Close() }
