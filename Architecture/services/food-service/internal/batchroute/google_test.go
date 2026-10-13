package batchroute

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fixture() Input {
	return Input{Pickup: Point{17.37, 78.48}, Stops: []Stop{{"private-order-a", Point{17.36, 78.47}}, {"private-order-b", Point{17.38, 78.49}}}}
}
func TestPreviewOpaqueIndicesCacheAndPermutation(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "private-order") {
			t.Error("order identity sent to Google")
		}
		var model map[string]any
		if json.Unmarshal(body, &model) != nil || r.Method != "POST" {
			t.Error("invalid request")
		}
		io.WriteString(w, `{"routes":[{"visits":[{"shipmentIndex":1},{}]}]}`)
	}))
	defer server.Close()
	client := newClient(server.Client(), server.URL)
	first, err := client.Preview(context.Background(), fixture())
	if err != nil || len(first.OrderIDs) != 2 || first.OrderIDs[0] != "private-order-b" || !first.Advisory {
		t.Fatalf("result=%+v err=%v", first, err)
	}
	first.OrderIDs[0] = "changed"
	second, err := client.Preview(context.Background(), fixture())
	if err != nil || second.OrderIDs[0] != "private-order-b" || calls != 1 {
		t.Fatalf("cache changed or duplicate call: %+v %v calls=%d", second, err, calls)
	}
}
func TestRejectsIncompleteOrCorruptGoogleRoutes(t *testing.T) {
	for _, body := range []string{`{}`, `{"routes":[{"visits":[{},{}]}]}`, `{"routes":[{"visits":[{}, {"shipmentIndex":3}]}]}`, `{"routes":[{"visits":[{}, {"shipmentIndex":1,"isPickup":true}]}]}`, `{"routes":[{"visits":[{}, {"shipmentIndex":1}]}],"skippedShipments":[{}]}`, `{"routes":[{"visits":[{}, {"shipmentIndex":1}]}],"validationErrors":[{}]}`, `bad`, strings.Repeat("x", 65537)} {
		t.Run(body[:min(len(body), 70)], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, body) }))
			defer server.Close()
			if _, err := newClient(server.Client(), server.URL).Preview(context.Background(), fixture()); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("expected unavailable, got %v", err)
			}
		})
	}
}
func TestInvalidInputNeverCallsGoogle(t *testing.T) {
	client := newClient(&http.Client{}, "invalid-url")
	for _, input := range []Input{{}, {Pickup: Point{91, 78}, Stops: fixture().Stops}, {Pickup: fixture().Pickup, Stops: fixture().Stops[:1]}, {Pickup: fixture().Pickup, Stops: []Stop{fixture().Stops[0], fixture().Stops[0]}}} {
		if _, err := client.Preview(context.Background(), input); !errors.Is(err, ErrInvalid) {
			t.Fatalf("expected invalid, got %v", err)
		}
	}
}
func TestErrorsRedactedAndCallsBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, "token=secret-credentials private-order-a")
	}))
	defer server.Close()
	client := newClient(server.Client(), server.URL)
	_, err := client.Preview(context.Background(), fixture())
	if !errors.Is(err, ErrUnavailable) || strings.Contains(err.Error(), "secret") {
		t.Fatalf("unsafe error: %v", err)
	}
	if _, err = client.Preview(context.Background(), fixture()); !errors.Is(err, ErrBusy) {
		t.Fatalf("expected backoff, got %v", err)
	}
}
func TestDisabledAndInvalidConfiguration(t *testing.T) {
	client, err := FromEnv(context.Background(), func(string) string { return "" })
	if err != nil || client != nil {
		t.Fatal("disabled feature should not read credentials")
	}
	_, err = FromEnv(context.Background(), func(k string) string {
		if k == "FOOD_BATCH_ROUTE_PREVIEW_ENABLED" {
			return "true"
		}
		return "bad"
	})
	if err == nil {
		t.Fatal("invalid project number accepted")
	}
}
func TestGoogleLivePreview(t *testing.T) {
	if os.Getenv("FOOD_ROUTE_LIVE_TEST") != "1" {
		t.Skip("explicit live Google verification only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := FromEnv(ctx, os.Getenv)
	if err != nil || client == nil {
		t.Fatalf("live client unavailable: %v", err)
	}
	result, err := client.Preview(ctx, fixture())
	if err != nil || len(result.OrderIDs) != 2 || !result.Advisory {
		t.Fatalf("live preview failed: %v", err)
	}
}
