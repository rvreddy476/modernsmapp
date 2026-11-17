//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Helpers for the commerce flows only (commerce_journey_test.go and
// commerce_prepaid_test.go). Every route, field and code below is the
// committed service's own; the file named beside each is where it lives.

// commerceAdminEnv carries the user id the stack's identity-auth lists in
// SUPERADMIN_USER_IDS (identity-platform auth-service internal/config
// config.go, EnvRolesForUser): the committed bootstrap grant, resolved to
// every admin permission by GET /v1/auth/internal/users/:id/permissions,
// which is what admin-service asks before it lets an admin route through
// (admin-service internal/http/gate.go).
const commerceAdminEnv = "ATPOST_E2E_ADMIN_USER"

// Prices of the one variant every commerce flow sells, in paise. The stub
// courier quotes a flat 4900 (commerce-service internal/courier/courier.go).
const (
	commerceMRPMinor   = 100000
	commercePriceMinor = 90000
)

// commerceSessionToken mints an HS256 access token the way client.go's
// mintToken does, with one extra claim: the session kind. The gateway takes
// the kind from `sk` (api-gateway pkg/tokenpolicy, pkg/adminsession): "admin"
// is an admin console session, absent is a consumer one.
//
// This is the development sign-in of this harness, not the console's: outside
// production the gateway accepts tokens signed with the stack's shared dev
// secret (tokenpolicy.LoadFromEnv, AllowHS256), which is how every synthetic
// user here signs in. It carries no admin_mfa and no step_up_at; admin-service
// on a local stack does not require the first (adminauth.RequireMFAFromEnv)
// and neither approval route asks for the second.
func commerceSessionToken(userID uuid.UUID, sessionKind string) string {
	b64 := func(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }
	header, _ := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT", "kid": jwtKID()})
	claims := map[string]any{
		"sub":     userID.String(),
		"user_id": userID.String(),
		"iat":     time.Now().Unix(),
		"exp":     time.Now().Add(time.Hour).Unix(),
	}
	if sessionKind != "" {
		claims["sk"] = sessionKind
	}
	body, _ := json.Marshal(claims)
	signingInput := b64(header) + "." + b64(body)
	mac := hmac.New(sha256.New, []byte(jwtSecret()))
	mac.Write([]byte(signingInput))
	return signingInput + "." + b64(mac.Sum(nil))
}

// commerceConsoleClient is a client that talks to /v1/admin the way the admin
// console does: the token in the admin_access_token cookie and nowhere else,
// plus the double-submit CSRF pair on writes (api-gateway pkg/adminsession).
// csrfHeader false leaves the X-CSRF-Token header off.
func commerceConsoleClient(base, token string, csrfHeader bool) *HTTPClient {
	csrf := uuid.NewString()
	c := NewHTTPClient(base, uuid.Nil).
		WithHeader("Cookie", "admin_access_token="+token+"; admin_csrf_token="+csrf)
	if csrfHeader {
		c = c.WithHeader("X-CSRF-Token", csrf)
	}
	return c
}

// commerceAdmin returns the console client of the stack's bootstrap admin.
// Without ATPOST_E2E_ADMIN_USER the flow stops here: a skip on a developer
// machine, a failure where the stack is required.
func commerceAdmin(t *testing.T, base string) *HTTPClient {
	t.Helper()
	id, err := uuid.Parse(os.Getenv(commerceAdminEnv))
	if err != nil {
		missingStack(t, "set %s to the user id identity-auth lists in SUPERADMIN_USER_IDS; "+
			"approving a seller or a product needs an admin session admin-service can resolve permissions for "+
			"(stack: identity-auth, admin-service with ADMIN_SERVICE_TOKEN_KEY, commerce-service with SERVICE_CALLERS=admin-service)",
			commerceAdminEnv)
	}
	return commerceConsoleClient(base, commerceSessionToken(id, "admin"), true)
}

// must fails the test with the server's error + body when status is not 2xx,
// and returns the response Data for field extraction.
func must(t *testing.T, label string, env *Envelope) json.RawMessage {
	t.Helper()
	if env.Status < 200 || env.Status >= 300 {
		code, msg := "", ""
		if env.Error != nil {
			code, msg = env.Error.Code, env.Error.Message
		}
		t.Fatalf("%s -> %d %s %s | data=%s", label, env.Status, code, msg, string(env.Data))
	}
	t.Logf("%s -> %d", label, env.Status)
	return env.Data
}

