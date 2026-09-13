package http

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
)

// Lane B8 full-stack tests: router -> service -> real store on TEST_PG_DSN.

// Every onboarding read-back answers the owner with masked identifiers only,
// says which step is not saved yet, and is 404 FOOD_NOT_FOUND for a stranger.
func TestB8OnboardingReadBacksAreMaskedAndOwnerOnly(t *testing.T) {
	r, pool, _ := onboardingIntegrationRouter(t, true)
	owner, stranger := uuid.New(), uuid.New()
	rid, base := createRestaurantViaAPI(t, r, owner)

	for step, path := range map[string]string{
		"compliance": base + "/compliance", "location": base + "/location",
		"operating_hours": base + "/operating-hours", "fssai_document": base + "/fssai",
	} {
		rec := doJSON(r, http.MethodGet, path, ``, owner, false)
		expectStatus(t, rec, http.StatusNotFound, "unsaved "+step)
		env := decodeEnvelope(t, rec)
		if env.Error == nil || env.Error.Code != "FOOD_ONBOARDING_STEP_NOT_SAVED" || env.Error.Details["step"] != step {
			t.Fatalf("unsaved %s: %s", step, rec.Body.String())
		}
	}

	for _, put := range []struct{ method, path, body string }{
		{http.MethodPut, base + "/compliance", itComplianceECO},
		{http.MethodPut, base + "/location", itLocation},
		{http.MethodPut, base + "/operating-hours", itHours},
		{http.MethodPut, base + "/fssai", itFSSAIBody("10099999000000")},
		{http.MethodPut, base + "/payout-account", itPayout},
	} {
		expectStatus(t, doJSON(r, put.method, put.path, put.body, owner, false), http.StatusOK, put.path)
	}
	seedAvailableMenuItem(t, pool, rid)

	panHex := hex.EncodeToString([]byte(itPAN))
	secrets := []string{itPAN, strings.ToLower(itPAN), itAccount, itAccount[:8], panHex}
	for _, path := range []string{base + "/readiness", base + "/compliance", base + "/location", base + "/operating-hours",
		base + "/fssai", base, base + "/payout-account", base + "/menu/categories"} {
		rec := doJSON(r, http.MethodGet, path, ``, owner, false)
		expectStatus(t, rec, http.StatusOK, "owner GET "+path)
		for _, secret := range secrets {
			if strings.Contains(rec.Body.String(), secret) {
				t.Fatalf("GET %s carries a plaintext identifier", path)
			}
		}
	}
	rec := doJSON(r, http.MethodGet, base+"/compliance", ``, owner, false)
	if masked, _ := dataField(t, rec, "pan_masked").(string); !strings.HasPrefix(masked, "****") || len(masked) != 8 {
		t.Fatalf("pan_masked = %q", masked)
	}
	if ready := dataField(t, doJSON(r, http.MethodGet, base+"/readiness", ``, owner, false), "ready"); ready != true {
		t.Fatalf("readiness after every step: %v", ready)
	}

	for _, path := range []string{base + "/readiness", base + "/compliance", base + "/location", base + "/operating-hours", base + "/fssai"} {
		rec := doJSON(r, http.MethodGet, path, ``, stranger, false)
		if rec.Code != http.StatusNotFound || errorCode(t, rec) != "FOOD_NOT_FOUND" {
			t.Errorf("stranger GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

// PATCH /v1/food/partner/restaurants/:id over HTTP keeps what the body omits.
func TestB8PatchRestaurantIsPartialOverHTTP(t *testing.T) {
	r, _, _ := onboardingIntegrationRouter(t, false)
	owner := uuid.New()
	rec := doJSON(r, http.MethodPost, "/v1/food/partner/restaurants",
		`{"name":"IT Patch `+owner.String()[:8]+`","slug":"it-patch-`+owner.String()+`","description":"Old","phone":"+919000000002",`+
			`"email":"patch@example.test","address_line1":"1 Test Lane","city":"Bengaluru","display_name":"Patch Display","min_order_amount":99,"packaging_fee":10}`,
		owner, false)
	expectStatus(t, rec, http.StatusCreated, "create")
	base := "/v1/food/partner/restaurants/" + dataField(t, rec, "id").(string)

	rec = doJSON(r, http.MethodPatch, base, `{"description":"New"}`, owner, false)
	expectStatus(t, rec, http.StatusOK, "patch description")
	for field, want := range map[string]any{"description": "New", "phone": "+919000000002", "email": "patch@example.test",
		"display_name": "Patch Display", "min_order_amount": 99.0, "packaging_fee": 10.0} {
		if got := dataField(t, rec, field); got != want {
			t.Errorf("after a description PATCH, %s = %v, want %v", field, got, want)
		}
	}

	rec = doJSON(r, http.MethodPatch, base, `{"phone":null}`, owner, false)
	expectStatus(t, rec, http.StatusOK, "clear phone")
	if dataField(t, rec, "phone") != nil || dataField(t, rec, "email") != "patch@example.test" {
		t.Fatalf("clear phone: %s", rec.Body.String())
	}

	rec = doJSON(r, http.MethodPatch, base, `{"name":null}`, owner, false)
	if rec.Code != http.StatusBadRequest || errorCode(t, rec) != "FOOD_PARTNER_RESTAURANT_UPDATE_FAILED" {
		t.Fatalf("null name: %d %s", rec.Code, rec.Body.String())
	}
}

// Variants, add-on groups and add-ons over HTTP: the owner manages them, a
// stranger gets 404, the item read carries them, and a dish photo resolves.
func TestB8MenuExtrasOverHTTP(t *testing.T) {
	r, _, _ := onboardingIntegrationRouter(t, false)
	owner, stranger := uuid.New(), uuid.New()
	_, base := createRestaurantViaAPI(t, r, owner)

	rec := doJSON(r, http.MethodPost, base+"/menu/categories", `{"name":"Mains","sort_order":1}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "category")
	categoryID := dataField(t, rec, "id").(string)
	expectStatus(t, doJSON(r, http.MethodPost, base+"/menu/categories", `{"name":"Desserts","sort_order":2}`, owner, false), http.StatusCreated, "empty category")

	media := uuid.NewString()
	rec = doJSON(r, http.MethodPost, base+"/menu/items", `{"category_id":"`+categoryID+`","name":"Idli","food_type":"VEG","base_price":60.5,`+
		`"preparation_minutes":10,"tax_percentage":5,"image_media_id":"`+media+`"}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "item")
	if dataField(t, rec, "image_media_id") != media || !strings.HasSuffix(dataField(t, rec, "image_url").(string), "/v1/media/"+media+"/serve") ||
		dataField(t, rec, "base_price_paise") != 6050.0 {
		t.Fatalf("item with a photo: %s", rec.Body.String())
	}
	itemID := dataField(t, rec, "id").(string)
	rec = doJSON(r, http.MethodPost, base+"/menu/items", `{"category_id":"`+categoryID+`","name":"Vada","food_type":"VEG","base_price":40,"image_media_id":"nope"}`, owner, false)
	if rec.Code != http.StatusUnprocessableEntity || errorCode(t, rec) != "FOOD_MEDIA_ID_INVALID" {
		t.Fatalf("bad image_media_id: %d %s", rec.Code, rec.Body.String())
	}

	item := "/v1/food/partner/menu/items/" + itemID
	rec = doJSON(r, http.MethodPost, item+"/variants", `{"name":"Half","price_paise":3500}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "variant")
	variantID := dataField(t, rec, "id").(string)
	if dataField(t, rec, "price") != 35.0 {
		t.Fatalf("variant: %s", rec.Body.String())
	}
	rec = doJSON(r, http.MethodPost, item+"/addon-groups", `{"name":"Chutneys","max_select":2}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "group")
	groupID := dataField(t, rec, "id").(string)
	rec = doJSON(r, http.MethodPost, item+"/addon-groups/"+groupID+"/addons", `{"name":"Coconut","price":10}`, owner, false)
	expectStatus(t, rec, http.StatusCreated, "add-on")
	if dataField(t, rec, "price_paise") != 1000.0 {
		t.Fatalf("add-on: %s", rec.Body.String())
	}

	for _, attempt := range []struct{ method, path, body string }{
		{http.MethodPost, item + "/variants", `{"name":"X","price_paise":1}`},
		{http.MethodPatch, item + "/variants/" + variantID, `{"price_paise":1}`},
		{http.MethodPost, item + "/addon-groups/" + groupID + "/addons", `{"name":"X","price_paise":1}`},
		{http.MethodDelete, item + "/addon-groups/" + groupID, ``},
		{http.MethodGet, item, ``},
	} {
		rec := doJSON(r, attempt.method, attempt.path, attempt.body, stranger, false)
		if rec.Code != http.StatusNotFound || errorCode(t, rec) != "FOOD_NOT_FOUND" {
			t.Errorf("stranger %s %s: %d %s", attempt.method, attempt.path, rec.Code, rec.Body.String())
		}
	}

	rec = doJSON(r, http.MethodPatch, item+"/variants/"+variantID, `{"price":40}`, owner, false)
	expectStatus(t, rec, http.StatusOK, "variant patch")
	if dataField(t, rec, "name") != "Half" || dataField(t, rec, "price_paise") != 4000.0 {
		t.Fatalf("variant patch: %s", rec.Body.String())
	}
	rec = doJSON(r, http.MethodGet, item, ``, owner, false)
	expectStatus(t, rec, http.StatusOK, "item read")
	groups, _ := dataField(t, rec, "addon_groups").([]any)
	variants, _ := dataField(t, rec, "variants").([]any)
	if len(variants) != 1 || len(groups) != 1 || len(groups[0].(map[string]any)["addons"].([]any)) != 1 {
		t.Fatalf("item read: %s", rec.Body.String())
	}

	rec = doJSON(r, http.MethodGet, base+"/menu/categories", ``, owner, false)
	expectStatus(t, rec, http.StatusOK, "categories")
	cats, _ := dataField(t, rec, "items").([]any)
	var desserts map[string]any
	for _, c := range cats {
		if m := c.(map[string]any); m["name"] == "Desserts" {
			desserts = m
		}
	}
	if len(cats) != 2 || desserts == nil || desserts["item_count"] != 0.0 {
		t.Fatalf("categories: %s", rec.Body.String())
	}

	expectStatus(t, doJSON(r, http.MethodDelete, item+"/addon-groups/"+groupID, ``, owner, false), http.StatusOK, "delete group")
	rec = doJSON(r, http.MethodGet, item+"/addon-groups", ``, owner, false)
	if left, _ := dataField(t, rec, "items").([]any); rec.Code != http.StatusOK || len(left) != 0 {
		t.Fatalf("groups after delete: %s", rec.Body.String())
	}
}
