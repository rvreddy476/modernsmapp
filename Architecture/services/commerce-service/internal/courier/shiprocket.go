package courier

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

// ShiprocketCourier implements Provider over Shiprocket's REST API.
// Docs: https://apidocs.shiprocket.in/
type ShiprocketCourier struct {
	email          string
	password       string
	webhookToken   string // shared-secret token Shiprocket sends as 'X-Api-Key'
	pickupLocation string // the nickname of a pickup address registered in the Shiprocket account
	webhookHMAC    string // optional HMAC-SHA256 secret if using signed webhooks
	token          string
	tokenAt        time.Time
	mu             sync.Mutex
	base           string
	http           *http.Client
}

func NewShiprocket(email, password string) *ShiprocketCourier {
	// Phase F3.2 — otelhttp wraps the courier's transport so Shiprocket
	// call latency lands in Jaeger as a child of the shipment span.
	return &ShiprocketCourier{
		email:          email,
		password:       password,
		pickupLocation: DefaultPickupLocation,
		base:           "https://apiv2.shiprocket.in/v1/external",
		http: &http.Client{
			Timeout:   20 * time.Second,
			Transport: otelhttp.NewTransport(http.DefaultTransport),
		},
	}
}

// WithWebhookSecrets sets the pre-shared token and/or HMAC key used to
// authenticate inbound Shiprocket webhook callbacks.
func (c *ShiprocketCourier) WithWebhookSecrets(token, hmacKey string) *ShiprocketCourier {
	c.webhookToken = token
	c.webhookHMAC = hmacKey
	return c
}

// VerifyWebhook checks the shared-token header ('X-Api-Key' or 'Token') and/or
// an HMAC-SHA256 signature ('X-Signature'). If neither secret is configured
// the webhook is rejected — prefer explicit config over silent acceptance.
func (c *ShiprocketCourier) VerifyWebhook(headers map[string]string, body []byte) error {
	if c.webhookToken == "" && c.webhookHMAC == "" {
		return errors.New("shiprocket webhook secret not configured")
	}
	lower := make(map[string]string, len(headers))
	for k, v := range headers {
		lower[strings.ToLower(k)] = v
	}
	if c.webhookToken != "" {
		for _, got := range []string{lower["x-api-key"], lower["token"], lower["x-token"]} {
			if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(c.webhookToken)) == 1 {
				return nil
			}
		}
	}
	if c.webhookHMAC != "" {
		if sig := lower["x-signature"]; sig != "" {
			mac := hmac.New(sha256.New, []byte(c.webhookHMAC))
			mac.Write(body)
			if subtle.ConstantTimeCompare([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) == 1 {
				return nil
			}
		}
	}
	return errors.New("shiprocket webhook signature invalid")
}

func (c *ShiprocketCourier) Name() string { return "shiprocket" }

// authToken fetches + caches bearer token (valid for 24h per docs).
func (c *ShiprocketCourier) authToken(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && time.Since(c.tokenAt) < 20*time.Hour {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]string{"email": c.email, "password": c.password})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+"/auth/login", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("shiprocket auth: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("shiprocket auth %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	c.token, c.tokenAt = out.Token, time.Now()
	return c.token, nil
}