// commerceRefused stops the flow unless env is exactly the refusal named: the
// HTTP status and the error code.
func commerceRefused(t *testing.T, label string, env *Envelope, status int, code string) {
	t.Helper()
	got := ""
	if env.Error != nil {
		got = env.Error.Code
	}
	if env.Status != status || got != code {
		t.Fatalf("%s: want %d %s, got %d %q", label, status, code, env.Status, got)
	}
	t.Logf("%s -> %d %s", label, status, code)
}

// field pulls a top-level string field (e.g. "id") out of a data object.
func field(t *testing.T, raw json.RawMessage, name string) string {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse object for %q: %v | raw=%s", name, err, string(raw))
	}
	v, ok := m[name]
	if !ok {
		t.Fatalf("field %q missing in: %s", name, string(raw))
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		t.Fatalf("field %q not a string: %s", name, string(v))
	}
	return s
}

// firstID returns the "id" of the first element, tolerating either a bare
// array or a wrapper object ({items|variants|data:[...]}).
func firstID(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var arr []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		var wrap map[string]json.RawMessage
		if err2 := json.Unmarshal(raw, &wrap); err2 != nil {
			t.Fatalf("firstID: not array or object: %s", string(raw))
		}
		for _, k := range []string{"items", "variants", "data", "results"} {
			if inner, ok := wrap[k]; ok {
				return firstID(t, inner)
			}
		}
		t.Fatalf("firstID: no list field in: %s", string(raw))
	}
	if len(arr) == 0 {
		t.Fatalf("firstID: empty list")
	}
	var s string
	if err := json.Unmarshal(arr[0]["id"], &s); err != nil {
		t.Fatalf("firstID: element has no string id: %s", string(raw))
	}
	return s
}

// commerceOpenShop starts the seller's onboarding and gives the shop what a
// quote needs later: a fulfilment setting and a pickup address (the origin
// pincode of the courier quote and the seller half of the GST place of
// supply; commerce-service internal/service/checkout_p0.go PrepareQuote). It
// returns the seller id. The shop is a draft: nothing here approves it.
func commerceOpenShop(t *testing.T, ctx context.Context, seller *HTTPClient, storeName string) string {
	t.Helper()
	started := must(t, "seller onboarding/start",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/onboarding/start", map[string]any{
			"store_name":    storeName,
			"email":         "seller-" + uuid.NewString()[:8] + "@example.com",
			"seller_type":   "individual",
			"business_type": "individual",
		}))
	sellerID := field(t, started, "id")
	if st := field(t, started, "status"); st != "draft" {
		t.Fatalf("a new shop is %q, want draft", st)
	}
	must(t, "seller fulfilment step",
		seller.MustDo(t, ctx, "PUT", "/v1/commerce/onboarding/step/fulfillment", map[string]any{
			"delivery_modes":     []string{"standard"},
			"cod_enabled":        false,
			"dispatch_sla_hours": 24,
			"return_supported":   true,
			"return_window_days": 7,
		}))
	must(t, "seller pickup address",
		seller.MustDo(t, ctx, "PUT", "/v1/commerce/seller/address", map[string]any{
			"address_type":   "pickup",
			"contact_name":   "E2E Seller",
			"phone":          "9888888888",
			"address_line_1": "2 Warehouse Road",
			"city":           "Bengaluru",
			"state":          "KA",
			"postal_code":    "560001",
			"country":        "IN",
			"is_default":     true,
		}))
	return sellerID
}

// commerceShopStatus reads the seller's own application status.
func commerceShopStatus(t *testing.T, ctx context.Context, seller *HTTPClient) string {
	t.Helper()
	return field(t, must(t, "seller reads onboarding status",
		seller.MustDo(t, ctx, "GET", "/v1/commerce/onboarding/status", nil)), "status")
}

