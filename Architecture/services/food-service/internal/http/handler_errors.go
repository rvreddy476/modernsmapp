package http

import (
	"errors"
	"net/http"

	"github.com/atpost/food-service/internal/payments"
	"github.com/atpost/food-service/internal/pricing"
	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

type errorMapping struct {
	target error
	status int
	code   string
}

// lifecycleErrors maps the order-lifecycle, dispatch and ordering sentinels to
// stable HTTP answers. Order matters only where one error could wrap another.
var lifecycleErrors = []errorMapping{
	{postgres.ErrOrderStatusConflict, http.StatusConflict, "FOOD_ORDER_STATUS_CONFLICT"},
	{postgres.ErrOrderTransitionNotAllowed, http.StatusConflict, "FOOD_ORDER_TRANSITION_NOT_ALLOWED"},
	{postgres.ErrDeliveryOTPRequired, http.StatusConflict, "FOOD_DELIVERY_OTP_REQUIRED"},
	{postgres.ErrAssignmentNotReady, http.StatusConflict, "FOOD_DELIVERY_ASSIGNMENT_NOT_READY"},
	{postgres.ErrDeliveryCodeLocked, http.StatusTooManyRequests, "FOOD_DELIVERY_CODE_ATTEMPTS_EXCEEDED"},
	{postgres.ErrPickupCodeLocked, http.StatusTooManyRequests, "FOOD_PICKUP_CODE_ATTEMPTS_EXCEEDED"},
	{postgres.ErrDeliveryPartnerNotActive, http.StatusForbidden, "FOOD_DELIVERY_PARTNER_NOT_ACTIVE"},
	{postgres.ErrRestaurantNotAccepting, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_NOT_ACCEPTING"},
	{postgres.ErrRestaurantOutsideHours, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_OUTSIDE_HOURS"},
	{postgres.ErrAddressOutOfRange, http.StatusUnprocessableEntity, "FOOD_ADDRESS_OUT_OF_RANGE"},
	{postgres.ErrAddressLocationRequired, http.StatusUnprocessableEntity, "FOOD_ADDRESS_LOCATION_REQUIRED"},
	{postgres.ErrRestaurantLocationMissing, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_LOCATION_MISSING"},
	{postgres.ErrAddonInvalid, http.StatusUnprocessableEntity, "FOOD_CART_ADDON_INVALID"},

	// Wave 1 B3 pricing through shared/gst.
	{pricing.ErrCouponsDisabled, http.StatusUnprocessableEntity, "FOOD_COUPONS_DISABLED"},
	{pricing.ErrRestaurantTaxCategoryMissing, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_TAX_CATEGORY_MISSING"},
	{pricing.ErrRestaurantStateUnknown, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_STATE_UNKNOWN"},
	{pricing.ErrRestaurantGSTINMissing, http.StatusUnprocessableEntity, "FOOD_RESTAURANT_GSTIN_MISSING"},
	{pricing.ErrPlatformGSTINNotConfigured, http.StatusServiceUnavailable, "FOOD_PLATFORM_GSTIN_NOT_CONFIGURED"},
	{pricing.ErrPricingFailed, http.StatusUnprocessableEntity, "FOOD_PRICING_FAILED"},

	// Payments.
	{payments.ErrPaymentMethodUnavailable, http.StatusUnprocessableEntity, "PAYMENT_METHOD_UNAVAILABLE"},
	{payments.ErrPaymentMethodInvalid, http.StatusUnprocessableEntity, "PAYMENT_METHOD_INVALID"},
	{payments.ErrPaymentNotOnline, http.StatusConflict, "FOOD_PAYMENT_NOT_ONLINE"},
	{postgres.ErrCODNotAllowed, http.StatusConflict, "FOOD_COD_NOT_ALLOWED_FROM_STATE"},
	{postgres.ErrPaymentNotAllowedFromState, http.StatusConflict, "FOOD_PAYMENT_NOT_ALLOWED_FROM_STATE"},
	{postgres.ErrRefundNotEligible, http.StatusConflict, "FOOD_REFUND_NOT_ELIGIBLE"},
	{service.ErrPaymentCallbackMismatch, http.StatusConflict, "FOOD_PAYMENT_CALLBACK_MISMATCH"},
	{service.ErrPaymentCallbackIncomplete, http.StatusBadRequest, "FOOD_PAYMENT_CALLBACK_INCOMPLETE"},
	{service.ErrPaymentNotVerified, http.StatusBadRequest, "FOOD_PAYMENT_NOT_VERIFIED"},
	{service.ErrPaymentIntentMissing, http.StatusConflict, "FOOD_PAYMENT_INTENT_MISSING"},
	{service.ErrPaymentsNotConfigured, http.StatusServiceUnavailable, "FOOD_PAYMENTS_UNAVAILABLE"},
	{payments.ErrPaymentsUnavailable, http.StatusServiceUnavailable, "FOOD_PAYMENTS_UNAVAILABLE"},
	{payments.ErrRefused, http.StatusBadGateway, "FOOD_PAYMENTS_REFUSED"},
}

// writeKnownError writes the mapped response and returns true when err is one
// of the lifecycle sentinels, or pgx.ErrNoRows (404, without echoing driver
// text). Otherwise it writes nothing and returns false so the caller keeps its
// existing fallback.
func writeKnownError(c *gin.Context, err error) bool {
	for _, m := range lifecycleErrors {
		if errors.Is(err, m.target) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, m.status, m.code, err.Error(), nil)
			return true
		}
	}
	if errors.Is(err, pgx.ErrNoRows) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound, "FOOD_NOT_FOUND", "not found", nil)
		return true
	}
	return false
}
