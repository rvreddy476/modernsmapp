// Package batchroute provides advisory stop ordering, never dispatch or navigation.
package batchroute

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	ErrUnavailable = errors.New("route suggestion is unavailable; keep the current stop order")
	ErrBusy        = errors.New("route suggestions are busy; try again shortly")
	ErrInvalid     = errors.New("route suggestion requires 2 or 3 valid stops")
)

type Point struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (p Point) valid() bool {
	return !math.IsNaN(p.Latitude) && !math.IsNaN(p.Longitude) && !math.IsInf(p.Latitude, 0) && !math.IsInf(p.Longitude, 0) && math.Abs(p.Latitude) <= 90 && math.Abs(p.Longitude) <= 180 && (p.Latitude != 0 || p.Longitude != 0)
}

type Stop struct {
	OrderID string
	Point   Point
}
type Input struct {
	Pickup Point
	Stops  []Stop
}
type Suggestion struct {
	OrderIDs []string `json:"order_ids"`
	Source   string   `json:"source"`
	Advisory bool     `json:"advisory"`
	Note     string   `json:"note"`
}
type cached struct {
	result  Suggestion
	expires time.Time
}
type Client struct {
	http     *http.Client
	endpoint string
	mu       sync.Mutex
	cache    map[[32]byte]cached
	lastCall time.Time
	busy     chan struct{}
}

// FromEnv leaves the feature off unless explicitly enabled. Only a server-side,
// read-only mounted service-account file is supported in this local rollout.
func FromEnv(ctx context.Context, getenv func(string) string) (*Client, error) {
	if getenv("FOOD_BATCH_ROUTE_PREVIEW_ENABLED") != "true" {
		return nil, nil
	}
	project := getenv("GOOGLE_CLOUD_PROJECT_NUMBER")
	if !regexp.MustCompile(`^[1-9][0-9]{5,19}$`).MatchString(project) {
		return nil, errors.New("GOOGLE_CLOUD_PROJECT_NUMBER is required for batch route previews")
	}
	file, err := os.Open(getenv("GOOGLE_APPLICATION_CREDENTIALS"))
	if err != nil {
		return nil, errors.New("batch route service-account file is unavailable")
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("batch route service-account file is invalid")
	}
	var kind struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &kind) != nil || kind.Type != "service_account" {
		return nil, errors.New("batch routes require service-account credentials")
	}
	conf, err := google.JWTConfigFromJSON(raw, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil || conf.TokenURL != google.JWTTokenURL {
		return nil, errors.New("batch route service-account configuration is invalid")
	}
	// Token refresh and API calls have independent network bounds. Never log SDK errors.
	authCtx := context.WithValue(ctx, oauth2.HTTPClient, &http.Client{Timeout: 10 * time.Second})
	client := conf.Client(authCtx)
	client.Timeout = 10 * time.Second
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return newClient(client, "https://routeoptimization.googleapis.com/v1/projects/"+project+":optimizeTours"), nil
}
func newClient(h *http.Client, endpoint string) *Client {
	return &Client{http: h, endpoint: endpoint, cache: make(map[[32]byte]cached), busy: make(chan struct{}, 1)}
}

// Preview sends coordinates and opaque indices only (no names, addresses, OTPs,
// customer/rider ids). Google uses DRIVING, not TWO_WHEELER: this is a suggested
// stop order only, never turn-by-turn directions or a customer ETA.
func (c *Client) Preview(ctx context.Context, input Input) (Suggestion, error) {
	if !input.Pickup.valid() || len(input.Stops) < 2 || len(input.Stops) > 3 {
		return Suggestion{}, ErrInvalid
	}
	seen := map[string]bool{}
	shipments := make([]any, 0, len(input.Stops))
	for _, stop := range input.Stops {
		if !stop.Point.valid() || stop.OrderID == "" || seen[stop.OrderID] {
			return Suggestion{}, ErrInvalid
		}
		seen[stop.OrderID] = true
		shipments = append(shipments, map[string]any{"deliveries": []any{map[string]any{"arrivalLocation": stop.Point}}})
	}
	keyData, _ := json.Marshal(input)
	key := sha256.Sum256(keyData)
	c.mu.Lock()
	if hit, ok := c.cache[key]; ok && time.Now().Before(hit.expires) {
		c.mu.Unlock()
		hit.result.OrderIDs = append([]string(nil), hit.result.OrderIDs...)
		return hit.result, nil
	}
	c.mu.Unlock()
	select {
	case c.busy <- struct{}{}:
		defer func() { <-c.busy }()
	default:
		return Suggestion{}, ErrBusy
	}
	c.mu.Lock()
	if time.Since(c.lastCall) < 10*time.Second {
		c.mu.Unlock()
		return Suggestion{}, ErrBusy
	}
	c.lastCall = time.Now()
	c.mu.Unlock()
	// costPerKilometer is a distance objective, not money or partner pay.
	body, _ := json.Marshal(map[string]any{"timeout": "2s", "model": map[string]any{"shipments": shipments, "vehicles": []any{map[string]any{"startLocation": input.Pickup, "costPerKilometer": 1}}}})
	requestCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return Suggestion{}, ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Suggestion{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Suggestion{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return Suggestion{}, ErrUnavailable
	}
	var out struct {
		Routes []struct {
			VehicleIndex int `json:"vehicleIndex"`
			Visits       []struct {
				ShipmentIndex int  `json:"shipmentIndex"`
				IsPickup      bool `json:"isPickup"`
			} `json:"visits"`
		} `json:"routes"`
		Skipped    []json.RawMessage `json:"skippedShipments"`
		Validation []json.RawMessage `json:"validationErrors"`
	}
	if json.Unmarshal(raw, &out) != nil || len(out.Routes) != 1 || out.Routes[0].VehicleIndex != 0 || len(out.Routes[0].Visits) != len(input.Stops) || len(out.Skipped) != 0 || len(out.Validation) != 0 {
		return Suggestion{}, ErrUnavailable
	}
	result := Suggestion{Source: "google_route_optimization", Advisory: true, Note: "Suggested drop-off order only. Your saved batch order is unchanged. Use two-wheeler navigation for each leg."}
	visited := map[int]bool{}
	for _, visit := range out.Routes[0].Visits {
		if visit.IsPickup || visit.ShipmentIndex < 0 || visit.ShipmentIndex >= len(input.Stops) || visited[visit.ShipmentIndex] {
			return Suggestion{}, ErrUnavailable
		}
		visited[visit.ShipmentIndex] = true
		result.OrderIDs = append(result.OrderIDs, input.Stops[visit.ShipmentIndex].OrderID)
	}
	c.mu.Lock()
	// Bounded, memory-only cache. It never persists private pins or changes assignments.
	if len(c.cache) >= 128 {
		clear(c.cache)
	}
	c.cache[key] = cached{result: result, expires: time.Now().Add(5 * time.Minute)}
	c.mu.Unlock()
	result.OrderIDs = append([]string(nil), result.OrderIDs...)
	return result, nil
}
