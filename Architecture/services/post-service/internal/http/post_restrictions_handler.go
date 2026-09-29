package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/moderationcap"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Restriction commands and the reconciliation read (Copyright Match plan,
// sections 3, 6.2, 9.2).
//
//	POST /v1/posts/internal/restrictions            internal key + post_restriction capability
//	GET  /v1/posts/internal/restrictions?...        internal key + service token post:restrictions.read (trust-safety)
//
// Both sit behind the internal-key gate every /v1 route gets (401 without
// it). The command is judged by the signed capability alone: no header
// identity is read, and no other caller or key can produce one.

const (
	// IssuerTrustSafety is the only caller whose service tokens reach the
	// reconciliation read.
	IssuerTrustSafety = moderationcap.IssuerTrustSafety
	// OpRestrictionsRead is the service-token operation the read requires.
	OpRestrictionsRead = "post:restrictions.read"

	CodeInvalidRestrictionClaims = "INVALID_CLAIMS"
	CodeSourceNotEnabled         = "SOURCE_NOT_ENABLED"
	CodeSubjectNotFound          = "SUBJECT_NOT_FOUND"
	CodeSubjectMismatch          = "SUBJECT_MISMATCH"
	CodeStaleCaseRevision        = "STALE_CASE_REVISION"
	CodeStateMismatch            = "STATE_MISMATCH"
)

// restrictionStore is what the handler needs from the service; the unit
// tests substitute a fake, the integration tests the real store.
type restrictionStore interface {
	ApplyPostRestriction(ctx context.Context, in postgres.RestrictionCommand) (*postgres.RestrictionOutcome, error)
	ListPostRestrictions(ctx context.Context, f postgres.RestrictionListFilter) ([]postgres.PostRestriction, string, error)
	// GetModerationSubject backs the extended moderation subject read
	// (base and effective status, last base decision, active restrictions).
	GetModerationSubject(ctx context.Context, postID uuid.UUID) (*postgres.ModerationSubject, error)
}

// WithRestrictionVerifier installs the post_restriction capability verifier
// (POST_RESTRICTION_HMAC_KEY). nil: every command is refused.
func (h *Handler) WithRestrictionVerifier(v *moderationcap.RestrictionVerifier) *Handler {
	h.restrictionVerifier = v
	return h
}

// WithRestrictionStore overrides the restriction store (tests).
func (h *Handler) WithRestrictionStore(s restrictionStore) *Handler {
	h.restrictions = s
	return h
}

func (h *Handler) restrictionStore() restrictionStore {
	if h.restrictions != nil {
		return h.restrictions
	}
	if h.svc != nil {
		return h.svc
	}
	return nil
}

type restrictionCommandRequest struct {
	Claims     moderationcap.RestrictionClaims `json:"claims" binding:"required"`
	Capability string                          `json:"capability" binding:"required"`
}

// restrictionListResponse is the reconciliation page.
type restrictionListResponse struct {
	Items      []postgres.PostRestriction `json:"items"`
	NextCursor string                     `json:"next_cursor,omitempty"`
}

