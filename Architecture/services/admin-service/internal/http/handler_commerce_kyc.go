package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"

	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Seller KYC documents, view-only (pinned 1 Oct 2026). The console lists a
// seller's documents and views one as image BYTES it draws on a watermarked
// canvas: no URL, no redirect, no download.
//
//	GET /v1/admin/commerce/sellers/:sellerId/documents
//	    commerce:kyc.verify; an ordinary audited read (one row, seller.kyc_documents)
//	GET /v1/admin/commerce/sellers/:sellerId/documents/:documentId/view
//	    commerce:kyc.verify + step-up; ONE row per call (seller.kyc_document_view,
//	    target the seller, detail {document_id, document_type})
//
// The view's order is fixed: the gate (permission, step-up) → resolve the
// document's type from commerce's list, which also proves it is this
// seller's → open commerce's image stream and check its status, type and
// size → write the audit row → only then send the first byte. A refusal at
// any earlier step is recorded by the gate as usual (one row, not success);
// once the row is written the gate writes no second one, so a stream that
// breaks part-way is still on the trail as a view. If the row cannot be
// written, no byte is sent (503 AUDIT_UNAVAILABLE).
const (
	opSellerKYCDocuments    = "seller.kyc_documents"
	opSellerKYCDocumentView = "seller.kyc_document_view"

	CodeDocumentNotFound    = "DOCUMENT_NOT_FOUND"
	CodeDocumentTooLarge    = "DOCUMENT_TOO_LARGE"
	CodeDocumentUnsupported = "DOCUMENT_UNSUPPORTED"
	CodeUpstreamError       = "UPSTREAM_ERROR"

	// maxKYCDocumentBytes caps one viewed document (media-service's own cap).
	maxKYCDocumentBytes = 15 << 20
)

// kycImageTypes are the only types a KYC document is (media-service refuses
// anything else at upload); an SVG or HTML answer is never relayed.
var kycImageTypes = map[string]bool{"image/jpeg": true, "image/png": true, "image/webp": true}

// kycViewHeaders are set on every streamed document, exactly as pinned.
var kycViewHeaders = [][2]string{
	{"Content-Disposition", "inline"},
	{"Cache-Control", "no-store, private, max-age=0"},
	{"Pragma", "no-cache"},
	{"X-Content-Type-Options", "nosniff"},
	{"Content-Security-Policy", "default-src 'none'; sandbox"},
	{"Cross-Origin-Resource-Policy", "same-origin"},
}

// kycDocument is the only shape the console receives: commerce's list,
// re-encoded field by field, so a media id, document number or URL could
// never pass through even if commerce sent one.
type kycDocument struct {
	ID                 string `json:"id"`
	DocumentType       string `json:"document_type"`
	VerificationStatus string `json:"verification_status"`
	UploadedAt         string `json:"uploaded_at"`
	Viewable           bool   `json:"viewable"`
}

var errUnreadableDocuments = errors.New("commerce answered an unreadable document list")

// fetchKYCDocuments reads commerce's document list for a seller as the gate's
// admin. err is a transport/configuration failure or an unreadable 200; a
// refusal is a non-200 resp with a nil err.
func fetchKYCDocuments(c *gin.Context, p product, permission, sellerID string) ([]kycDocument, service.ProductResponse, error) {
	resp, err := p.client.Do(productContext(c), service.ProductRequest{
		Method: http.MethodGet, Path: "/sellers/" + sellerID + "/documents",
		Permission: permission, Actor: actorFrom(c),
	})
	if err != nil || resp.Status != http.StatusOK {
		return nil, resp, err
	}
	var env struct {
		Data []kycDocument `json:"data"`
	}
	if err := json.Unmarshal(resp.Body, &env); err != nil {
		return nil, resp, errUnreadableDocuments
	}
	docs := make([]kycDocument, 0, len(env.Data))
	for _, d := range env.Data {
		id, err := uuid.Parse(d.ID)
		if err != nil {
			continue
		}
		d.ID = id.String()
		docs = append(docs, d)
	}
	return docs, resp, nil
}

// kycPath reads the seller (and, for a view, the document) id. Both are
// commerce uuids; anything else is refused before commerce is called.
func kycPath(c *gin.Context, withDocument bool) (seller, document string, ok bool) {
	s, err := uuid.Parse(c.Param("sellerId"))
	if err != nil {
		return "", "", false
	}
	if !withDocument {
		return s.String(), "", true
	}
	d, err := uuid.Parse(c.Param("documentId"))
	if err != nil {
		return "", "", false
	}
	return s.String(), d.String(), true
}

func kycPermission(c *gin.Context) (string, bool) {
	req, ok := effectiveRequirement(c)
	if !ok || req.Permission == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
		return "", false
	}
	return req.Permission, true
}

func upstreamError(c *gin.Context, info *auditInfo, status int, msg string) {
	info.outcome = postgres.AuditOutcomeFailure
	if status != 0 {
		info.set("upstream_status", status)
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadGateway, CodeUpstreamError, msg, nil)
}

