package http

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/food-service/internal/service"
	"github.com/atpost/food-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// HTTP halves of the access-and-data lane (audit 2026-10-30): H1 and M6
// answer with stable codes; L5's fingerprint reaches the store and leaves
// the body intact for the handler.

type laneFakeStore struct {
	service.Store
	createErr error
	upsertErr error
}

func (f *laneFakeStore) CreateMenuItem(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, postgres.MenuItemInput) (*postgres.MenuItem, error) {
	if f.createErr != nil {
		return nil, f.createErr
	}
	return &postgres.MenuItem{}, nil
}

func (f *laneFakeStore) UpsertDeliveryPartner(context.Context, uuid.UUID, postgres.DeliveryPartnerInput) (*postgres.DeliveryPartner, error) {
	if f.upsertErr != nil {
		return nil, f.upsertErr
	}
	return &postgres.DeliveryPartner{}, nil
}

func laneRouter(st service.Store) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(st)).RegisterRoutes(r)
	return r
}

func laneServe(r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestCreateMenuItem_ForeignCategoryIs404(t *testing.T) {
	r := laneRouter(&laneFakeStore{createErr: postgres.ErrMenuCategoryNotFound})
	w := laneServe(r, http.MethodPost, "/v1/food/partner/restaurants/"+uuid.NewString()+"/menu/items",
		`{"category_id":"`+uuid.NewString()+`","name":"x","base_price":1}`)
	if w.Code != http.StatusNotFound || errorCode(t, w) != "FOOD_MENU_CATEGORY_NOT_FOUND" {
		t.Fatalf("status=%d code=%q body=%s", w.Code, errorCode(t, w), w.Body.String())
	}
}

func TestCreateMenuItem_ForeignImageURLIs422(t *testing.T) {
	r := laneRouter(&laneFakeStore{})
	w := laneServe(r, http.MethodPost, "/v1/food/partner/restaurants/"+uuid.NewString()+"/menu/items",
		`{"category_id":"`+uuid.NewString()+`","name":"x","base_price":1,"image_url":"https://tracker.example.test/p.gif"}`)
	if w.Code != http.StatusUnprocessableEntity || errorCode(t, w) != "FOOD_IMAGE_URL_INVALID" {
		t.Fatalf("status=%d code=%q body=%s", w.Code, errorCode(t, w), w.Body.String())
	}
}

func TestUpsertDeliveryPartner_LockedIdentityIs409(t *testing.T) {
	r := laneRouter(&laneFakeStore{upsertErr: &postgres.RiderIdentityLockedError{Status: "ACTIVE", Fields: []string{"phone"}}})
	w := laneServe(r, http.MethodPatch, "/v1/food/delivery/profile", `{"full_name":"R","phone":"+919"}`)
	if w.Code != http.StatusConflict || errorCode(t, w) != postgres.CodeDeliveryProfileLocked {
		t.Fatalf("status=%d code=%q body=%s", w.Code, errorCode(t, w), w.Body.String())
	}
	if f, _ := decodeEnvelope(t, w).Error.Details["fields"].([]any); len(f) != 1 || f[0] != "phone" {
		t.Fatalf("details.fields = %v", decodeEnvelope(t, w).Error.Details["fields"])
	}
}

func TestRequestFingerprint_DistinguishesRequests(t *testing.T) {
	base := requestFingerprint("POST", "/v1/food/delivery/assignments/A/picked-up", "", []byte(`{}`))
	for name, other := range map[string]string{
		"method": requestFingerprint("PATCH", "/v1/food/delivery/assignments/A/picked-up", "", []byte(`{}`)),
		"path":   requestFingerprint("POST", "/v1/food/delivery/assignments/B/picked-up", "", []byte(`{}`)),
		"step":   requestFingerprint("POST", "/v1/food/delivery/assignments/A/delivered", "", []byte(`{}`)),
		"query":  requestFingerprint("POST", "/v1/food/delivery/assignments/A/picked-up", "x=1", []byte(`{}`)),
		"body":   requestFingerprint("POST", "/v1/food/delivery/assignments/A/picked-up", "", []byte(`{"a":1}`)),
	} {
		if other == base {
			t.Errorf("%s change gave the same fingerprint", name)
		}
	}
	if again := requestFingerprint("POST", "/v1/food/delivery/assignments/A/picked-up", "", []byte(`{}`)); again != base {
		t.Error("same request gave a different fingerprint")
	}
}

// The middleware sets the fingerprint only when a key is sent, and the
// handler still reads the whole body.
func TestIdempotencyFingerprintMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var seenBody, seenFP string
	r := gin.New()
	r.POST("/x/:id", idempotencyFingerprint(), func(c *gin.Context) {
		b, _ := io.ReadAll(c.Request.Body)
		seenBody = string(b)
		seenFP = postgres.RequestFingerprintOf(c.Request.Context())
		c.Status(http.StatusNoContent)
	})
	body := `{"status":"PREPARING"}`
	send := func(key, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/x/1", strings.NewReader(body))
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w
	}
	w := send("k1", body)
	if w.Code != http.StatusNoContent || seenBody != body {
		t.Fatalf("code=%d body=%q", w.Code, seenBody)
	}
	if want := requestFingerprint(http.MethodPost, "/x/1", "", []byte(body)); seenFP != want {
		t.Fatalf("fingerprint = %q, want %q", seenFP, want)
	}
	if w := send("", body); w.Code != http.StatusNoContent || seenFP != "" || seenBody != body {
		t.Fatalf("no key: code=%d fp=%q body=%q", w.Code, seenFP, seenBody)
	}
	if w := send("k2", strings.Repeat("a", maxFingerprintBody+1)); w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized keyed body: %d", w.Code)
	}
}