// ApplyPostRestriction — POST /v1/posts/internal/restrictions.
func (h *Handler) ApplyPostRestriction(c *gin.Context) {
	ctx := c.Request.Context()
	var req restrictionCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	// 1. Authority, time bounds and signature. A forgery, the wrong key,
	// the wrong purpose or an expired capability all look the same.
	if h.restrictionVerifier == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Restriction capability rejected", nil)
		return
	}
	if err := h.restrictionVerifier.Verify(req.Claims, req.Capability); err != nil {
		if errors.Is(err, moderationcap.ErrInvalidRestrictionClaims) {
			// Well signed, wrong shape: the issuer has a bug, tell it which.
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeInvalidRestrictionClaims, err.Error(), nil)
			return
		}
		slog.WarnContext(ctx, "post: restriction capability refused", "reason", err.Error())
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "INVALID_CAPABILITY", "Restriction capability rejected", nil)
		return
	}
	if req.Claims.Source == moderationcap.RestrictionSourceSafety {
		api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeSourceNotEnabled, "source=safety is reserved", nil)
		return
	}
	store := h.restrictionStore()
	if store == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Restrictions are not configured", nil)
		return
	}
	// Validate parsed every id already.
	in := postgres.RestrictionCommand{
		Action: req.Claims.Action, Source: req.Claims.Source,
		CaseID: uuid.MustParse(req.Claims.CaseID), CaseRevision: req.Claims.CaseRevision,
		PostID: uuid.MustParse(req.Claims.SubjectID), SubjectAuthorID: uuid.MustParse(req.Claims.SubjectAuthorID),
		ExpectedState: req.Claims.ExpectedState, DecisionID: uuid.MustParse(req.Claims.DecisionID),
		PolicyVersion: req.Claims.PolicyVersion, ReasonCode: req.Claims.ReasonCode,
		ActorID: uuid.MustParse(req.Claims.ActorID), ClaimsDigest: req.Claims.Digest(),
	}
	out, err := store.ApplyPostRestriction(ctx, in)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrRestrictionSourceNotEnabled):
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeSourceNotEnabled, "source is not enabled", nil)
		case errors.Is(err, postgres.ErrRestrictionInvalidInput):
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, CodeInvalidRestrictionClaims, err.Error(), nil)
		case errors.Is(err, postgres.ErrRestrictionSubjectNotFound):
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeSubjectNotFound, "Post not found", nil)
		case errors.Is(err, postgres.ErrRestrictionDecisionConflict):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "DECISION_CONFLICT", err.Error(), nil)
		case errors.Is(err, postgres.ErrRestrictionSubjectMismatch):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeSubjectMismatch, err.Error(), nil)
		case errors.Is(err, postgres.ErrRestrictionStaleRevision):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeStaleCaseRevision, err.Error(), nil)
		case errors.Is(err, postgres.ErrRestrictionStateMismatch):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeStateMismatch, err.Error(), nil)
		default:
			slog.ErrorContext(ctx, "post: restriction command failed", "error", err)
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Restriction command failed", nil)
		}
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// requireTrustSafetyToken admits ONLY a trust-safety-service token carrying
// op. No token → 401; anything else wrong → 403. Same verifier and header
// as the admin family, a different pinned issuer.
func (h *Handler) requireTrustSafetyToken(op string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeAdminTokenRequired, "a trust-safety-service token is required", nil)
			c.Abort()
			return
		}
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired, "service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		verified, err := h.verifier.Verify(rawServiceToken(c), op, "")
		if err != nil {
			code := CodeServiceTokenRejected
			if errors.Is(err, servicetoken.ErrScopeDenied) {
				code = CodeAdminPermissionScope
			}
			slog.WarnContext(ctx, "post: restrictions read token refused", "reason", err.Error())
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, code, "service token rejected", gin.H{"required": op})
			c.Abort()
			return
		}
		if verified.Issuer != IssuerTrustSafety {
			slog.WarnContext(ctx, "post: restrictions read called by another service", "issuer", verified.Issuer)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		for _, name := range identityHeaders {
			c.Request.Header.Del(name)
		}
		c.Next()
	}
}

// ListPostRestrictions — GET /v1/posts/internal/restrictions
//
//	?source=copyright            (default copyright)
//	&case_ids=<uuid>,<uuid>      (≤ 100; targeted verify)
//	&post_id=<uuid>              (one post's rows)
//	&updated_after=<RFC3339>     (incremental sweep)
//	&cursor=<next_cursor>&limit=<n ≤ 500>
func (h *Handler) ListPostRestrictions(c *gin.Context) {
	ctx := c.Request.Context()
	f := postgres.RestrictionListFilter{Source: strings.TrimSpace(c.DefaultQuery("source", postgres.RestrictionSourceCopyright)), Cursor: c.Query("cursor")}
	if raw := strings.TrimSpace(c.Query("case_ids")); raw != "" {
		for _, part := range strings.Split(raw, ",") {
			id, err := uuid.Parse(strings.TrimSpace(part))
			if err != nil {
				api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "case_ids must be UUIDs", nil)
				return
			}
			f.CaseIDs = append(f.CaseIDs, id)
		}
		if len(f.CaseIDs) > 100 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "at most 100 case_ids", nil)
			return
		}
	}
	if raw := strings.TrimSpace(c.Query("post_id")); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "post_id must be a UUID", nil)
			return
		}
		f.PostID = &id
	}
	if raw := strings.TrimSpace(c.Query("updated_after")); raw != "" {
		at, err := time.Parse(time.RFC3339Nano, raw)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "updated_after must be RFC3339", nil)
			return
		}
		f.UpdatedAfter = &at
	}
	if raw := strings.TrimSpace(c.Query("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 || n > postgres.MaxRestrictionListLimit {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "limit must be 1..500", nil)
			return
		}
		f.Limit = n
	}
	store := h.restrictionStore()
	if store == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Restrictions are not configured", nil)
		return
	}
	items, next, err := store.ListPostRestrictions(ctx, f)
	if err != nil {
		if errors.Is(err, postgres.ErrRestrictionInvalidInput) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
			return
		}
		slog.ErrorContext(ctx, "post: restrictions read failed", "error", err)
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Restrictions read failed", nil)
		return
	}
	if items == nil {
		items = []postgres.PostRestriction{}
	}
	api.JSON(c.Writer, http.StatusOK, restrictionListResponse{Items: items, NextCursor: next}, nil)
}
