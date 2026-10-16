package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Admin sound library (2026-10-16); the rules are service/admin_sounds.go.
//
// Token-only routes for admin-service (audience "media", issuer
// admin-service, the act claim the acting admin), the same verification as
// the partner-evidence route: never the internal key, never a gateway
// identity header. The `internal` segment also keeps them off the edge
// (api-gateway internalroutes).
//
//	GET    /v1/media/internal/admin/sounds/library              media:sounds.read
//	GET    /v1/media/internal/admin/sounds/library/:id          media:sounds.read
//	GET    /v1/media/internal/admin/sounds/library/:id/preview  media:sounds.read  (307)
//	POST   /v1/media/internal/admin/sounds/library/uploads      media:sounds.manage
//	POST   /v1/media/internal/admin/sounds/library              media:sounds.manage (201)
//	PATCH  /v1/media/internal/admin/sounds/library/:id          media:sounds.manage
//	POST   /v1/media/internal/admin/sounds/library/:id/withdraw media:sounds.manage
const (
	AdminSoundsPrefix = "/v1/media/internal/admin/sounds/library"
	OpSoundsRead      = "media:sounds.read"
	OpSoundsManage    = "media:sounds.manage"

	ctxAdminSoundActor = "media.admin_sound_actor"
	maxAdminSoundBody  = 64 << 10
)

// adminSoundService is the slice of service.AdminSounds the routes use.
type adminSoundService interface {
	List(ctx context.Context, q service.AdminSoundListQuery) (*service.AdminSoundPage, error)
	Get(ctx context.Context, id uuid.UUID) (*postgres.AdminLibrarySound, error)
	PresignUpload(ctx context.Context, req service.LibraryUploadRequest) (*service.LibraryUpload, error)
	Create(ctx context.Context, actor uuid.UUID, req service.LibrarySoundCreate) (*postgres.AdminLibrarySound, error)
	Patch(ctx context.Context, id uuid.UUID, body []byte) (*postgres.AdminLibrarySound, error)
	Withdraw(ctx context.Context, id uuid.UUID, req service.LibrarySoundWithdraw) (*postgres.AdminLibrarySound, error)
	PreviewURL(ctx context.Context, id uuid.UUID) (string, error)
}

// WithAdminSounds replaces the feature (tests); nil means the service's.
func (h *Handler) WithAdminSounds(s adminSoundService) *Handler {
	h.adminSounds = s
	return h
}

func (h *Handler) adminSoundSvc() adminSoundService {
	if h.adminSounds != nil {
		return h.adminSounds
	}
	return h.svc.AdminSounds()
}

// RegisterAdminSoundLibrary registers the routes, verifying admin-service's
// key (kid, base64 public key). With no key every call is refused 403.
func (h *Handler) RegisterAdminSoundLibrary(r *gin.Engine, kid, pub string) error {
	verifier := servicetoken.NewVerifier("media")
	if pub != "" {
		if err := verifier.RegisterBase64("admin-service", kid, pub, []string{OpSoundsRead, OpSoundsManage}, nil); err != nil {
			return err
		}
	}
	read, manage := requireAdminSoundCaller(verifier, OpSoundsRead), requireAdminSoundCaller(verifier, OpSoundsManage)
	g := r.Group(AdminSoundsPrefix, refuseGatewayIdentity())
	g.GET("", read, h.ListAdminSounds)
	g.GET("/:soundId", read, h.GetAdminSound)
	g.GET("/:soundId/preview", read, h.PreviewAdminSound)
	g.POST("/uploads", manage, h.PresignAdminSoundUpload)
	g.POST("", manage, h.CreateAdminSound)
	g.PATCH("/:soundId", manage, h.PatchAdminSound)
	g.POST("/:soundId/withdraw", manage, h.WithdrawAdminSound)
	return nil
}

// requireAdminSoundCaller admits only an admin-service token scoped to op
// with a real acting admin.
func requireAdminSoundCaller(v *servicetoken.Verifier, op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		c.Header("Cache-Control", "no-store, private")
		c.Header("X-Content-Type-Options", "nosniff")
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceTokenRequired, "an admin-service token is required", nil)
			c.Abort()
			return
		}
		verified, err := v.Verify(raw, op, "")
		if err != nil || verified.Issuer != "admin-service" {
			reason := "wrong issuer"
			if err != nil {
				reason = err.Error()
			}
			slog.WarnContext(ctx, "media-service: admin sound token refused", "operation", op, "reason", reason)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		actor, err := uuid.Parse(verified.Actor)
		if err != nil || actor == uuid.Nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token names no acting admin", nil)
			c.Abort()
			return
		}
		c.Set(ctxAdminSoundActor, actor)
		c.Next()
	}
}