// commerceTaxClass picks a GST class from the public rate table. A product
// cannot be created without one (handler_catalogue.go, TAX_CLASS_REQUIRED).
func commerceTaxClass(t *testing.T, ctx context.Context, c *HTTPClient) string {
	t.Helper()
	raw := must(t, "list tax classes", c.MustDo(t, ctx, "GET", "/v1/commerce/tax-classes", nil))
	var body struct {
		Items []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"items"`
	}
	if err := json.Unmarshal(raw, &body); err != nil || len(body.Items) == 0 {
		t.Fatalf("tax classes: none listed (err=%v raw=%s)", err, string(raw))
	}
	for _, it := range body.Items {
		if it.Name == "GST 18%" {
			return it.ID
		}
	}
	return body.Items[0].ID
}

// commerceProductBody is the create body of the one listing the flows sell:
// one variant, priced in paise, fifty in stock.
func commerceProductBody(title, sku, taxClassID string) map[string]any {
	body := map[string]any{
		"title":              title,
		"product_type":       "physical",
		"condition":          "new",
		"return_policy_type": "7_days",
		"return_policy_days": 7,
		"weight_grams":       250,
		"variants": []map[string]any{{
			"sku":                 sku,
			"mrp_minor":           commerceMRPMinor,
			"selling_price_minor": commercePriceMinor,
			"stock_qty":           50,
		}},
	}
	if taxClassID != "" {
		body["tax_class_id"] = taxClassID
	}
	return body
}

// commerceDraftProduct creates the listing as a draft, with no image yet, and
// returns its id and its variant's id.
func commerceDraftProduct(t *testing.T, ctx context.Context, seller *HTTPClient, title string) (productID, variantID string) {
	t.Helper()
	sku := "E2E-" + uuid.NewString()[:8]
	prod := must(t, "create product",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products", commerceProductBody(title, sku, commerceTaxClass(t, ctx, seller))))
	productID = field(t, prod, "id")
	if st := field(t, prod, "approval_status"); st != "draft" {
		t.Fatalf("a new product is %q, want draft", st)
	}
	variantID = firstID(t, must(t, "list variants",
		seller.MustDo(t, ctx, "GET", "/v1/commerce/products/"+productID+"/variants", nil)))
	return productID, variantID
}

// commerceUploadImage uploads a real PNG as owner through media-service
// (init, PUT to the presigned URL with the signed headers, confirm) and
// returns the media id once it reads "ready". commerce-service accepts a
// product image only when media-service says it is the seller's own, ready
// and passed (commerce-service internal/media/media.go VerifyOwned), so a
// listing cannot be submitted on a stack without media-service.
func commerceUploadImage(t *testing.T, ctx context.Context, owner *HTTPClient) string {
	t.Helper()
	const side = 64
	img := image.NewNRGBA(image.Rect(0, 0, side, side))
	for y := 0; y < side; y++ {
		for x := 0; x < side; x++ {
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(x * 255 / side), G: uint8(y * 255 / side), B: 0x60, A: 0xFF})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode product image: %v", err)
	}
	payload := buf.Bytes()

	initRaw := must(t, "media init (product image)",
		owner.MustDo(t, ctx, "POST", "/v1/media/init", map[string]any{
			"file_type":       "image",
			"mime_type":       "image/png",
			"file_size_bytes": len(payload),
		}))
	var init struct {
		MediaID       string            `json:"media_id"`
		UploadURL     string            `json:"upload_url"`
		UploadHeaders map[string]string `json:"upload_headers"`
	}
	if err := json.Unmarshal(initRaw, &init); err != nil || init.MediaID == "" || init.UploadURL == "" {
		t.Fatalf("media init: media_id/upload_url missing (err=%v raw=%s)", err, string(initRaw))
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, init.UploadURL, bytes.NewReader(payload))
	if err != nil {
		t.Fatalf("build presigned PUT: %v", err)
	}
	put.Header.Set("Content-Type", "image/png")
	for k, v := range init.UploadHeaders {
		// Content-Length is signed too; it comes from the reader.
		if http.CanonicalHeaderKey(k) == "Content-Length" {
			continue
		}
		put.Header.Set(k, v)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(put)
	if err != nil {
		t.Fatalf("PUT product image to the object store: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Fatalf("PUT product image: got %d, want 2xx", resp.StatusCode)
	}
	confirmed := must(t, "media confirm (product image)",
		owner.MustDo(t, ctx, "POST", "/v1/media/confirm", map[string]any{"media_id": init.MediaID}))
	if st := field(t, confirmed, "processing_status"); st != "ready" {
		t.Fatalf("product image is %q after confirm, want ready (raw=%s)", st, string(confirmed))
	}
	return init.MediaID
}

// commerceAttachImage uploads an image as the seller and makes it the
// listing's gallery (POST /products/:id/media, media_ids sets the gallery).
func commerceAttachImage(t *testing.T, ctx context.Context, seller *HTTPClient, productID string) {
	t.Helper()
	mediaID := commerceUploadImage(t, ctx, seller)
	must(t, "set product gallery",
		seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/media", map[string]any{
			"media_ids": []string{mediaID},
		}))
}

// commerceApprove performs one approval through admin-service's own route as
// the given console session. what is "sellers" or "products".
func commerceApprove(t *testing.T, ctx context.Context, admin *HTTPClient, what, id string) *Envelope {
	t.Helper()
	// reason and notes: commerce records the notes; a reason is what an
	// approval is audited with.
	return admin.MustDo(t, ctx, "POST", "/v1/admin/commerce/"+what+"/"+id+"/approve", map[string]any{
		"reason": "e2e flow approval",
		"notes":  "approved by the e2e flow",
	})
}

// commerceLiveListing takes a fresh seller all the way to a listing a shopper
// can buy: shop opened, product drafted with an image, shop approved by an
// admin, product submitted, product approved by an admin. It returns the
// variant id. The journey flow walks the same steps one by one and asserts
// what each refuses; this is the short form for flows about what comes after.
func commerceLiveListing(t *testing.T, ctx context.Context, admin, seller *HTTPClient, storeName string) string {
	t.Helper()
	sellerID := commerceOpenShop(t, ctx, seller, storeName)
	productID, variantID := commerceDraftProduct(t, ctx, seller, storeName+" Widget")
	commerceAttachImage(t, ctx, seller, productID)
	must(t, "admin approves the seller", commerceApprove(t, ctx, admin, "sellers", sellerID))
	must(t, "submit product", seller.MustDo(t, ctx, "POST", "/v1/commerce/products/"+productID+"/submit", nil))
	must(t, "admin approves the product", commerceApprove(t, ctx, admin, "products", productID))
	return variantID
}

// commerceAddAddress saves the customer's delivery address and returns its id.
func commerceAddAddress(t *testing.T, ctx context.Context, customer *HTTPClient) string {
	t.Helper()
	return field(t, must(t, "add address",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/addresses", map[string]any{
			"address_type":   "home",
			"contact_name":   "E2E Buyer",
			"phone":          "9999999999",
			"address_line_1": "1 Integration Way",
			"city":           "Bengaluru",
			"state":          "KA",
			"postal_code":    "560002",
			"country":        "IN",
			"is_default":     true,
		})), "id")
}

// commerceQuote is the slice of POST /v1/commerce/checkout/quote the flows
// read (commerce-service internal/service/checkout_p0.go QuoteResult).
type commerceQuote struct {
	QuoteID       string `json:"quote_id"`
	SubtotalMinor int64  `json:"subtotal_minor"`
	DiscountMinor int64  `json:"discount_minor"`
	ShippingMinor int64  `json:"shipping_minor"`
	TotalMinor    int64  `json:"total_minor"`
	CourierCode   string `json:"courier_code"`
	Serviceable   bool   `json:"serviceable"`
}

// commerceOrder is the slice of the checkout answer the flows read
// (CheckoutOutputP0, same file).
type commerceOrder struct {
	OrderID    string `json:"order_id"`
	TotalMinor int64  `json:"total_minor"`
	IntentID   string `json:"payment_intent_id"`
}

// commerceCartAndQuote puts one unit of the variant in the customer's cart
// and prices it for the address, to be paid by UPI. Cash on delivery is not a
// launch method (handler_p0.go CheckoutP0, COD_NOT_SUPPORTED).
func commerceCartAndQuote(t *testing.T, ctx context.Context, customer *HTTPClient, label, variantID, addressID string) commerceQuote {
	t.Helper()
	must(t, label+": add to cart",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/cart/items", map[string]any{"variant_id": variantID, "quantity": 1}))
	raw := must(t, label+": quote",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/checkout/quote", map[string]any{
			"address_id": addressID, "payment_method": "upi",
		}))
	var q commerceQuote
	if err := json.Unmarshal(raw, &q); err != nil || q.QuoteID == "" || !q.Serviceable {
		t.Fatalf("%s: quote unusable (err=%v raw=%s)", label, err, string(raw))
	}
	if q.SubtotalMinor != commercePriceMinor {
		t.Fatalf("%s: quote subtotal %d, want the variant's price %d", label, q.SubtotalMinor, commercePriceMinor)
	}
	if q.ShippingMinor <= 0 || q.TotalMinor != q.SubtotalMinor-q.DiscountMinor+q.ShippingMinor {
		t.Fatalf("%s: quote total %d is not subtotal %d - discount %d + shipping %d",
			label, q.TotalMinor, q.SubtotalMinor, q.DiscountMinor, q.ShippingMinor)
	}
	return q
}

// commerceCheckoutBody is the v2 checkout body for a quote: the quote id and
// the total the customer was shown are both required.
func commerceCheckoutBody(addressID string, q commerceQuote, method string) map[string]any {
	return map[string]any{
		"address_id":           addressID,
		"quote_id":             q.QuoteID,
		"payment_method":       method,
		"expected_total_minor": q.TotalMinor,
	}
}

// commercePlaceOrder quotes and checks out one unit, and returns the order.
// The order is unpaid: payment_pending / pending.
func commercePlaceOrder(t *testing.T, ctx context.Context, customer *HTTPClient, label, variantID, addressID string) commerceOrder {
	t.Helper()
	q := commerceCartAndQuote(t, ctx, customer, label, variantID, addressID)
	raw := must(t, label+": checkout (upi)",
		customer.WithHeader("Idempotency-Key", uuid.NewString()).
			MustDo(t, ctx, "POST", "/v1/commerce/v2/orders/checkout", commerceCheckoutBody(addressID, q, "upi")))
	var o commerceOrder
	if err := json.Unmarshal(raw, &o); err != nil || o.OrderID == "" {
		t.Fatalf("%s: checkout answer unusable (err=%v raw=%s)", label, err, string(raw))
	}
	if o.TotalMinor != q.TotalMinor {
		t.Fatalf("%s: order total %d, want the quoted %d", label, o.TotalMinor, q.TotalMinor)
	}
	if s, ps := commerceOrderState(t, ctx, customer, o.OrderID); s != "payment_pending" || ps != "pending" {
		t.Fatalf("%s: a fresh prepaid order is %s/%s, want payment_pending/pending", label, s, ps)
	}
	return o
}

// commerceOrderState reads the order as its customer: (status, payment_status).
func commerceOrderState(t *testing.T, ctx context.Context, customer *HTTPClient, orderID string) (status, paymentStatus string) {
	t.Helper()
	raw := must(t, "get order", customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+orderID, nil))
	return field(t, raw, "status"), field(t, raw, "payment_status")
}

// commerceIntent is the slice of POST /orders/:id/payment/intent the flows
// read (handler_p0.go OpenPaymentIntent). There is no amount in the request:
// commerce authors it from the order.
type commerceIntent struct {
	ID            string            `json:"payment_intent_id"`
	AmountMinor   int64             `json:"amount_minor"`
	ProviderRef   string            `json:"provider_ref"`
	Status        string            `json:"status"`
	ClientSession map[string]string `json:"client_session"`
}

// commerceOpenIntent opens (or reads back) the order's payment intent, which
// commerce-service creates at payments-service in-cluster.
func commerceOpenIntent(t *testing.T, ctx context.Context, customer *HTTPClient, label string, o commerceOrder) commerceIntent {
	t.Helper()
	raw := must(t, label+": open payment intent",
		customer.MustDo(t, ctx, "POST", "/v1/commerce/orders/"+o.OrderID+"/payment/intent", nil))
	var in commerceIntent
	if err := json.Unmarshal(raw, &in); err != nil || in.ID == "" || in.ProviderRef == "" {
		t.Fatalf("%s: intent answer unusable (err=%v raw=%s)", label, err, string(raw))
	}
	if in.AmountMinor != o.TotalMinor {
		t.Fatalf("%s: intent is for %d, want the order total %d", label, in.AmountMinor, o.TotalMinor)
	}
	if o.IntentID != "" && o.IntentID != in.ID {
		t.Fatalf("%s: checkout bound intent %s, the intent route answered %s", label, o.IntentID, in.ID)
	}
	return in
}

// commerceStubConfirmBody is the body of the dev stack's settlement call
// (handler_orders.go ConfirmPayment). commerce-service asks payments-service
// to verify it against the intent before the order moves, and settles only
// for gateway "stub" on a stack whose payments-service has no real provider.
func commerceStubConfirmBody(in commerceIntent, paymentID string, amountMinor int64) map[string]any {
	return map[string]any{
		"payment_intent_id":   in.ID,
		"razorpay_order_id":   in.ProviderRef,
		"razorpay_payment_id": paymentID,
		"razorpay_signature":  "stub-signature",
		"amount_minor":        amountMinor,
		"gateway":             "stub",
	}
}

// commercePaymentState is the customer's three-state payment read
// (GET /orders/:id/payment): confirming | paid | failed, and the amount.
func commercePaymentState(t *testing.T, ctx context.Context, customer *HTTPClient, orderID string) (status string, amountMinor int64) {
	t.Helper()
	raw := must(t, "customer reads the order's payment",
		customer.MustDo(t, ctx, "GET", "/v1/commerce/orders/"+orderID+"/payment", nil))
	var st struct {
		Status      string `json:"status"`
		AmountMinor int64  `json:"amount_minor"`
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("payment state: %v (raw=%s)", err, string(raw))
	}
	return st.Status, st.AmountMinor
}

// commercePaidStatus reports whether an order status is one a paid order
// passes through. Payment lands it in "confirmed"; the fulfilment worker then
// books the stub courier and moves it on, so no flow asserts "confirmed".
func commercePaidStatus(status string) bool {
	switch status {
	case "confirmed", "packed", "shipped", "out_for_delivery", "delivered":
		return true
	}
	return false
}
