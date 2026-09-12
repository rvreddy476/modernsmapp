package postgres

// The shipment wire shape.
//
// Shipment and ShipmentEvent carried db tags only, so encoding/json used the
// Go field names and GET /orders/:id/shipments was the one commerce payload
// in PascalCase ("TrackingNumber", "LabelURL"). The Android seller DTO had to
// be written against those names. These pin the snake_case keys, and pin
// that the old names are gone rather than merely joined.

import (
	"encoding/json"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

func wireKeys(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return got
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestShipmentGoesOutInSnakeCase(t *testing.T) {
	awb := "DL123456"
	now := time.Now()
	got := wireKeys(t, &Shipment{
		ID: uuid.New(), OrderID: uuid.New(), SellerID: uuid.New(),
		Courier: "Delhivery", TrackingNumber: &awb, Status: "booked",
		ShippedAt: &now, CreatedAt: now, UpdatedAt: now,
	})

	want := []string{
		"courier", "courier_order_id", "created_at", "delivered_at", "eta", "id",
		"label_url", "last_event_at", "order_id", "seller_id", "shipped_at",
		"status", "tracking_number", "tracking_url", "updated_at",
	}
	if k := sortedKeys(got); !equalStrings(k, want) {
		t.Fatalf("shipment keys = %v\nwant %v", k, want)
	}
	if got["tracking_number"] != awb || got["courier"] != "Delhivery" {
		t.Errorf("tracking_number/courier = %v/%v, want %q/Delhivery", got["tracking_number"], got["courier"], awb)
	}
	// Nullable columns are present as null, not absent: a client can tell
	// "no label yet" from "no such key".
	if v, present := got["label_url"]; !present || v != nil {
		t.Errorf("label_url = %v (present=%v); want an explicit null", v, present)
	}
	for _, old := range []string{"ID", "TrackingNumber", "Courier", "LabelURL"} {
		if _, present := got[old]; present {
			t.Errorf("PascalCase key %q is still on the wire", old)
		}
	}
}

func TestShipmentEventGoesOutInSnakeCase(t *testing.T) {
	remark := "recorded by seller: Delhivery DL123456"
	now := time.Now()
	got := wireKeys(t, &ShipmentEvent{
		ID: uuid.New(), ShipmentID: uuid.New(), Status: "booked",
		Remark: &remark, OccurredAt: now, CreatedAt: now,
	})
	want := []string{"created_at", "id", "location", "occurred_at", "remark", "shipment_id", "status"}
	if k := sortedKeys(got); !equalStrings(k, want) {
		t.Fatalf("shipment event keys = %v\nwant %v", k, want)
	}
	if got["remark"] != remark {
		t.Errorf("remark = %v, want %q", got["remark"], remark)
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
