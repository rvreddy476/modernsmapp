package http

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/atpost/post-service/internal/places"
	"github.com/atpost/shared/api"
)

// Location search for the upload studio and the post composer
// (places/places.go). Signed in only: every call is a billed Google request.
//
//	GET /v1/posts/places/autocomplete?q=&session=&lang=
//	    -> {"data":[{place_id, name, secondary}]}
//	    503 PLACES_DISABLED / PLACES_UNAVAILABLE: the client keeps free text.

// placeSearcher is the one call the route needs, an interface for tests.
type placeSearcher interface {
	Autocomplete(ctx context.Context, q, session, language string) ([]places.Place, error)
}

// WithPlaces wires the location search; without it the route answers 503.
func (h *Handler) WithPlaces(p placeSearcher) *Handler {
	h.places = p
	return h
}

// AutocompletePlaces handles GET /v1/posts/places/autocomplete.
func (h *Handler) AutocompletePlaces(c *gin.Context) {
	ctx := c.Request.Context()
	if _, ok := channelCaller(c); !ok {
		return
	}
	if h.places == nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "PLACES_DISABLED", places.ErrDisabled.Error(), nil)
		return
	}
	found, err := h.places.Autocomplete(ctx, c.Query("q"), c.Query("session"), c.Query("lang"))
	switch {
	case err == nil:
		api.JSON(c.Writer, http.StatusOK, found, nil)
	case errors.Is(err, places.ErrBadQuery):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_QUERY", err.Error(), nil)
	case errors.Is(err, places.ErrDisabled):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "PLACES_DISABLED", err.Error(), nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "PLACES_UNAVAILABLE", places.ErrUpstream.Error(), nil)
	}
}
