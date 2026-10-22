package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
)

// Service-to-service money routes outside the admin family (security sweep
// #17, 21 Oct 2026).
//
// POST /v1/monetization/internal/charge-and-credit debits one wallet and
// credits another, between ARBITRARY from_user_id / to_user_id. It sat in
// the internal-key group with nothing else: the gateway stamps that key on
// every request it proxies and every backend pod holds it, so the key says
// "a trusted process", never which one, and any compromised sibling could
// drain any wallet once writes open. It now also needs a signed service
// token (same verifier and header as the admin family, SERVICE_CALLERS),
// from an issuer this route names, carrying the route's operation.
//
// The internal key, the launch boundary and maintenance mode still apply in
// front of the token, exactly as before.

const (
	// OpChargeAndCredit is the operation a charge-and-credit token carries.
	OpChargeAndCredit = "monetization:charge-and-credit"
	// IssuerFoodService is today's only caller of charge-and-credit (Feast
	// wallet payments and their refunds, food-service service.go).
	IssuerFoodService = "food-service"
)

// chargeAndCreditCallers are the issuers whose tokens reach the route.
var chargeAndCreditCallers = []string{IssuerFoodService}

// requireServiceCaller admits ONLY a service token from one of issuers that
// carries op. No token → 401; no verifier configured → 401; a bad, expired,
// foreign-audience or unknown-caller token, a scope without op, or another
// issuer → 403. The issuer is recorded for the handler.
func (h *Handler) requireServiceCaller(op string, issuers ...string) gin.HandlerFunc {
	if op == "" || len(issuers) == 0 {
		panic("monetization: requireServiceCaller needs the route's operation and callers")
	}
	allowed := make(map[string]bool, len(issuers))
	for _, iss := range issuers {
		allowed[iss] = true
	}
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		if rawServiceToken(c) == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"a service token is required", nil)
			c.Abort()
			return
		}
		if h.verifier == nil || h.verifier.Callers() == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceCredentialRequired,
				"service tokens are not accepted by this deployment", nil)
			c.Abort()
			return
		}
		verified, err := h.verifier.Verify(rawServiceToken(c), op, "")
		if err != nil {
			code := CodeServiceTokenRejected
			if errors.Is(err, servicetoken.ErrScopeDenied) {
				code = CodeAdminPermissionScope
			}
			slog.WarnContext(ctx, "monetization: service token refused", "reason", err.Error(), "required", op, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, code, "service token rejected", gin.H{"required": op})
			c.Abort()
			return
		}
		if !allowed[verified.Issuer] {
			slog.WarnContext(ctx, "monetization: internal route called by another service", "issuer", verified.Issuer, "path", c.Request.URL.Path)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		c.Set(ctxServiceCaller, verified.Issuer)
		c.Next()
	}
}
