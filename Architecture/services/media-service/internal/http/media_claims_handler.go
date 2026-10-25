package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Cross-service media claims (postgres/media_claims.go has the protocol).
//
//	POST /v1/media/internal/claims          {claimant, kind, ref, media_ids}
//	  200 {"claimed": n}                    every id is now claimed (idempotent)
//	  400 CLAIM_INVALID                     unknown claimant/kind, bad ref or ids
//	  404 MEDIA_NOT_FOUND                   an id names no asset; NOTHING was claimed
//	POST /v1/media/internal/claims/release  {claimant, kind, ref, media_ids? | except_media_ids?}
//	  200 {"released": n}                   absent claims are a success; with
//	                                        except_media_ids, everything else goes
//	  400 CLAIM_INVALID
//
// Internal key only, and refused when an end-user identity rides along, like
// every service-only route here. The 404 carries its code so a claimant can
// tell "no such asset" from "no such route" (a media-service that predates
// this endpoint), which it must treat as unavailable, not as a verdict.

// CodeMediaNotFound ("MEDIA_NOT_FOUND") is shared with face_compare_handler.go.
const (
	CodeClaimInvalid = "CLAIM_INVALID"
	CodeMediaInUse   = "MEDIA_IN_USE"
)

type mediaClaimsService interface {
	ClaimMedia(ctx context.Context, c postgres.MediaClaim, ids []uuid.UUID) error
	ReleaseMediaClaims(ctx context.Context, c postgres.MediaClaim, ids []uuid.UUID) (int64, error)
	ReleaseMediaClaimsExcept(ctx context.Context, c postgres.MediaClaim, keep []uuid.UUID) (int64, error)
}

func (h *Handler) claimsSvc() mediaClaimsService {
	if h.claims != nil {
		return h.claims
	}
	return h.svc
}

type mediaClaimRequest struct {
	Claimant string      `json:"claimant"`
	Kind     string      `json:"kind"`
	Ref      string      `json:"ref"`
	MediaIDs []uuid.UUID `json:"media_ids"`
	// ExceptMediaIDs (release only): release everything the holder claims
	// but these. The reconcile tool's "this row holds exactly this set".
	ExceptMediaIDs []uuid.UUID `json:"except_media_ids"`
}

func (r mediaClaimRequest) claim() postgres.MediaClaim {
	return postgres.MediaClaim{Claimant: r.Claimant, Kind: r.Kind, Ref: r.Ref}
}

func (h *Handler) registerClaimRoutes(internal *gin.RouterGroup) {
	internal.POST("/claims", refuseGatewayIdentity(), h.ClaimMedia)
	internal.POST("/claims/release", refuseGatewayIdentity(), h.ReleaseMediaClaims)
}

// ClaimMedia — POST /v1/media/internal/claims.
func (h *Handler) ClaimMedia(c *gin.Context) {
	ctx := c.Request.Context()
	var req mediaClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeClaimInvalid, "body must be {claimant, kind, ref, media_ids}", nil)
		return
	}
	err := h.claimsSvc().ClaimMedia(ctx, req.claim(), req.MediaIDs)
	switch {
	case errors.Is(err, service.ErrClaimInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeClaimInvalid, err.Error(), nil)
	case errors.Is(err, service.ErrClaimMediaNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeMediaNotFound, "media does not exist", nil)
	case err != nil:
		slog.Error("media claim failed", "claimant", req.Claimant, "kind", req.Kind, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "claim could not be recorded", nil)
	default:
		api.JSON(c.Writer, http.StatusOK, map[string]int{"claimed": len(req.MediaIDs)}, nil)
	}
}

// ReleaseMediaClaims — POST /v1/media/internal/claims/release.
func (h *Handler) ReleaseMediaClaims(c *gin.Context) {
	ctx := c.Request.Context()
	var req mediaClaimRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeClaimInvalid, "body must be {claimant, kind, ref, media_ids?}", nil)
		return
	}
	if len(req.MediaIDs) > 0 && len(req.ExceptMediaIDs) > 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeClaimInvalid, "send media_ids or except_media_ids, not both", nil)
		return
	}
	var (
		n   int64
		err error
	)
	if len(req.ExceptMediaIDs) > 0 {
		n, err = h.claimsSvc().ReleaseMediaClaimsExcept(ctx, req.claim(), req.ExceptMediaIDs)
	} else {
		n, err = h.claimsSvc().ReleaseMediaClaims(ctx, req.claim(), req.MediaIDs)
	}
	switch {
	case errors.Is(err, service.ErrClaimInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeClaimInvalid, err.Error(), nil)
	case err != nil:
		slog.Error("media claim release failed", "claimant", req.Claimant, "kind", req.Kind, "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "claims could not be released", nil)
	default:
		api.JSON(c.Writer, http.StatusOK, map[string]int64{"released": n}, nil)
	}
}

// writeDeleteMediaError answers a refused owner delete
// (DELETE /v1/media/:mediaId). Returns false for an error it does not own.
//
//	409 MEDIA_IN_USE  {"in_use_by":[{"service","kind","count"}]} when another
//	                  service has claimed the asset; no details when a
//	                  reference inside this database refused it.
func writeDeleteMediaError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	var inUse *postgres.MediaInUseError
	switch {
	case errors.As(err, &inUse):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaInUse,
			service.MediaInUseMessage(inUse.Holders),
			map[string]any{"in_use_by": service.MediaInUseDetails(inUse.Holders)})
		return true
	case errors.Is(err, service.ErrMediaReferenced):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeMediaInUse,
			service.MediaInUseMessage(nil), nil)
		return true
	}
	return false
}
