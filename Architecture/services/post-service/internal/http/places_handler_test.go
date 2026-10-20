package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/atpost/post-service/internal/places"
)

type fakePlaces struct {
	out []places.Place
	err error
	q   string
}

func (f *fakePlaces) Autocomplete(_ context.Context, q, _, _ string) ([]places.Place, error) {
	f.q = q
	return f.out, f.err
}

func placesRequest(h *Handler, user string) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/v1/posts/places/autocomplete", h.AutocompletePlaces)
	req := httptest.NewRequest(http.MethodGet, "/v1/posts/places/autocomplete?q=Charminar", nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestAutocompletePlacesRoute(t *testing.T) {
	user := uuid.NewString()
	ok := &fakePlaces{out: []places.Place{{PlaceID: "p1", Name: "Charminar", Secondary: "Hyderabad"}}}

	if rec := placesRequest((&Handler{}).WithPlaces(ok), ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("signed out: %d", rec.Code)
	}
	if rec := placesRequest(&Handler{}, user); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "PLACES_DISABLED" {
		t.Fatalf("not wired: %d", rec.Code)
	}
	rec := placesRequest((&Handler{}).WithPlaces(ok), user)
	if rec.Code != http.StatusOK || ok.q != "Charminar" {
		t.Fatalf("ok: %d q=%q", rec.Code, ok.q)
	}
	for err, want := range map[error]string{
		places.ErrBadQuery: "INVALID_QUERY",
		places.ErrDisabled: "PLACES_DISABLED",
		errors.New("google 500"): "PLACES_UNAVAILABLE",
	} {
		rec := placesRequest((&Handler{}).WithPlaces(&fakePlaces{err: err}), user)
		if errorCode(t, rec) != want {
			t.Fatalf("%v: got %s want %s", err, errorCode(t, rec), want)
		}
	}
}
