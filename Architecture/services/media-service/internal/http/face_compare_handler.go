package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Dating plan lane D5 — internal face comparison.
//
// POST /internal/v1/media/faces/compare is service-to-service only:
//
//   - The path is outside every api-gateway prefix (the gateway proxies
//     /v1/media, not /internal/...), so it is not reachable from the edge.
//   - A request carrying any gateway-set end-user identity header is refused
//     (403 USER_CALLER_REFUSED) whatever credential it also carries: the
//     gateway injects the internal key on every proxied request, so the key
//     alone never proves the caller is a service.
//   - Otherwise the internal service key is required (401). media-service
//     does not verify service tokens today, so the key is the credential.
//   - The route is registered only when face comparison is enabled AND an
//     internal key is configured; otherwise it does not exist (404).

// FaceComparePath is the internal compare route.
const FaceComparePath = "/internal/v1/media/faces/compare"

// Stable error codes for the compare route.
const (
	CodeUserCallerRefused      = "USER_CALLER_REFUSED"
	CodeMediaNotFound          = "MEDIA_NOT_FOUND"
	CodeSameMedia              = "SAME_MEDIA"
	CodeImageUnsupported       = "IMAGE_UNSUPPORTED"
	CodeFaceCompareUnavailable = "FACE_COMPARE_UNAVAILABLE"
)

// gatewayIdentityHeaders are the headers the api-gateway sets only from a
// verified user token.
var gatewayIdentityHeaders = []string{"X-User-Id", "X-Verified-User-Id", "X-Scopes", "X-Admin-Role"}

// refuseGatewayIdentity rejects any request carrying an end-user identity.
func refuseGatewayIdentity() gin.HandlerFunc {
	return func(c *gin.Context) {
		for _, name := range gatewayIdentityHeaders {
			if strings.TrimSpace(c.GetHeader(name)) != "" {
				api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeUserCallerRefused,
					"service-only endpoint; user requests are not accepted", nil)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

// WithFaceCompare wires the face comparison service. Nil leaves the route
// unregistered.
func (h *Handler) WithFaceCompare(fc *service.FaceCompareService) *Handler {
	h.faceCompare = fc
	return h
}

// RegisterFaceCompareRoutes registers the internal compare route when face
// comparison is wired and an internal key exists.
func (h *Handler) RegisterFaceCompareRoutes(r *gin.Engine) {
	if h.faceCompare == nil {
		return
	}
	if h.internalKey == "" {
		slog.Warn("media-service: face comparison is configured but INTERNAL_SERVICE_KEY is not — compare route NOT registered")
		return
	}
	r.POST(FaceComparePath, refuseGatewayIdentity(), sharedmiddleware.RequireInternalKey(h.internalKey), h.CompareFaces)
	if h.faceCompare.LivenessEnabled() {
		r.POST(LivenessPath, refuseGatewayIdentity(), sharedmiddleware.RequireInternalKey(h.internalKey), h.CheckLiveness)
	}
}

type compareFacesRequest struct {
	SourceMediaID   string `json:"source_media_id"`
	TargetMediaID   string `json:"target_media_id"`
	RequesterUserID string `json:"requester_user_id"`
}

// CompareFaces — POST /internal/v1/media/faces/compare.
//
// Body: {source_media_id, target_media_id, requester_user_id}.
// 200: {similarity 0-100, face_count_source, face_count_target, match,
// reason?, provider}. Both media must be images owned by requester_user_id,
// processed (ready) and moderation-passed; otherwise 404 MEDIA_NOT_FOUND,
// with no hint which condition failed.
func (h *Handler) CompareFaces(c *gin.Context) {
	ctx := c.Request.Context()
	var body compareFacesRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "invalid JSON body", nil)
		return
	}
	source, errS := uuid.Parse(strings.TrimSpace(body.SourceMediaID))
	target, errT := uuid.Parse(strings.TrimSpace(body.TargetMediaID))
	requester, errR := uuid.Parse(strings.TrimSpace(body.RequesterUserID))
	if errS != nil || errT != nil || errR != nil || source == uuid.Nil || target == uuid.Nil || requester == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"source_media_id, target_media_id and requester_user_id must be UUIDs", nil)
		return
	}
	out, err := h.faceCompare.Compare(ctx, requester, source, target)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrFaceMediaNotFound):
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "media not found", nil)
		case errors.Is(err, service.ErrFaceSameMedia):
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeSameMedia, "source and target must be different media", nil)
		case errors.Is(err, service.ErrFaceImageUnsupported):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeImageUnsupported, "image cannot be compared", nil)
		case errors.Is(err, processing.ErrFaceCompareUnavailable):
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeFaceCompareUnavailable, "face comparison is unavailable; retry later", nil)
		default:
			slog.Error("media: face compare failed", "source_media_id", source, "target_media_id", target, "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "face comparison failed", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