func (c *ShiprocketCourier) CreateShipment(ctx context.Context, req ShipmentRequest) (*ShipmentResponse, error) {
	tok, err := c.authToken(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, map[string]any{
			"name":          it.Name,
			"sku":           it.SKU,
			"units":         it.Quantity,
			"selling_price": it.Price,
			"hsn":           it.HSN,
		})
	}
	payload := map[string]any{
		"order_id":              req.OrderNumber,
		"order_date":            time.Now().Format("2006-01-02 15:04"),
		"pickup_location":       c.pickupLocation,
		"billing_customer_name": req.DropAddress.Name,
		"billing_last_name":     "",
		"billing_address":       req.DropAddress.Line1,
		"billing_address_2":     req.DropAddress.Line2,
		"billing_city":          req.DropAddress.City,
		"billing_pincode":       req.DropAddress.Postal,
		"billing_state":         req.DropAddress.State,
		"billing_country":       req.DropAddress.Country,
		"billing_email":         req.DropAddress.Email,
		"billing_phone":         req.DropAddress.Phone,
		"shipping_is_billing":   true,
		"order_items":           items,
		"payment_method":        paymentMode(req.PaymentMethod),
		"sub_total":             req.PackageValue,
		"weight":                req.Weight,
		// Shiprocket validates these four together: an order with a weight
		// and no dimensions is rejected 422 before it reaches a courier.
		"length":  dimensionOrDefault(req.LengthCm),
		"breadth": dimensionOrDefault(req.BreadthCm),
		"height":  dimensionOrDefault(req.HeightCm),
	}
	if req.PaymentMethod == "cod" {
		payload["cod_charges"] = req.CODAmount
	}
	body, _ := json.Marshal(payload)
	httpReq, _ := http.NewRequestWithContext(ctx, "POST", c.base+"/orders/create/adhoc", bytes.NewReader(body))
	httpReq.Header.Set("Authorization", "Bearer "+tok)
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("shiprocket create: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shiprocket create %d: %s", resp.StatusCode, string(raw))
	}
	var out struct {
		ShipmentID int    `json:"shipment_id"`
		OrderID    int    `json:"order_id"`
		AWBCode    string `json:"awb_code"`
		LabelURL   string `json:"label_url"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	// Shiprocket answers 200 with an empty body when it rejects the order
	// for a reason it does not treat as an HTTP error — an unregistered
	// pickup_location above all. Without this the caller books a shipment
	// that exists nowhere but our database.
	if out.OrderID == 0 {
		return nil, fmt.Errorf("shiprocket create: no order id in response (pickup_location %q registered?): %s",
			c.pickupLocation, string(raw))
	}
	return &ShipmentResponse{
		CourierOrderID: fmt.Sprintf("%d", out.OrderID),
		AWBNumber:      out.AWBCode,
		LabelURL:       out.LabelURL,
		TrackingURL:    fmt.Sprintf("https://shiprocket.co/tracking/%s", out.AWBCode),
		EstimatedETA:   time.Now().Add(72 * time.Hour),
		RawResponse:    raw,
	}, nil
}

func paymentMode(pm string) string {
	if pm == "cod" {
		return "COD"
	}
	return "Prepaid"
}

func (c *ShiprocketCourier) CancelShipment(ctx context.Context, awb string) error {
	tok, err := c.authToken(ctx)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"awbs": []string{awb}})
	req, _ := http.NewRequestWithContext(ctx, "POST", c.base+"/orders/cancel/shipment/awbs", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("shiprocket cancel %d: %s", resp.StatusCode, string(raw))
	}
	return nil
}

// ParseWebhook understands Shiprocket's tracking webhook payload shape.
// Ref: https://apidocs.shiprocket.in/#a8e1ed62-34d8-4b6a-8eea-6c9a7dec3b38
func (c *ShiprocketCourier) ParseWebhook(_ context.Context, payload []byte) ([]TrackingUpdate, error) {
	var wh struct {
		AWB                     string `json:"awb"`
		CurrentStatus           string `json:"current_status"`
		CurrentStatusBody       string `json:"current_status_body"`
		ShipmentStatus          string `json:"shipment_status"`
		ShipmentTrackActivities []struct {
			Activity string `json:"activity"`
			Location string `json:"location"`
			Date     string `json:"date"`
			Status   string `json:"status"`
		} `json:"shipment_track_activities"`
	}
	if err := json.Unmarshal(payload, &wh); err != nil {
		return nil, err
	}
	mapStatus := func(s string) string {
		switch s {
		case "DELIVERED":
			return "delivered"
		case "OUT FOR DELIVERY":
			return "out_for_delivery"
		case "IN TRANSIT":
			return "in_transit"
		case "PICKED UP":
			return "picked_up"
		case "RTO INITIATED":
			return "rto_initiated"
		case "RTO DELIVERED":
			return "rto_delivered"
		case "LOST":
			return "lost"
		default:
			return "in_transit"
		}
	}
	var updates []TrackingUpdate
	for _, a := range wh.ShipmentTrackActivities {
		t, _ := time.Parse("2006-01-02 15:04:05", a.Date)
		updates = append(updates, TrackingUpdate{
			TrackingNumber: wh.AWB,
			Status:         mapStatus(a.Status),
			Location:       a.Location,
			Remark:         a.Activity,
			OccurredAt:     t,
		})
	}
	if len(updates) == 0 && wh.CurrentStatus != "" {
		updates = append(updates, TrackingUpdate{
			TrackingNumber: wh.AWB,
			Status:         mapStatus(wh.CurrentStatus),
			Remark:         wh.CurrentStatusBody,
			OccurredAt:     time.Now(),
		})
	}
	return updates, nil
}

// CheckServiceability queries Shiprocket's rate card and returns a real
// delivery price.
//
// B8 — what this replaces. The previous body was a "Phase 1.3 placeholder"
// that returned `Serviceable: true` and never set ShippingChargeMinor. It was
// not dormant: `COURIER_PROVIDER=shiprocket` is set in values-prod.yaml, and
// PrepareQuote persists `ShippingChargeMinor` straight into the quote that
// checkout consumes. Every production order would therefore have been created
// with ₹0 delivery, and the platform would have paid the real carrier charge
// on all of them. The handover listed this under "unverified dependencies";
// it is not unverified, it is a guaranteed loss on the first order.
//
// The two failure directions are deliberately NOT symmetric:
//
//   - not serviceable → a normal, non-error result the caller renders as
//     "we cannot deliver there";
//   - serviceable but unpriceable → an ERROR. A rate we could not obtain must
//     never become a zero we charge nothing for. PrepareQuote already refuses
//     to invent a rate when no courier is configured (D7/M-10); this closes
//     the same hole one layer down, where a configured courier answers
//     without a price.
func (c *ShiprocketCourier) CheckServiceability(ctx context.Context, req ServiceabilityRequest) (*ServiceabilityResult, error) {
	if !validIndianPincode(req.PickupPincode) || !validIndianPincode(req.DropPincode) {
		return &ServiceabilityResult{
			Serviceable: false,
			Courier:     "shiprocket",
			Reason:      "invalid pincode",
		}, nil
	}

	tok, err := c.authToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("shiprocket serviceability: %w", err)
	}

	// Shiprocket bills by weight in kilograms and wants at least a token
	// weight; a zero would be rejected by the rate card.
	weight := req.WeightKg
	if weight <= 0 {
		weight = 0.5
	}
	cod := "0"
	if strings.EqualFold(req.PaymentMethod, "cod") {
		cod = "1"
	}

	url := fmt.Sprintf("%s/courier/serviceability/?pickup_postcode=%s&delivery_postcode=%s&weight=%.2f&cod=%s",
		c.base, req.PickupPincode, req.DropPincode, weight, cod)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("shiprocket serviceability: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+tok)

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("shiprocket serviceability: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)

	// 404 is Shiprocket's "no courier serves this lane" answer, which is a
	// legitimate not-serviceable rather than a fault.
	if resp.StatusCode == http.StatusNotFound {
		return &ServiceabilityResult{
			Serviceable: false,
			Courier:     "shiprocket",
			Reason:      "no courier serves this route",
		}, nil
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("shiprocket serviceability %d: %s", resp.StatusCode, string(raw))
	}

	var out struct {
		Status int `json:"status"`
		Data   struct {
			AvailableCourierCompanies []struct {
				CourierName           string  `json:"courier_name"`
				CourierCompanyID      int     `json:"courier_company_id"`
				Rate                  float64 `json:"rate"`
				FreightCharge         float64 `json:"freight_charge"`
				CODCharges            float64 `json:"cod_charges"`
				EstimatedDeliveryDays string  `json:"etd"`
				EstimatedDays         string  `json:"estimated_delivery_days"`
				IsCODAvailable        int     `json:"cod"`
			} `json:"available_courier_companies"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("shiprocket serviceability: decode: %w", err)
	}
	if len(out.Data.AvailableCourierCompanies) == 0 {
		return &ServiceabilityResult{
			Serviceable: false,
			Courier:     "shiprocket",
			Reason:      "no courier serves this route",
		}, nil
	}

	// Cheapest priced option wins. An entry with a zero or negative rate is
	// skipped rather than selected: it is the "free shipping" that this
	// whole change exists to stop.
	best := -1
	for i, cc := range out.Data.AvailableCourierCompanies {
		rate := cc.Rate
		if rate <= 0 {
			rate = cc.FreightCharge
		}
		if rate <= 0 {
			continue
		}
		if best < 0 {
			best = i
			continue
		}
		prev := out.Data.AvailableCourierCompanies[best].Rate
		if prev <= 0 {
			prev = out.Data.AvailableCourierCompanies[best].FreightCharge
		}
		if rate < prev {
			best = i
		}
	}
	if best < 0 {
		// Serviceable, but every option came back without a usable price.
		// Refusing is the point: the alternative is charging the customer
		// nothing and absorbing the carrier bill.
		return nil, fmt.Errorf(
			"shiprocket returned %d courier options for %s→%s but none carried a usable rate; "+
				"refusing to quote a zero delivery charge",
			len(out.Data.AvailableCourierCompanies), req.PickupPincode, req.DropPincode)
	}

	chosen := out.Data.AvailableCourierCompanies[best]
	rate := chosen.Rate
	if rate <= 0 {
		rate = chosen.FreightCharge
	}
	// Rupees-major float from the provider → paise-minor int64, once, at the
	// boundary. Rounded rather than truncated so a 49.99 rate bills 4999.
	chargeMinor := int64(rate*100 + 0.5)

	days := parseShiprocketDays(chosen.EstimatedDays)
	result := &ServiceabilityResult{
		Serviceable:         true,
		CODSupported:        chosen.IsCODAvailable == 1,
		EstimatedDays:       days,
		Courier:             "shiprocket",
		ShippingChargeMinor: chargeMinor,
	}
	if days > 0 {
		result.EstimatedETA = time.Now().AddDate(0, 0, days)
	}
	return result, nil
}

// parseShiprocketDays reads the estimated-days field, which Shiprocket sends
// as a string and occasionally leaves blank. Zero means "unknown", which the
// caller renders as no ETA rather than as same-day.
func parseShiprocketDays(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// dimensionOrDefault substitutes a small-parcel side for a dimension a
// seller has not declared. Ten centimetres is Shiprocket's own minimum
// billable side, so it never inflates a volumetric weight beyond what the
// courier would charge anyway.
func dimensionOrDefault(cm float64) float64 {
	if cm > 0 {
		return cm
	}
	return defaultParcelSideCm
}

// defaultParcelSideCm is the side used when a product carries no dimensions.
const defaultParcelSideCm = 10.0

// DefaultPickupLocation is the nickname Shiprocket gives a seller's first
// registered pickup address. An account whose address is named anything
// else must say so through SHIPROCKET_PICKUP_LOCATION, or every booking is
// refused.
const DefaultPickupLocation = "Primary"

// WithPickupLocation names the registered pickup address to ship from.
// Empty keeps the default.
func (c *ShiprocketCourier) WithPickupLocation(name string) *ShiprocketCourier {
	if name != "" {
		c.pickupLocation = name
	}
	return c
}
