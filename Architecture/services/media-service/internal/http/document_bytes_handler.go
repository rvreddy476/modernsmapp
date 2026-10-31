package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Private documents (31 Oct 2026).
//
// GET /v1/media/internal/:mediaId/document-bytes
//
// The verified bytes of one private document (file_type "document": a PDF, a
// JPEG or a PNG), for commerce-service to stream through admin-service to the
// reviewer of a seller's KYC. Never a URL; nothing else serves a document
// (service.ViewerReadDenies).
//
// Who may call — the image-bytes shape, without its local/dev escape:
//
//   - The internal group's service key first (401 otherwise); the
//     api-gateway 404s /v1/media/internal/* at the edge.
//   - Any gateway-set end-user identity header: 403 USER_CALLER_REFUSED.
//   - A service token in X-Service-Authorization (audience "media",
//     operation media:document-bytes.read) issued by commerce-service. With
//     no token the answer is 401 SERVICE_TOKEN_REQUIRED on EVERY environment:
//     like partner evidence, the bare internal key never reads a document.
//     Anything else is 403 SERVICE_TOKEN_REJECTED.
//
// What is served: service.DocumentReader's answer (a ready document whose
// object is the one confirmed and whose first bytes are still its declared
// type), byte for byte. Every refusal is 404 MEDIA_NOT_FOUND, one answer.
//
// Headers on every answer: Cache-Control no-store, nosniff. On the 200 also:
// Content-Type from the verified type (never the stored metadata),
// Content-Disposition attachment, and a sandboxing CSP, so a PDF (or a
// polyglot posing as one) opened directly can neither run script nor be
// framed into a page.

// DocumentBytesRoute is the route within the /v1/media/internal group.
const DocumentBytesRoute = "/:mediaId/document-bytes"

// OpDocumentBytesRead is the operation a token must carry for document-bytes.
const OpDocumentBytesRead = "media:document-bytes.read"

// documentBytesIssuers is the closed set of services document-bytes answers.
// Doorstep's professional documents are images and stay on image-bytes.
var documentBytesIssuers = map[string]bool{
	IssuerCommerceService: true,
}

// DocumentCSP is the Content-Security-Policy every served document carries.
const DocumentCSP = "default-src 'none'; sandbox"

const ctxDocumentBytesCaller = "media_document_bytes_caller"

// documentBytesService reads one document.
type documentBytesService interface {
	DocumentBytes(ctx context.Context, mediaID uuid.UUID) (*service.DocumentBytes, error)
}

func (h *Handler) documentBytesSvc() documentBytesService {
	if h.documentBytes != nil {
		return h.documentBytes
	}
	return h.svc.DocumentReader()
}

// requireDocumentBytesCaller admits a commerce-service token carrying
// media:document-bytes.read, and nothing else. It runs after the group's
// internal-key check. The verifier is the image-bytes one
// (SERVICE_CALLER_COMMERCE_SERVICE_OPS must list the operation).
func (h *Handler) requireDocumentBytesCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		documentNoStore(c)
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceTokenRequired,
				"a commerce-service token is required", nil)
			c.Abort()
			return
		}
		if h.imageBytesVerifier == nil {
			slog.WarnContext(ctx, "media-service: document-bytes token refused: no service callers configured")
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		verified, err := h.imageBytesVerifier.Verify(raw, OpDocumentBytesRead, "")
		if err != nil {
			slog.WarnContext(ctx, "media-service: document-bytes token refused", "reason", err.Error())
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if !documentBytesIssuers[verified.Issuer] {
			slog.WarnContext(ctx, "media-service: document-bytes called by a service it does not answer", "issuer", verified.Issuer)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		c.Set(ctxDocumentBytesCaller, verified.Issuer)
		c.Next()
	}
}

// documentNoStore sets what every document-bytes answer carries, refusals
// included: nothing from this route is cached or sniffed.
func documentNoStore(c *gin.Context) {
	c.Header("Cache-Control", "no-store, private")
	c.Header("X-Content-Type-Options", "nosniff")
}

// documentFilename is the attachment name for a verified type. No id, no
// document kind: the name is not a place to leak either.
func documentFilename(contentType string) string {
	switch contentType {
	case service.DocumentMIMEPDF:
		return "document.pdf"
	case service.DocumentMIMEPNG:
		return "document.png"
	default:
		return "document.jpg"
	}
}

// GetDocumentBytes — GET /v1/media/internal/:mediaId/document-bytes
func (h *Handler) GetDocumentBytes(c *gin.Context) {
	ctx := c.Request.Context()
	documentNoStore(c)

	notFound := func() {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "Media not found", nil)
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		notFound()
		return
	}
	doc, err := h.documentBytesSvc().DocumentBytes(ctx, mediaID)
	if errors.Is(err, service.ErrDocumentNotFound) {
		notFound()
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "media-service: document-bytes failed", "media_id", mediaID.String(), "error", err.Error())
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeMediaUnavailable, "Media is unavailable; retry", nil)
		return
	}
	// The reader verified the type; this refuses anything it did not, so a
	// change there cannot widen what leaves here.
	if doc == nil || len(doc.Bytes) == 0 || int64(len(doc.Bytes)) > service.MaxDocumentSizeBytes ||
		service.SniffDocumentType(doc.Bytes) == "" || service.SniffDocumentType(doc.Bytes) != doc.ContentType {
		notFound()
		return
	}
	caller, _ := c.Get(ctxDocumentBytesCaller)
	callerName, _ := caller.(string)
	slog.InfoContext(ctx, "media-service: document bytes served", "media_id", mediaID.String(), "caller", callerName)
	c.Header("Content-Disposition", `attachment; filename="`+documentFilename(doc.ContentType)+`"`)
	c.Header("Content-Security-Policy", DocumentCSP)
	c.Header("Cross-Origin-Resource-Policy", "same-origin")
	c.Header("Content-Length", strconv.Itoa(len(doc.Bytes)))
	c.Data(http.StatusOK, doc.ContentType, doc.Bytes)
}
