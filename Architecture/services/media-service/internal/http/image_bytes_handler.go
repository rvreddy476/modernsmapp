package http

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Seller KYC documents, view-only (1 Oct 2026).
//
// GET /v1/media/internal/:mediaId/image-bytes
//
// The bytes of one ready image, for commerce-service to stream (through
// admin-service) onto the admin console's canvas. Never a URL.
//
// Who may call:
//
//   - The route lives in the internal group, so the internal service key is
//     required first (401 otherwise) and the api-gateway 404s the path at
//     the edge.
//   - A request carrying any gateway-set end-user identity header is refused
//     (403 USER_CALLER_REFUSED): the key alone never proves the caller is a
//     service.
//   - A service token in X-Service-Authorization (shared/servicetoken,
//     audience "media", operation media:image-bytes.read) must verify AND be
//     issued by commerce-service; anything else is 403
//     SERVICE_TOKEN_REJECTED.
//   - With no token, the legacy internal key alone is accepted ONLY on
//     local/dev (processing.IsLocalDevEnv). Elsewhere it is 401
//     SERVICE_TOKEN_REQUIRED.
//
// What is served: only an image in "ready" state that moderation did not
// reject; every other case is 404 MEDIA_NOT_FOUND, one answer. The body is
// rendered fresh by processing.RenderDisplayImage (orientation applied,
// re-encoded, so no EXIF/GPS survives), at most 15 MB, with
// Cache-Control: no-store, private and X-Content-Type-Options: nosniff.

// ImageBytesRoute is the route within the /v1/media/internal group.
const ImageBytesRoute = "/:mediaId/image-bytes"

// ServiceAuthHeader carries a sibling service's token — the header
// payments-service, commerce-service and user-service read.
const ServiceAuthHeader = "X-Service-Authorization"

// AudienceMedia is the service-token audience media-service accepts.
const AudienceMedia = "media"

// OpImageBytesRead is the operation a token must carry for image-bytes.
const OpImageBytesRead = "media:image-bytes.read"

// IssuerCommerceService is the only issuer image-bytes accepts.
const IssuerCommerceService = "commerce-service"

// callerLegacyKey names the caller in the log when only the internal key
// authenticated it.
const callerLegacyKey = "internal-key"

// Error codes for the image-bytes caller check. (A store outage answers
// CodeMediaUnavailable, declared with the dating photo routes.)
const (
	CodeServiceTokenRequired = "SERVICE_TOKEN_REQUIRED"
	CodeServiceTokenRejected = "SERVICE_TOKEN_REJECTED"
)

const ctxImageBytesCaller = "media_image_bytes_caller"

// serviceTokenVerifier is the slice of *servicetoken.Verifier used here.
type serviceTokenVerifier interface {
	Verify(token, operation, refType string) (*servicetoken.Verified, error)
}

// imageBytesService renders the display image of one asset.
type imageBytesService interface {
	DisplayImage(ctx context.Context, mediaID uuid.UUID) (*processing.DisplayImage, error)
}

// WithImageBytesAuth installs the service-token verifier for image-bytes and
// says whether the bare internal key is accepted (local/dev only). A nil
// verifier accepts no token.
func (h *Handler) WithImageBytesAuth(v *servicetoken.Verifier, acceptLegacyKey bool) *Handler {
	if v == nil {
		h.imageBytesVerifier = nil
	} else {
		h.imageBytesVerifier = v
	}
	h.imageBytesLegacyKey = acceptLegacyKey
	return h
}

func (h *Handler) imageBytesSvc() imageBytesService {
	if h.imageBytes != nil {
		return h.imageBytes
	}
	return h.svc.ImageBytesReader()
}

