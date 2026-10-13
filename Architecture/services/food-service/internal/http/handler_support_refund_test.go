package http

import (
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"testing"
)

func TestSupportRefundErrorsAreActionableAndNotServerErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{
		{postgres.ErrSupportOrderForbidden, 403}, {postgres.ErrInvalidRefundAmount, 422}, {postgres.ErrRefundDecisionConflict, 409}, {postgres.ErrRefundNotEligible, 409},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("POST", "/v1/food/refunds", nil)
		if !writeSupportError(c, tc.err) || w.Code != tc.status {
			t.Fatalf("%v: %d %s", tc.err, w.Code, w.Body.String())
		}
	}
}
