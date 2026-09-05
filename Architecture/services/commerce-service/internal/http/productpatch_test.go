package http

// The PATCH allowlist, at the edge, with no database.
//
// The refusal happens before the handler reaches the service, which is what
// makes this testable without one — and is also the point: a body naming a
// column a seller may not write never gets as far as a transaction.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// patchEdge builds an engine whose PATCH route is registered but whose
// service is nil. Any test here that reached the service would panic, which
// is a louder and more useful failure than a mocked 200.
func patchEdge(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil)
	r.PATCH("/v1/commerce/products/:productId", h.UpdateProduct)
	return r
}

func patch(t *testing.T, r *gin.Engine, body any) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPatch,
		"/v1/commerce/products/"+uuid.NewString(), bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func errorEnvelope(t *testing.T, body []byte) (code, message string, details map[string]any) {
	t.Helper()
	var env struct {
		Error struct {
			Code    string         `json:"code"`
			Message string         `json:"message"`
			Details map[string]any `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return env.Error.Code, env.Error.Message, env.Error.Details
}

// A field that is not on the allowlist is REFUSED, by name — not silently
// dropped.
//
// Silently dropping is the tempting choice. A client that sends
// `{"approval_status":"approved"}` and gets a 200 has been told its request
// was honoured; the seller's screen then shows an approved listing the
// database calls a draft, and the disagreement surfaces days later as "my
// product vanished".
func TestThePatchAllowlistRefusesEveryFieldThatIsNotOnIt(t *testing.T) {
	r := patchEdge(t)

	for _, field := range []struct {
		key string
		val any
	}{
		{"approval_status", "approved"}, // the review outcome
		{"seller_id", uuid.NewString()}, // ownership: a catalogue takeover
		{"status", "active"},            // the publish switch
		{"visibility", "public"},        // ditto
		{"slug", "cheaper-than-yours"},  // the URL live links key on
		{"published_at", "2026-01-01T00:00:00Z"},
		{"avg_rating", 5}, // a five-star shop by lunchtime
		{"review_count", 9999},
		{"order_count", 9999},
		{"view_count", 9999},
		{"is_featured", true},                // merchandising, an operator's call
		{"rejection_reason", ""},             // the reviewer's own words
		{"schema_version", 99},               // "these values were checked" — they were not
		{"attributes_doc", map[string]any{}}, // a projection, rebuilt not written
		{"gtin", "9780000000000"},
		{"source_image_url", "https://example.test/x.jpg"},
		{"created_at", "2020-01-01T00:00:00Z"},
		{"id", uuid.NewString()},
		{"not_a_column_at_all", 1},
	} {
		t.Run(field.key, func(t *testing.T) {
			w := patch(t, r, map[string]any{field.key: field.val})
			if w.Code != http.StatusBadRequest {
				t.Fatalf("PATCH with %q returned %d; want 400 — it must be refused before it "+
					"reaches a transaction\n%s", field.key, w.Code, w.Body.String())
			}
			code, msg, details := errorEnvelope(t, w.Body.Bytes())
			if code != "FIELD_NOT_PATCHABLE" {
				t.Fatalf("code %q, want FIELD_NOT_PATCHABLE\n%s", code, w.Body.String())
			}
			// The refusal NAMES the field. "invalid body" would make the
			// client bisect its own request.
			if !bytes.Contains([]byte(msg), []byte(field.key)) {
				t.Fatalf("the refusal does not name %q: %s", field.key, msg)
			}
			if details == nil || details["fields"] == nil {
				t.Fatalf("the refusal carries no machine-readable field list: %s", w.Body.String())
			}
			// And it serves the whole allowlist, so a client that guessed
			// wrong can see what it may send without reading the source.
			if details["patchable"] == nil {
				t.Fatalf("the refusal does not serve the allowlist: %s", w.Body.String())
			}
		})
	}
}

// Every refused key comes back at once. One at a time makes a client fix them
// one round trip each — the same argument the per-field attribute errors make.
func TestThePatchNamesEveryRefusedFieldInOneResponse(t *testing.T) {
	w := patch(t, patchEdge(t), map[string]any{
		"approval_status": "approved",
		"seller_id":       uuid.NewString(),
		"title":           "a legitimate edit",
	})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	_, _, details := errorEnvelope(t, w.Body.Bytes())
	fields, _ := details["fields"].([]any)
	if len(fields) != 2 {
		t.Fatalf("want both refused fields listed, got %v\n%s", fields, w.Body.String())
	}
}

// A NOT NULL column cannot be cleared, and the refusal says which one.
//
// Letting `{"title": null}` through reaches the database as a NOT NULL
// violation, which the edge reports as "one of the supplied values is not
// permitted" — true, and useless.
func TestClearingARequiredFieldIsRefusedByName(t *testing.T) {
	w := patch(t, patchEdge(t), map[string]json.RawMessage{"title": json.RawMessage("null")})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status %d\n%s", w.Code, w.Body.String())
	}
	code, msg, _ := errorEnvelope(t, w.Body.Bytes())
	if code != "INVALID_BODY" || !bytes.Contains([]byte(msg), []byte("title")) {
		t.Fatalf("code=%q msg=%q; want INVALID_BODY naming title", code, msg)
	}
}

// The allowlist table itself, asserted as a set. A field added to it is a
// deliberate decision and should have to be made here too.
func TestThePatchableFieldSetIsExactlyWhatWasDecided(t *testing.T) {
	want := map[string]bool{
		"category_id": true, "brand_id": true, "tax_class_id": true,
		"title": true, "short_title": true,
		"description": true, "short_description": true,
		"brand_name": true, "manufacturer_name": true,
		"product_type": true, "condition": true,
		"primary_image_media_id": true, "video_media_id": true,
		"weight_grams": true, "length_cm": true, "width_cm": true, "height_cm": true,
		"country_of_origin": true, "warranty_info": true,
		"return_policy_type": true, "return_policy_days": true, "hsn_code": true,
		"search_keywords": true, "meta_title": true, "meta_description": true,
	}
	for k := range patchableProductFields {
		if !want[k] {
			t.Errorf("%q is patchable and was not in the agreed set", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("%q was agreed to be patchable and is not", k)
	}
}