// requireImageBytesCaller admits commerce-service (token) or, on local/dev,
// the bare internal key. It runs after the group's internal-key check.
func (h *Handler) requireImageBytesCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			if !h.imageBytesLegacyKey {
				api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceTokenRequired,
					"a commerce-service token is required", nil)
				c.Abort()
				return
			}
			c.Set(ctxImageBytesCaller, callerLegacyKey)
			c.Next()
			return
		}
		if h.imageBytesVerifier == nil {
			slog.WarnContext(ctx, "media-service: image-bytes token refused: no service callers configured")
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		verified, err := h.imageBytesVerifier.Verify(raw, OpImageBytesRead, "")
		if err != nil {
			slog.WarnContext(ctx, "media-service: image-bytes token refused", "reason", err.Error())
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerCommerceService {
			slog.WarnContext(ctx, "media-service: image-bytes called by a non-commerce service", "issuer", verified.Issuer)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		c.Set(ctxImageBytesCaller, verified.Issuer)
		c.Next()
	}
}

// GetImageBytes — GET /v1/media/internal/:mediaId/image-bytes
func (h *Handler) GetImageBytes(c *gin.Context) {
	ctx := c.Request.Context()
	// Set on every answer, errors included: nothing from this route is cached
	// or sniffed.
	c.Header("Cache-Control", "no-store, private")
	c.Header("X-Content-Type-Options", "nosniff")

	notFound := func() {
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "Media not found", nil)
	}
	mediaID, err := uuid.Parse(c.Param("mediaId"))
	if err != nil {
		notFound()
		return
	}
	img, err := h.imageBytesSvc().DisplayImage(ctx, mediaID)
	if errors.Is(err, service.ErrImageBytesNotFound) {
		notFound()
		return
	}
	if err != nil {
		slog.ErrorContext(ctx, "media-service: image-bytes failed", "media_id", mediaID.String(), "error", err.Error())
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeMediaUnavailable, "Media is unavailable; retry", nil)
		return
	}
	if img == nil || len(img.Bytes) == 0 || len(img.Bytes) > processing.DisplayImageMaxBytes ||
		!strings.HasPrefix(img.ContentType, "image/") {
		notFound()
		return
	}
	caller, _ := c.Get(ctxImageBytesCaller)
	slog.InfoContext(ctx, "media-service: image bytes served", "media_id", mediaID.String(), "caller", fmt.Sprint(caller))
	c.Header("Content-Length", strconv.Itoa(len(img.Bytes)))
	c.Data(http.StatusOK, img.ContentType, img.Bytes)
}

// ServiceCallersFromEnv builds the service-token verifier (audience "media").
// Same shape as payments-, commerce- and user-service:
//
//	SERVICE_CALLERS=commerce-service
//	SERVICE_CALLER_COMMERCE_SERVICE_KID=c1
//	SERVICE_CALLER_COMMERCE_SERVICE_PUBKEY=<base64 ed25519 public key>
//	SERVICE_CALLER_COMMERCE_SERVICE_OPS=media:image-bytes.read
//
// A blank SERVICE_CALLERS returns (nil, nil): no token is accepted. A named
// caller with a missing key or an empty operation list is a configuration
// error, never "allow everything".
func ServiceCallersFromEnv(getenv func(string) string) (*servicetoken.Verifier, error) {
	raw := strings.TrimSpace(getenv("SERVICE_CALLERS"))
	if raw == "" {
		return nil, nil
	}
	v := servicetoken.NewVerifier(AudienceMedia)
	for _, name := range strings.Split(raw, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		prefix := "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
		kid := strings.TrimSpace(getenv(prefix + "_KID"))
		pub := strings.TrimSpace(getenv(prefix + "_PUBKEY"))
		var ops []string
		for _, p := range strings.Split(getenv(prefix+"_OPS"), ",") {
			if p = strings.TrimSpace(p); p != "" {
				ops = append(ops, p)
			}
		}
		if kid == "" || pub == "" {
			return nil, fmt.Errorf("caller %q is missing %s_KID or %s_PUBKEY", name, prefix, prefix)
		}
		if len(ops) == 0 {
			return nil, fmt.Errorf("caller %q must declare %s_OPS", name, prefix)
		}
		if err := v.RegisterBase64(name, kid, pub, ops, nil); err != nil {
			return nil, fmt.Errorf("caller %q: %w", name, err)
		}
	}
	if v.Callers() == 0 {
		return nil, fmt.Errorf("SERVICE_CALLERS produced no usable entries")
	}
	return v, nil
}
