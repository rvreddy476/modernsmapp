package main

import (
	"context"
	"net/http/httptest"
	"testing"
)

func TestPartnerIntakeDoesNotOpenCustomerPilot(t *testing.T) {
	for _, tc := range []struct {
		name, path               string
		enabled, verified, allow bool
	}{
		{"default closed", "/v1/doorstep/pro/application", false, true, false},
		{"authenticated applicant", "/v1/doorstep/pro/application", true, true, true},
		{"anonymous", "/v1/doorstep/pro/application", true, false, false},
		{"customer", "/v1/doorstep/bookings", true, true, false},
		{"prefix spoof", "/v1/doorstep/proxy", true, true, false},
		{"admin", "/v1/doorstep/internal/admin/professionals", true, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			flag := "false"
			if tc.enabled {
				flag = "true"
			}
			products := productsFrom(t, map[string]string{"DOORSTEP_PARTNER_ENABLED": flag})
			r := httptest.NewRequest("GET", tc.path, nil)
			r.Header.Set("X-User-Id", outsiderUser)
			if tc.verified {
				r = r.WithContext(context.WithValue(r.Context(), verifiedUserIDKey{}, outsiderUser))
			}
			got := !serveDormantProductGate(httptest.NewRecorder(), r, products)
			if got != tc.allow {
				t.Fatalf("allow=%v want=%v", got, tc.allow)
			}
		})
	}
}
