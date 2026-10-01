package http

// The reviewer's view of a seller's KYC documents (admin-service token only).
//
//	GET /v1/commerce/internal/admin/sellers/:sellerId/documents
//	    → {"data":[{"id","document_type","verification_status","uploaded_at","viewable"}]}
//	GET /v1/commerce/internal/admin/sellers/:sellerId/documents/:documentId/image
//	    → the image bytes, inline, never cached
//
// Both carry commerce:kyc.verify. Registered ONLY in the token family: the
// admin console reaches commerce through admin-service's signed token, and the
// internal-key family has no caller that needs a seller's identity documents.
// A route that streams a PAN card should accept exactly one credential.
//
// No media id, no document number and no URL leaves commerce here. The image
// is read by commerce from the row that belongs to the seller in the path and
// handed back as bytes; there is nothing in either response a caller could
// replay or share.

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/atpost/commerce-service/internal/media"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// AdminListSellerDocuments — GET …/internal/admin/sellers/:sellerId/documents.
func (h *Handler) AdminListSellerDocuments(c *gin.Context) {
	ctx := c.Request.Context()
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	docs, err := h.svc.AdminListSellerDocuments(ctx, sellerID)
	if errors.Is(err, postgres.ErrSellerNotFound) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "SELLER_NOT_FOUND", "seller not found", nil)
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "commerce: listing seller documents", "seller_id", sellerID, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "documents unavailable", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, docs, nil)
}

// AdminSellerDocumentImage — GET …/sellers/:sellerId/documents/:documentId/image.
//
// The image is read in full (it is capped at media.MaxImageBytes) before the
// first header is written. That costs at most 15 MB of memory for one admin
// view, and buys the one property streaming cannot: an upstream that fails or
// overruns the cap half-way is answered with an error status, never with a
// 200 and a truncated picture a reviewer might approve a seller on.
func (h *Handler) AdminSellerDocumentImage(c *gin.Context) {
	ctx := c.Request.Context()
	sellerID, ok := parseUUID(c, "sellerId")
	if !ok {
		return
	}
	documentID, ok := parseUUID(c, "documentId")
	if !ok {
		return
	}
	actor, _ := tokenActor(c)
	img, err := h.svc.AdminOpenSellerDocumentImage(ctx, sellerID, documentID, actor)
	if err != nil {
		writeDocumentImageError(c, err)
		return
	}
	defer img.Body.Close() //nolint:errcheck
	raw, err := io.ReadAll(img.Body)
	if err != nil {
		writeDocumentImageError(c, err)
		return
	}

	hdr := c.Writer.Header()
	hdr.Set("Content-Type", img.ContentType)
	hdr.Set("Content-Length", strconv.Itoa(len(raw)))
	hdr.Set("Content-Disposition", "inline")
	hdr.Set("Cache-Control", "no-store, private")
	hdr.Set("X-Content-Type-Options", "nosniff")
	c.Writer.WriteHeader(http.StatusOK)
	if _, err := io.Copy(c.Writer, bytes.NewReader(raw)); err != nil {
		slog.WarnContext(ctx, "commerce: writing a KYC document image", "document_id", documentID, "error", err)
	}
}

func writeDocumentImageError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, postgres.ErrSellerDocumentNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "DOCUMENT_NOT_FOUND", "document not found", nil)
	case errors.Is(err, service.ErrDocumentImageUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "DOCUMENT_IMAGE_UNAVAILABLE",
			"this document has no image that can be shown", nil)
	case errors.Is(err, postgres.ErrActorRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeAdminActorRequired,
			"the token does not name the acting admin", nil)
	case errors.Is(err, media.ErrNotAnImage), errors.Is(err, media.ErrImageTooLarge):
		slog.WarnContext(ctx, "commerce: media-service returned an unusable document image", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadGateway, "DOCUMENT_IMAGE_INVALID",
			"the stored image could not be served", nil)
	case errors.Is(err, media.ErrMediaUnavailable):
		slog.WarnContext(ctx, "commerce: media-service unavailable for a document image", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "MEDIA_UNAVAILABLE",
			"the image service is unavailable; try again", nil)
	default:
		slog.ErrorContext(ctx, "commerce: opening a KYC document image", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "QUERY_FAILED", "document unavailable", nil)
	}
}
