package http

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
)

// Audit 2026-10-30 M2: substitution refusals carry stable codes.
func TestSubstitutionErrorCodes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{postgres.ErrSubstitutionPriceIncrease, http.StatusUnprocessableEntity, "FOOD_SUBSTITUTION_PRICE_INCREASE"},
		{postgres.ErrSubstitutionPricePaiseRequired, http.StatusUnprocessableEntity, "FOOD_SUBSTITUTION_PRICE_PAISE_REQUIRED"},
		{postgres.ErrSubstitutionItemInvalid, http.StatusUnprocessableEntity, "FOOD_SUBSTITUTION_ITEM_INVALID"},
		{fmt.Errorf("%w: order is DELIVERED", postgres.ErrSubstitutionNotAllowed), http.StatusConflict, "FOOD_SUBSTITUTION_NOT_ALLOWED"},
		{postgres.ErrSubstitutionRefundUnavailable, http.StatusConflict, "FOOD_SUBSTITUTION_REFUND_UNAVAILABLE"},
		{fmt.Errorf("something else"), http.StatusBadRequest, "SUBSTITUTION_PROPOSE_FAILED"},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
		substitutionError(c, "SUBSTITUTION_PROPOSE_FAILED", tc.err)
		if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.code) {
			t.Errorf("%v: got %d %s, want %d %s", tc.err, w.Code, w.Body.String(), tc.status, tc.code)
		}
	}
}

// Audit L3: an impossible rider jump has its own 422 code.
func TestLocationImplausibleIsKnown(t *testing.T) {
	m, ok := knownError(fmt.Errorf("%w: 22 km in 30 s", postgres.ErrLocationImplausible))
	if !ok || m.status != http.StatusUnprocessableEntity || m.code != "FOOD_DELIVERY_LOCATION_IMPLAUSIBLE" {
		t.Fatalf("mapping = %+v %v", m, ok)
	}
}
