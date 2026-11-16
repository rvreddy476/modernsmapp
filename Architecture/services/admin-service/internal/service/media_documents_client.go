package service

import (
	"bufio"
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/atpost/shared/servicetoken"
)

// Private documents (media-service internal/http/document_bytes_handler.go):
// GET /v1/media/internal/:mediaId/document-bytes?owner_id=<uploader>, audience
// "media", operation media:document-bytes.read, issuer admin-service with the
// acting admin as act (docs/designs/official-marks-console-contract.md
// section 6). The route sits in media-service's internal group, so the
// request also carries the internal service key; media-service registers
// admin-service for it under SERVICE_CALLERS (SERVICE_CALLER_ADMIN_SERVICE_*).
const (
	MediaDocumentsPrefix = "/v1/media/internal"
	// MediaDocumentBytesRead is the one scope a document-bytes token carries.
	MediaDocumentBytesRead = "media:document-bytes.read"
	// InternalServiceKeyHeader carries the internal key media's group checks.
	InternalServiceKeyHeader = "X-Internal-Service-Key"
)

// documentMagic is the first bytes each relayed document type must start
// with. A 200 whose bytes are not its declared type has its Content-Type
// replaced by documentUnverifiedType, which no document view relays.
var documentMagic = map[string][]byte{
	"application/pdf": []byte("%PDF-"),
	"image/png":       {0x89, 'P', 'N', 'G', '\r', '\n', 0x1A, '\n'},
	"image/jpeg":      {0xFF, 0xD8, 0xFF},
}

// documentUnverifiedType replaces the type of a body that failed its magic
// check.
const documentUnverifiedType = "application/x-unverified-document"

// NewMediaDocumentClient builds the client for media-service's document-bytes
// route. A nil signer yields a client whose every call returns
// ErrProductUnavailable, like every product client.
func NewMediaDocumentClient(baseURL, internalKey string, signer *servicetoken.Signer) *ProductClient {
	c := newProductClient(baseURL, MediaDocumentsPrefix, MediaAudience, signer)
	t := &mediaDocumentTransport{key: strings.TrimSpace(internalKey), base: http.DefaultTransport}
	c.httpClient.Transport = t
	c.streamClient.Transport = t
	return c
}

// mediaDocumentTransport adds the internal key to every request and checks a
// 200 document's first bytes against its declared type before anyone reads it.
type mediaDocumentTransport struct {
	key  string
	base http.RoundTripper
}

func (t *mediaDocumentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	if t.key != "" {
		r.Header.Set(InternalServiceKeyHeader, t.key)
	}
	resp, err := t.base.RoundTrip(r)
	if err != nil || resp.StatusCode != http.StatusOK {
		return resp, err
	}
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	magic, known := documentMagic[strings.ToLower(mt)]
	if err != nil || !known {
		// Not a document type: the view refuses it by type already.
		return resp, nil
	}
	br := bufio.NewReaderSize(resp.Body, 512)
	head, _ := br.Peek(len(magic))
	if !bytes.Equal(head, magic) {
		resp.Header.Set("Content-Type", documentUnverifiedType)
	}
	resp.Body = peekedBody{Reader: br, Closer: resp.Body}
	return resp, nil
}

// peekedBody reads through the buffered reader and closes the original body.
type peekedBody struct {
	io.Reader
	io.Closer
}
