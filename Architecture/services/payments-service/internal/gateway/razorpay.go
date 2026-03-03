package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

const razorpayBaseURL = "https://api.razorpay.com/v1"

// RazorpayGateway implements PaymentGateway using Razorpay's REST API.
type RazorpayGateway struct {
	keyID     string
	keySecret string
	client    *http.Client
}

// NewRazorpayGateway creates a new Razorpay gateway client.
func NewRazorpayGateway(keyID, keySecret string) *RazorpayGateway {
	return &RazorpayGateway{
		keyID:     keyID,
		keySecret: keySecret,
		client:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (g *RazorpayGateway) CreateOrder(ctx context.Context, amount int64, currency, receipt string) (GatewayOrder, error) {
	body, _ := json.Marshal(map[string]interface{}{
		"amount":   amount,
		"currency": currency,
		"receipt":  receipt,
	})
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, razorpayBaseURL+"/orders", bytes.NewReader(body))
	req.SetBasicAuth(g.keyID, g.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return GatewayOrder{}, err
	}
	defer resp.Body.Close()

	var result struct {
		ID       string `json:"id"`
		Amount   int64  `json:"amount"`
		Currency string `json:"currency"`
		Receipt  string `json:"receipt"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	if resp.StatusCode >= 400 {
		return GatewayOrder{}, fmt.Errorf("razorpay: create order failed with status %d", resp.StatusCode)
	}
	return GatewayOrder{ID: result.ID, Amount: result.Amount, Currency: result.Currency, Receipt: result.Receipt}, nil
}

func (g *RazorpayGateway) VerifySignature(orderID, paymentID, signature string) bool {
	payload := orderID + "|" + paymentID
	mac := hmac.New(sha256.New, []byte(g.keySecret))
	mac.Write([]byte(payload))
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (g *RazorpayGateway) InitiateRefund(ctx context.Context, paymentID string, amount int64) (GatewayRefund, error) {
	body, _ := json.Marshal(map[string]interface{}{"amount": amount})
	url := fmt.Sprintf("%s/payments/%s/refund", razorpayBaseURL, paymentID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	req.SetBasicAuth(g.keyID, g.keySecret)
	req.Header.Set("Content-Type", "application/json")

	resp, err := g.client.Do(req)
	if err != nil {
		return GatewayRefund{}, err
	}
	defer resp.Body.Close()

	var result struct {
		ID     string `json:"id"`
		Amount int64  `json:"amount"`
	}
	respBody, _ := io.ReadAll(resp.Body)
	json.Unmarshal(respBody, &result)
	if resp.StatusCode >= 400 {
		return GatewayRefund{}, fmt.Errorf("razorpay: refund failed with status %d: %s", resp.StatusCode, respBody)
	}
	return GatewayRefund{ID: result.ID, PaymentID: paymentID, Amount: result.Amount, Status: "processed"}, nil
}

func (g *RazorpayGateway) FetchPayment(ctx context.Context, paymentID string) (GatewayPayment, error) {
	url := fmt.Sprintf("%s/payments/%s", razorpayBaseURL, paymentID)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	req.SetBasicAuth(g.keyID, g.keySecret)

	resp, err := g.client.Do(req)
	if err != nil {
		return GatewayPayment{}, err
	}
	defer resp.Body.Close()

	var result struct {
		ID      string `json:"id"`
		OrderID string `json:"order_id"`
		Amount  int64  `json:"amount"`
		Status  string `json:"status"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	slog.Debug("razorpay: fetch payment", "id", result.ID, "status", result.Status)
	return GatewayPayment{ID: result.ID, OrderID: result.OrderID, Amount: result.Amount, Status: result.Status}, nil
}
