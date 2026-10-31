package media

// Reading a private KYC document (PDF, JPEG or PNG), for the admin console
// and nobody else (31 Oct 2026).
//
// The chain is imagebytes.go's, bytes end to end and never a URL:
//
//	admin console → admin-service → commerce (this client) → media-service
//	                GET /v1/media/internal/:mediaId/document-bytes
//
// What is different from the image read:
//
//   - media-service does NOT re-encode a document; it serves the bytes the
//     seller uploaded after checking them against their declared type. So
//     this client checks again, on the bytes: the answer's type must be one
//     of the three and the first bytes must be that type (`%PDF-`, a JPEG or
//     a PNG signature). An HTML page or an SVG never leaves commerce labelled
//     as a document.
//   - The service token is required: media-service answers the route only to
//     a commerce-service token carrying media:document-bytes.read, on every
//     environment. A client without a signer does not call at all.
//   - The whole document is read (at most MaxDocumentBytes) before it is
//     returned, so a short or overlong answer is an error, never a 200 with a
//     truncated PDF.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// MaxDocumentBytes is media-service's document cap (10 MiB). A larger answer
// is refused.
const MaxDocumentBytes = 10 << 20

// DocumentBytesOperation is the operation the document read's token carries.
const DocumentBytesOperation = "media:document-bytes.read"

// Document content types, as media-service verifies and answers them.
const (
	DocumentTypePDF  = "application/pdf"
	DocumentTypeJPEG = "image/jpeg"
	DocumentTypePNG  = "image/png"
)

var (
	// ErrNotADocument means media-service answered with a type outside the
	// three, or bytes that are not the type it named.
	ErrNotADocument = errors.New("commerce: media-service did not return a PDF, JPEG or PNG document")

	// ErrDocumentTooLarge means the document exceeds MaxDocumentBytes.
	ErrDocumentTooLarge = errors.New("commerce: the document is larger than the permitted maximum")
)

// SniffDocumentType names the document type of b from its first bytes, or
// "" when it is none of the three.
func SniffDocumentType(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte("%PDF-")):
		return DocumentTypePDF
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return DocumentTypeJPEG
	case bytes.HasPrefix(b, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'}):
		return DocumentTypePNG
	}
	return ""
}

// FetchDocumentBytes reads one private document from media-service.
//
// Errors: ErrMediaNotFound (media-service's one not-found: missing, not a
// document, not ready); ErrNotADocument; ErrDocumentTooLarge;
// ErrMediaUnavailable for everything else, a nil client or a client with no
// service-token signer included.
func (c *Client) FetchDocumentBytes(ctx context.Context, mediaID uuid.UUID) ([]byte, string, error) {
	if c == nil {
		return nil, "", fmt.Errorf("%w: no media-service client is configured", ErrMediaUnavailable)
	}
	if mediaID == uuid.Nil {
		return nil, "", ErrMediaNotFound
	}
	if c.signer == nil {
		// media-service answers 401 without a token on every environment.
		return nil, "", fmt.Errorf("%w: no commerce service token is configured for media-service", ErrMediaUnavailable)
	}
	url := fmt.Sprintf("%s/v1/media/internal/%s/document-bytes", c.baseURL, mediaID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	tok, err := c.signer.Mint(ImageBytesAudience, tokenSubject, []string{DocumentBytesOperation}, nil, imageTokenTTL)
	if err != nil {
		return nil, "", fmt.Errorf("%w: minting the service token: %v", ErrMediaUnavailable, err)
	}
	req.Header.Set(serviceAuthHeader, "Bearer "+tok)
	req.Header.Set("Accept", "application/pdf, image/jpeg, image/png")
	// NO user identity on this call, exactly as FetchImageBytes.

	resp, err := c.imageHTTP().Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("%w: %v", ErrMediaUnavailable, err)
	}
	defer resp.Body.Close() //nolint:errcheck

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, "", ErrMediaNotFound
	case resp.StatusCode != http.StatusOK:
		return nil, "", fmt.Errorf("%w: status %d", ErrMediaUnavailable, resp.StatusCode)
	}
	ct, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	ct = strings.ToLower(ct)
	if err != nil || (ct != DocumentTypePDF && ct != DocumentTypeJPEG && ct != DocumentTypePNG) {
		return nil, "", fmt.Errorf("%w (%q)", ErrNotADocument, resp.Header.Get("Content-Type"))
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > MaxDocumentBytes {
			return nil, "", ErrDocumentTooLarge
		}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxDocumentBytes+1))
	if err != nil {
		return nil, "", fmt.Errorf("%w: reading the document: %v", ErrMediaUnavailable, err)
	}
	if len(data) > MaxDocumentBytes {
		return nil, "", ErrDocumentTooLarge
	}
	if len(data) == 0 || SniffDocumentType(data) != ct {
		return nil, "", fmt.Errorf("%w: the bytes are not %s", ErrNotADocument, ct)
	}
	slog.InfoContext(ctx, "commerce: KYC document fetched from media-service", "media_id", mediaID, "type", ct)
	return data, ct, nil
}
