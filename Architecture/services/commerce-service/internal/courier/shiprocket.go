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
)

// ShiprocketCourier implements Provider over Shiprocket's REST API.
// Docs: https://apidocs.shiprocket.in/
type ShiprocketCourier struct {
	email        string
	password     string
	webhookToken string // shared-secret token Shiprocket sends as 'X-Api-Key'
	webhookHMAC  string // optional HMAC-SHA256 secret if using signed webhooks
	token        string
	tokenAt      time.Time
	mu           sync.Mutex
	base         string
	http         *http.Client
}

func NewShiprocket(email, password string) *ShiprocketCourier {
	return &ShiprocketCourier{
		email:    email,
		password: password,
		base:     "https://apiv2.shiprocket.in/v1/external",
		http:     &http.Client{Timeout: 20 * time.Second},
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
			"name":         it.Name,
			"sku":          it.SKU,
			"units":        it.Quantity,
			"selling_price": it.Price,
			"hsn":          it.HSN,
		})
	}
	payload := map[string]any{
		"order_id":                  req.OrderNumber,
		"order_date":                time.Now().Format("2006-01-02 15:04"),
		"pickup_location":           "Primary",
		"billing_customer_name":     req.DropAddress.Name,
		"billing_last_name":         "",
		"billing_address":           req.DropAddress.Line1,
		"billing_address_2":         req.DropAddress.Line2,
		"billing_city":              req.DropAddress.City,
		"billing_pincode":           req.DropAddress.Postal,
		"billing_state":             req.DropAddress.State,
		"billing_country":           req.DropAddress.Country,
		"billing_email":             req.DropAddress.Email,
		"billing_phone":             req.DropAddress.Phone,
		"shipping_is_billing":       true,
		"order_items":               items,
		"payment_method":            paymentMode(req.PaymentMethod),
		"sub_total":                 req.PackageValue,
		"weight":                    req.Weight,
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
		AWB                  string `json:"awb"`
		CurrentStatus        string `json:"current_status"`
		CurrentStatusBody    string `json:"current_status_body"`
		ShipmentStatus       string `json:"shipment_status"`
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

// CheckServiceability — Phase 1.3 placeholder. Shiprocket exposes
// /courier/serviceability/ with the pickup_postcode + delivery_postcode +
// weight + cod (0/1) query params; wiring it is straightforward but
// touches the auth-token cache and a non-trivial response shape. Until
// that lands the adapter returns the same permissive default as the stub
// so checkout flows still work in environments configured with
// COURIER_PROVIDER=shiprocket but no serviceability cache.
func (c *ShiprocketCourier) CheckServiceability(_ context.Context, req ServiceabilityRequest) (*ServiceabilityResult, error) {
	if !validIndianPincode(req.PickupPincode) || !validIndianPincode(req.DropPincode) {
		return &ServiceabilityResult{
			Serviceable: false,
			Courier:     "shiprocket",
			Reason:      "invalid pincode",
		}, nil
	}
	return &ServiceabilityResult{
		Serviceable:   true,
		CODSupported:  true,
		EstimatedDays: 4,
		EstimatedETA:  time.Now().AddDate(0, 0, 4),
		Courier:       "shiprocket",
	}, nil
}