func adminSoundActor(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(ctxAdminSoundActor); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

// writeAdminSoundError answers a service error in the platform envelope.
func writeAdminSoundError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	var se *service.AdminSoundError
	if errors.As(err, &se) {
		if se.Status >= 500 {
			slog.ErrorContext(ctx, "media-service: admin sound library failed", "code", se.Code, "error", err)
		}
		api.ErrorWithContext(ctx, c.Writer, se.Status, se.Code, se.Message, nil)
		return
	}
	slog.ErrorContext(ctx, "media-service: admin sound library failed", "error", err)
	api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, service.CodeSoundLibraryUnavail, "The sound library is unavailable right now; retry", nil)
}

func adminSoundID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("soundId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, service.CodeAdminSoundValidation, "Invalid sound id", nil)
		return uuid.Nil, false
	}
	return id, true
}

// readAdminSoundBody reads a bounded JSON body.
func readAdminSoundBody(c *gin.Context) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxAdminSoundBody+1))
	if err != nil || len(body) > maxAdminSoundBody || !json.Valid(body) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, service.CodeAdminSoundValidation, "The body must be a JSON object", nil)
		return nil, false
	}
	return body, true
}

func decodeAdminSoundBody(c *gin.Context, into any) bool {
	body, ok := readAdminSoundBody(c)
	if !ok {
		return false
	}
	if err := json.Unmarshal(body, into); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, service.CodeAdminSoundValidation, "The body has a field of the wrong type", nil)
		return false
	}
	return true
}

// ListAdminSounds — GET …/library
func (h *Handler) ListAdminSounds(c *gin.Context) {
	page, err := h.adminSoundSvc().List(c.Request.Context(), service.AdminSoundListQuery{
		Status: c.Query("status"), Q: c.Query("q"), Genre: c.Query("genre"),
		Limit: c.Query("limit"), Cursor: c.Query("cursor"),
	})
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	var next any
	if page.NextCursor != "" {
		next = page.NextCursor
	}
	sounds := page.Sounds
	if sounds == nil {
		sounds = []postgres.AdminLibrarySound{}
	}
	c.JSON(http.StatusOK, gin.H{"data": sounds, "meta": gin.H{"next_cursor": next}})
}

// GetAdminSound — GET …/library/:soundId
func (h *Handler) GetAdminSound(c *gin.Context) {
	id, ok := adminSoundID(c)
	if !ok {
		return
	}
	row, err := h.adminSoundSvc().Get(c.Request.Context(), id)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, row, nil)
}

// PreviewAdminSound — GET …/library/:soundId/preview: 307 to a short-lived
// signed URL, whatever the sound's status.
func (h *Handler) PreviewAdminSound(c *gin.Context) {
	id, ok := adminSoundID(c)
	if !ok {
		return
	}
	u, err := h.adminSoundSvc().PreviewURL(c.Request.Context(), id)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store, private")
	c.Redirect(http.StatusTemporaryRedirect, u)
}

// PresignAdminSoundUpload — POST …/library/uploads
func (h *Handler) PresignAdminSoundUpload(c *gin.Context) {
	var req service.LibraryUploadRequest
	if !decodeAdminSoundBody(c, &req) {
		return
	}
	up, err := h.adminSoundSvc().PresignUpload(c.Request.Context(), req)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, up, nil)
}

// CreateAdminSound — POST …/library
func (h *Handler) CreateAdminSound(c *gin.Context) {
	var req service.LibrarySoundCreate
	if !decodeAdminSoundBody(c, &req) {
		return
	}
	row, err := h.adminSoundSvc().Create(c.Request.Context(), adminSoundActor(c), req)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, row, nil)
}

// PatchAdminSound — PATCH …/library/:soundId
func (h *Handler) PatchAdminSound(c *gin.Context) {
	id, ok := adminSoundID(c)
	if !ok {
		return
	}
	body, ok := readAdminSoundBody(c)
	if !ok {
		return
	}
	row, err := h.adminSoundSvc().Patch(c.Request.Context(), id, body)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, row, nil)
}

// WithdrawAdminSound — POST …/library/:soundId/withdraw
func (h *Handler) WithdrawAdminSound(c *gin.Context) {
	id, ok := adminSoundID(c)
	if !ok {
		return
	}
	var req service.LibrarySoundWithdraw
	if !decodeAdminSoundBody(c, &req) {
		return
	}
	row, err := h.adminSoundSvc().Withdraw(c.Request.Context(), id, req)
	if err != nil {
		writeAdminSoundError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, row, nil)
}