// listSellerKYCDocuments answers GET .../sellers/:sellerId/documents.
func (h *Handler) listSellerKYCDocuments(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		info := auditFrom(c)
		seller, _, ok := kycPath(c, false)
		if !ok {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		info.targetType, info.targetID = "seller", seller
		perm, ok := kycPermission(c)
		if !ok {
			return
		}
		docs, resp, err := fetchKYCDocuments(c, p, perm, seller)
		switch {
		case errors.Is(err, errUnreadableDocuments):
			upstreamError(c, info, resp.Status, "The owning service answered with an unreadable document list")
		case err != nil:
			writeProduct(c, info, p.label, resp, err, false)
		case resp.Status >= 300 && resp.Status < 400:
			// Never a redirect: the console receives bytes or nothing.
			upstreamError(c, info, resp.Status, "The owning service answered with a redirect")
		case resp.Status != http.StatusOK:
			writeProduct(c, info, p.label, resp, nil, false)
		default:
			c.Header("Cache-Control", "no-store, private, max-age=0")
			api.JSON(c.Writer, http.StatusOK, docs, nil)
		}
	}
}

// viewSellerKYCDocument answers GET .../documents/:documentId/view with the
// document's image bytes. See the order at the top of this file.
func (h *Handler) viewSellerKYCDocument(p product) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		info := auditFrom(c)
		seller, document, ok := kycPath(c, true)
		if !ok {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidPathParam, "Invalid path parameter", nil)
			return
		}
		// The target is the seller whose document was viewed, not the document.
		info.targetType, info.targetID = "seller", seller
		info.set("document_id", document)
		perm, ok := kycPermission(c)
		if !ok {
			return
		}
		notFound := func() {
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeDocumentNotFound, "Document not found", nil)
		}

		// 1. The document's type, and proof it is this seller's.
		docs, resp, err := fetchKYCDocuments(c, p, perm, seller)
		switch {
		case errors.Is(err, errUnreadableDocuments):
			upstreamError(c, info, resp.Status, "The owning service answered with an unreadable document list")
			return
		case err != nil:
			writeProduct(c, info, p.label, resp, err, false)
			return
		case resp.Status == http.StatusNotFound:
			info.set("upstream_status", resp.Status)
			notFound()
			return
		case resp.Status != http.StatusOK:
			upstreamError(c, info, resp.Status, "The owning service did not list the documents")
			return
		}
		var doc *kycDocument
		for i := range docs {
			if docs[i].ID == document {
				doc = &docs[i]
				break
			}
		}
		if doc == nil || !doc.Viewable {
			if doc != nil {
				info.set("document_type", doc.DocumentType)
				info.set("viewable", false)
			}
			notFound()
			return
		}
		info.set("document_type", doc.DocumentType)

		// 2. Open the image and judge it before anything is sent or recorded
		// as a view.
		up, err := p.client.Stream(productContext(c), service.ProductRequest{
			Method: http.MethodGet, Path: "/sellers/" + seller + "/documents/" + document + "/image",
			Permission: perm, Actor: actorFrom(c),
		})
		if err != nil {
			writeProduct(c, info, p.label, service.ProductResponse{}, err, false)
			return
		}
		defer up.Body.Close()
		switch {
		case up.StatusCode == http.StatusNotFound:
			info.set("upstream_status", up.StatusCode)
			notFound()
			return
		case up.StatusCode != http.StatusOK:
			// Includes every 3xx: a redirect is never followed or relayed.
			upstreamError(c, info, up.StatusCode, "The owning service did not return the document")
			return
		}
		mt, _, err := mime.ParseMediaType(up.Header.Get("Content-Type"))
		if err != nil || !kycImageTypes[mt] {
			info.outcome = postgres.AuditOutcomeFailure
			info.set("error", "unsupported content type")
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, CodeDocumentUnsupported,
				"The owning service returned something that is not a document image", nil)
			return
		}
		tooLarge := func() {
			info.outcome = postgres.AuditOutcomeFailure
			info.set("error", "document over the size cap")
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, CodeDocumentTooLarge, "The document is too large to view", nil)
		}
		length := up.ContentLength
		var body io.Reader = up.Body
		if length > maxKYCDocumentBytes {
			tooLarge()
			return
		}
		if length < 0 {
			// Unknown length (chunked): read at most the cap before answering,
			// so an oversize document is refused whole instead of cut short
			// behind a 200 the browser would take for a complete image.
			buf, err := io.ReadAll(io.LimitReader(up.Body, maxKYCDocumentBytes+1))
			if err != nil {
				info.set("error", "document read failed")
				upstreamError(c, info, 0, "The owning service did not return the document")
				return
			}
			if len(buf) > maxKYCDocumentBytes {
				tooLarge()
				return
			}
			body, length = bytes.NewReader(buf), int64(len(buf))
		}
		if length == 0 {
			upstreamError(c, info, 0, "The owning service returned an empty document")
			return
		}

		// 3. The audit row, before the first byte.
		if err := h.gate.recordNow(c, info, http.StatusOK); err != nil {
			info.outcome = postgres.AuditOutcomeFailure
			info.set("error", "audit row not written; document withheld")
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeAuditUnavailable,
				"The view could not be recorded, so the document was not shown", nil)
			return
		}

		// 4. The bytes, with a declared length: a stream that breaks part-way
		// leaves the browser short of Content-Length, which it treats as a
		// failed load, never as a whole image.
		hdr := c.Writer.Header()
		hdr.Set("Content-Type", mt)
		hdr.Set("Content-Length", strconv.FormatInt(length, 10))
		for _, kv := range kycViewHeaders {
			hdr.Set(kv[0], kv[1])
		}
		c.Status(http.StatusOK)
		c.Writer.WriteHeaderNow()
		if n, err := io.CopyN(c.Writer, body, length); err != nil {
			slog.WarnContext(ctx, "kyc document stream ended early",
				"seller_id", seller, "document_id", document, "sent", n, "want", length, "error", err)
		}
	}
}
