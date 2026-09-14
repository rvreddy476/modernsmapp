package http

import (
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/food-service/internal/onboarding"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Feast customer gaps: the optional delivery point on the restaurant list,
// the restaurant detail and add-to-cart. Every parameter is additive; a
// request without one behaves as before.

// validateNear checks an optional lat/lng pair: both or neither, each a
// finite number in range. A refusal is a 422 field error.
func validateNear(lat, lng *float64) (*postgres.GeoPoint, error) {
	switch {
	case lat == nil && lng == nil:
		return nil, nil
	case lat == nil:
		return nil, &onboarding.FieldError{Code: onboarding.CodeLocationRequired, Field: "lat", Message: "lat and lng must be given together"}
	case lng == nil:
		return nil, &onboarding.FieldError{Code: onboarding.CodeLocationRequired, Field: "lng", Message: "lat and lng must be given together"}
	}
	if math.IsNaN(*lat) || math.IsInf(*lat, 0) || *lat < -90 || *lat > 90 {
		return nil, &onboarding.FieldError{Code: onboarding.CodeCoordinatesOutOfRange, Field: "lat", Message: "lat must be a number from -90 to 90"}
	}
	if math.IsNaN(*lng) || math.IsInf(*lng, 0) || *lng < -180 || *lng > 180 {
		return nil, &onboarding.FieldError{Code: onboarding.CodeCoordinatesOutOfRange, Field: "lng", Message: "lng must be a number from -180 to 180"}
	}
	return &postgres.GeoPoint{Lat: *lat, Lng: *lng}, nil
}

// nearFromQuery reads ?lat=&lng=. It answers the 422 itself and returns false
// when the pair is invalid. A present but empty value is invalid.
func nearFromQuery(c *gin.Context) (*postgres.GeoPoint, bool) {
	var values [2]*float64
	for i, name := range []string{"lat", "lng"} {
		raw, present := c.GetQuery(name)
		if !present {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
		if err != nil {
			writeOnboardingError(c, &onboarding.FieldError{Code: onboarding.CodeCoordinatesOutOfRange, Field: name, Message: name + " must be a number"})
			return nil, false
		}
		values[i] = &v
	}
	near, err := validateNear(values[0], values[1])
	if err != nil {
		writeOnboardingError(c, err)
		return nil, false
	}
	return near, true
}

// cartDeliveryPoint reads add-to-cart's optional address_id or lat/lng. It
// answers the refusal itself and returns false when the body is invalid.
func cartDeliveryPoint(c *gin.Context, rawAddressID string, lat, lng *float64) (*uuid.UUID, *postgres.GeoPoint, bool) {
	var addressID *uuid.UUID
	if rawAddressID != "" {
		parsed, err := uuid.Parse(rawAddressID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ADDRESS", "invalid address id", nil)
			return nil, nil, false
		}
		addressID = &parsed
	}
	near, err := validateNear(lat, lng)
	if err == nil && near != nil && addressID != nil {
		err = &onboarding.FieldError{Code: onboarding.CodeAddressInvalid, Field: "address_id", Message: "send address_id or lat and lng, not both"}
	}
	if err != nil {
		writeOnboardingError(c, err)
		return nil, nil, false
	}
	return addressID, near, true
}

// describeUnserviceable names a restaurant's refusal with the code and message
// POST /orders answers for the same error (knownError).
func describeUnserviceable(r *postgres.RestaurantSummary) {
	if r.Unserviceable == nil {
		return
	}
	if m, ok := knownError(r.Unserviceable); ok {
		r.UnserviceableReasonCode = m.code
	}
	r.UnserviceableMessage = r.Unserviceable.Error()
}
