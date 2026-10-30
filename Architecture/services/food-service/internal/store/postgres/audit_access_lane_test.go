package postgres

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/google/uuid"
)

// Unit tests for the access-and-data lane of the 2026-10-30 food audit
// (M3 rider chat window, M6 rider identity lock, L5 idempotency binding).
// The database halves are in audit_access_lane_integration_test.go.

// M3: a rider is a chat party only inside the drop-off window.
func TestRiderChatParty(t *testing.T) {
	cases := []struct {
		name        string
		assignments []string
		order       string
		want        bool
	}{
		{"accepted, on its way", []string{"ACCEPTED"}, "DELIVERY_ASSIGNED", true},
		{"picked up", []string{"PICKED_UP"}, "PICKED_UP", true},
		{"at the door", []string{"ARRIVED_AT_CUSTOMER"}, "OUT_FOR_DELIVERY", true},
		{"merely assigned", []string{"ASSIGNED"}, "DELIVERY_ASSIGNED", false},
		{"delivered", []string{"DELIVERED"}, "DELIVERED", false},
		{"accepted row but order delivered", []string{"PICKED_UP"}, "DELIVERED", false},
		{"cancelled order", []string{"ACCEPTED"}, "CANCELLED_BY_ADMIN", false},
		{"released then another rider", []string{"CANCELLED"}, "PICKED_UP", false},
		{"never assigned", nil, "PICKED_UP", false},
		{"one old row, one live row", []string{"CANCELLED", "ACCEPTED"}, "DELIVERY_ASSIGNED", true},
	}
	for _, tc := range cases {
		if got := riderChatParty(tc.assignments, tc.order); got != tc.want {
			t.Errorf("%s: riderChatParty(%v, %s) = %v, want %v", tc.name, tc.assignments, tc.order, got, tc.want)
		}
	}
}

// M6: a reviewed rider cannot change name, phone or vehicle.
func TestCheckRiderIdentityChange(t *testing.T) {
	cur := riderIdentity{FullName: "R. Kumar", Phone: "+919000000001", VehicleType: "BIKE", VehicleNumber: "KA01AB1234"}
	for _, status := range []string{"APPROVED", "ACTIVE", "OFFLINE", "SUSPENDED"} {
		// Same identity, formatting aside: allowed (email/city edits).
		same := riderIdentity{FullName: " r.  kumar ", Phone: " +919000000001", VehicleType: "bike", VehicleNumber: "ka01 ab-1234"}
		if err := checkRiderIdentityChange(status, cur, same); err != nil {
			t.Fatalf("%s: unchanged identity refused: %v", status, err)
		}
		next := riderIdentity{FullName: "S. Rao", Phone: "+919000000009", VehicleType: "SCOOTER", VehicleNumber: "KA02CD5678"}
		err := checkRiderIdentityChange(status, cur, next)
		var locked *RiderIdentityLockedError
		if !errors.As(err, &locked) {
			t.Fatalf("%s: identity rewrite allowed (err=%v)", status, err)
		}
		if want := []string{"full_name", "phone", "vehicle_type", "vehicle_number"}; !reflect.DeepEqual(locked.Fields, want) {
			t.Fatalf("%s: fields = %v, want %v", status, locked.Fields, want)
		}
		for field, one := range map[string]riderIdentity{
			"full_name":      {FullName: "X", Phone: cur.Phone, VehicleType: cur.VehicleType, VehicleNumber: cur.VehicleNumber},
			"phone":          {FullName: cur.FullName, Phone: "+919999999999", VehicleType: cur.VehicleType, VehicleNumber: cur.VehicleNumber},
			"vehicle_type":   {FullName: cur.FullName, Phone: cur.Phone, VehicleType: "CAR", VehicleNumber: cur.VehicleNumber},
			"vehicle_number": {FullName: cur.FullName, Phone: cur.Phone, VehicleType: cur.VehicleType, VehicleNumber: "KA01AB9999"},
		} {
			err := checkRiderIdentityChange(status, cur, one)
			if !errors.As(err, &locked) || !reflect.DeepEqual(locked.Fields, []string{field}) {
				t.Fatalf("%s: changing only %s: err=%v", status, field, err)
			}
		}
	}
	// Before review every field is editable, as onboarding is.
	for _, status := range []string{"DRAFT", "PENDING_REVIEW", "REJECTED"} {
		if err := checkRiderIdentityChange(status, cur, riderIdentity{FullName: "S. Rao", Phone: "+919000000009"}); err != nil {
			t.Fatalf("%s: edit refused: %v", status, err)
		}
	}
}

// L5: a generic idempotency key is bound to the request and the caller.
func TestGenericIdempotencyBinding(t *testing.T) {
	user := uuid.New()
	bg := context.Background()
	if h := genericIdempotencyHash(bg, user); h != "" {
		t.Fatalf("no fingerprint should store no hash, got %q", h)
	}
	a := genericIdempotencyHash(WithRequestFingerprint(bg, "POST /v1/food/x body:1"), user)
	a2 := genericIdempotencyHash(WithRequestFingerprint(bg, "POST /v1/food/x body:1"), user)
	b := genericIdempotencyHash(WithRequestFingerprint(bg, "POST /v1/food/x body:2"), user)
	other := genericIdempotencyHash(WithRequestFingerprint(bg, "POST /v1/food/x body:1"), uuid.New())
	if a == "" || a != a2 {
		t.Fatalf("same request hashed differently: %q %q", a, a2)
	}
	if a == b || a == other {
		t.Fatal("different request or caller produced the same hash")
	}
	if len(a) > 128 {
		t.Fatalf("hash longer than request_hash VARCHAR(128): %d", len(a))
	}
	if idempotencyRequestMismatch(a, a2) {
		t.Fatal("same request judged a mismatch")
	}
	if !idempotencyRequestMismatch(a, b) {
		t.Fatal("different request not judged a mismatch")
	}
	if !idempotencyRequestMismatch(a, "") {
		t.Fatal("a fingerprinted key reused without a fingerprint was not judged a mismatch")
	}
	if idempotencyRequestMismatch("", b) {
		t.Fatal("a row stored before L5 (no hash) was judged")
	}
	if !errors.Is(errGenericIdempotencyMismatch, ErrIdempotencyPayloadMismatch) {
		t.Fatal("generic mismatch must answer as ErrIdempotencyPayloadMismatch (409)")
	}
}
