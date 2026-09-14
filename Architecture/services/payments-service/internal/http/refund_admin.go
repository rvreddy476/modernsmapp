package http

// Operator routes for parked refunds, on the /v1/payments/internal family.
//
//	GET  /v1/payments/internal/refunds/needs-attention?ref_type=&limit=&cursor=
//	POST /v1/payments/internal/refunds/:commandId/resolve
//	     {"resolution":"refunded_manually"|"written_off"|"test_data","note":"…"}
//
// Credential: the family's own gate (requireServiceCredential). A service
// token must carry OpRefundAdmin and is scoped to intents its domain owns
// (owner_domain = the token's issuer). The legacy internal key sees every
// domain, so it is admitted on these two routes only outside production
// (ENV=prod refuses it with 403 SERVICE_TOKEN_REQUIRED before any store
// access); in local/dev it still works, which the Feast dev seeder relies on.
// The resolving operator is named in X-User-Id, which is recorded on the
// command and the audit row, never authorised against.

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/payments-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// OpRefundAdmin is the service-token operation for listing and resolving
// parked refunds. A caller holds it only when SERVICE_CALLER_<NAME>_OPS says so.
const OpRefundAdmin = "payments:refund.admin"

// CodeServiceTokenRequired is the stable error code a production refund
// operator route answers when the caller presented the legacy internal key.
const CodeServiceTokenRequired = "SERVICE_TOKEN_REQUIRED"

const (
	maxResolutionNoteRunes = 1000
	maxOperatorIDLen       = 200
)

// refuseLegacyKeyInProduction runs after requireServiceCredential and before
// requireOp on the two operator routes. In production a legacy internal-key
// caller is refused: the key is shared by every sibling service and sees every
// domain, so it must not be able to write off or manually settle another
// domain's refund. Nothing is read or written before the refusal. The WARN
// names the route, never the key.
func (h *Handler) refuseLegacyKeyInProduction() gin.HandlerFunc {
	return func(c *gin.Context) {
		if h.production && isLegacyCaller(c) {
			slog.Warn("payments: refund operator route refused the internal service key in production; "+
				"a service token carrying "+OpRefundAdmin+" is required",
				"method", c.Request.Method, "route", c.FullPath())
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeServiceTokenRequired,
				"this route requires a service token carrying "+OpRefundAdmin+
					"; the internal service key is not accepted in production", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

// ListRefundsNeedingAttention GET /v1/payments/internal/refunds/needs-attention
func (h *Handler) ListRefundsNeedingAttention(c *gin.Context) {
	ctx := c.Request.Context()
	limit := 50
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_LIMIT", "limit must be 1..200", nil)
			return
		}
		limit = n
	}
	f := postgres.NeedsAttentionFilter{Limit: limit, ReferenceType: c.Query("ref_type")}
	if v := c.Query("cursor"); v != "" {
		cur, err := decodeRefundCursor(v)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "cursor is not valid", nil)
			return
		}
		f.After = cur
	}
	if !isLegacyCaller(c) {
		f.OwnerDomain = callerDomain(c)
		if f.OwnerDomain == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "caller identity is required", nil)
			return
		}
	}
	items, next, err := h.svc.ListRefundsNeedingAttention(ctx, f)
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "FETCH_FAILED", "could not list parked refunds", nil)
		return
	}
	if items == nil {
		items = []postgres.NeedsAttentionRefund{}
	}
	var nextCursor any
	if next != nil {
		nextCursor = encodeRefundCursor(*next)
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"items": items, "next_cursor": nextCursor}, nil)
}

// ResolveRefundCommand POST /v1/payments/internal/refunds/:commandId/resolve
func (h *Handler) ResolveRefundCommand(c *gin.Context) {
	ctx := c.Request.Context()
	id, err := uuid.Parse(c.Param("commandId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "invalid refund command id", nil)
		return
	}
	operator := strings.TrimSpace(c.GetHeader("X-User-Id"))
	if operator == "" || len(operator) > maxOperatorIDLen || !utf8.ValidString(operator) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "OPERATOR_REQUIRED",
			"X-User-Id must name the operator resolving the refund", nil)
		return
	}
	var body struct {
		Resolution string `json:"resolution"`
		Note       string `json:"note"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_BODY", "body must be JSON with resolution and note", nil)
		return
	}
	if !postgres.IsValidResolution(body.Resolution) {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_RESOLUTION",
			"resolution must be refunded_manually, written_off or test_data", nil)
		return
	}
	note := strings.TrimSpace(body.Note)
	if note == "" || utf8.RuneCountInString(note) > maxResolutionNoteRunes {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_NOTE",
			"note is required and at most 1000 characters", nil)
		return
	}

	in := postgres.ResolveRefundInput{
		CommandID: id, Resolution: body.Resolution, Note: note, OperatorID: operator,
		Credential: "internal_key",
	}
	if !isLegacyCaller(c) {
		in.OwnerDomain = callerDomain(c)
		if in.OwnerDomain == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "FORBIDDEN", "caller identity is required", nil)
			return
		}
		in.Credential = "service_token:" + in.OwnerDomain
	}

	res, err := h.svc.ResolveRefundCommand(ctx, in)
	switch {
	case errors.Is(err, postgres.ErrRefundCommandNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "refund command not found", nil)
		return
	case errors.Is(err, postgres.ErrRefundCommandNotParked):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "REFUND_NOT_PARKED",
			"only a refund in needs_attention can be resolved", nil)
		return
	case errors.Is(err, postgres.ErrManualRefundRefused):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "MANUAL_REFUND_REFUSED", err.Error(), nil)
		return
	case errors.Is(err, postgres.ErrInvalidResolution):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_RESOLUTION", err.Error(), nil)
		return
	case err != nil:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "RESOLVE_FAILED", "could not resolve the refund", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

func encodeRefundCursor(cur postgres.RefundCursor) string {
	return base64.RawURLEncoding.EncodeToString(
		[]byte(cur.CreatedAt.UTC().Format(time.RFC3339Nano) + "|" + cur.ID.String()))
}

func decodeRefundCursor(s string) (*postgres.RefundCursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return nil, err
	}
	at, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return nil, errors.New("cursor has no separator")
	}
	t, err := time.Parse(time.RFC3339Nano, at)
	if err != nil {
		return nil, err
	}
	u, err := uuid.Parse(id)
	if err != nil {
		return nil, err
	}
	return &postgres.RefundCursor{CreatedAt: t, ID: u}, nil
}
