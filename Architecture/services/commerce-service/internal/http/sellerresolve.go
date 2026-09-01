package http

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// sellerForCaller resolves the caller's own seller profile, or writes the
// correct refusal and returns false.
//
// Eighteen handlers held this block:
//
//	seller, err := h.svc.GetSellerProfile(ctx, userID)
//	if err != nil {
//	    ... 403 NO_SELLER ...
//	}
//
// Any error meant "you are not a seller". A dropped connection, a statement
// timeout, a failed-over primary — every one of them was reported to the
// seller as an authorisation failure, and logged as nothing at all. A seller
// whose dashboard says "seller account not found" during a database incident
// does not retry; they open a support ticket saying their account is gone.
//
// It could not have been written any other way until this pass, because
// `GetSellerByUserID` returned pgx.ErrNoRows for "no such seller" with no way
// to tell it from a transport failure. `ErrNoSellerRow` is what makes the two
// separable, so this is the second half of that fix rather than a new idea.
func (h *Handler) sellerForCaller(c *gin.Context, userID uuid.UUID) (*postgres.Seller, bool) {
	seller, err := h.svc.GetSellerProfile(c.Request.Context(), userID)
	switch {
	case errors.Is(err, postgres.ErrNoSellerRow):
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden,
			"NO_SELLER", "seller account not found", nil)
		return nil, false
	case err != nil:
		// Logged at error, because this one is an incident and the previous
		// version produced no signal whatsoever.
		slog.Error("commerce: could not read the caller's seller profile",
			"user_id", userID, "error", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "could not read your seller account", nil)
		return nil, false
	}
	return seller, true
}
