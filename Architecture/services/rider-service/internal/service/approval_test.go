package service

import (
	"strings"
	"testing"

	"github.com/atpost/rider-service/internal/store"
)

func approvedDocs() map[string]store.DocumentState {
	return map[string]store.DocumentState{
		DocAadhaar:        {Type: DocAadhaar, Status: "approved", Source: store.DocSourceDigiLocker},
		DocDrivingLicence: {Type: DocDrivingLicence, Status: "approved", Source: store.DocSourceDigiLocker},
		DocSelfie:         {Type: DocSelfie, Status: "approved", Source: store.DocSourceUpload},
	}
}

func approvedVehicle() []store.VehicleState {
	return []store.VehicleState{{RegistrationNumber: "KA01ZZ0001", Status: "approved", RCStatus: "approved", RCSource: store.DocSourceDigiLocker}}
}

// The pure approval rule: every required document verified and one
// vehicle's RC verified approves; anything pending or missing does not.
func TestEvaluateApproval(t *testing.T) {
	cases := []struct {
		name    string
		status  string
		mutate  func(map[string]store.DocumentState, *[]store.VehicleState)
		approve bool
		pending []string
		missing []string
	}{
		{"all verified", "pending_verification", func(map[string]store.DocumentState, *[]store.VehicleState) {}, true, nil, nil},
		{"draft partner with everything verified", "draft", func(map[string]store.DocumentState, *[]store.VehicleState) {}, true, nil, nil},
		// Mutation guards.
		{"uploaded DL still pending: under review, not approved", "pending_verification", func(d map[string]store.DocumentState, _ *[]store.VehicleState) {
			d[DocDrivingLicence] = store.DocumentState{Type: DocDrivingLicence, Status: "pending", Source: store.DocSourceUpload}
		}, false, []string{DocDrivingLicence}, nil},
		{"selfie pending (below threshold)", "pending_verification", func(d map[string]store.DocumentState, _ *[]store.VehicleState) {
			d[DocSelfie] = store.DocumentState{Type: DocSelfie, Status: "pending", Source: store.DocSourceUpload}
		}, false, []string{DocSelfie}, nil},
		{"no aadhaar at all: incomplete", "pending_verification", func(d map[string]store.DocumentState, _ *[]store.VehicleState) {
			delete(d, DocAadhaar)
		}, false, nil, []string{DocAadhaar}},
		{"rejected selfie: incomplete", "pending_verification", func(d map[string]store.DocumentState, _ *[]store.VehicleState) {
			d[DocSelfie] = store.DocumentState{Type: DocSelfie, Status: "rejected", Source: store.DocSourceUpload}
		}, false, nil, []string{DocSelfie}},
		{"vehicle pending with uploaded RC pending", "pending_verification", func(_ map[string]store.DocumentState, v *[]store.VehicleState) {
			*v = []store.VehicleState{{Status: "pending", RCStatus: "pending", RCSource: store.DocSourceUpload}}
		}, false, []string{DocVehicleRC}, nil},
		{"vehicle without any RC", "pending_verification", func(_ map[string]store.DocumentState, v *[]store.VehicleState) {
			*v = []store.VehicleState{{Status: "pending", RCStatus: ""}}
		}, false, []string{DocVehicleRC}, nil},
		{"no vehicle: RC missing", "pending_verification", func(_ map[string]store.DocumentState, v *[]store.VehicleState) { *v = nil }, false, nil, []string{DocVehicleRC}},
		{"already approved partner is left alone", "approved", func(map[string]store.DocumentState, *[]store.VehicleState) {}, false, nil, nil},
		{"suspended partner never approved from here", "suspended", func(map[string]store.DocumentState, *[]store.VehicleState) {}, false, nil, nil},
		{"rejected partner never approved from here", "rejected", func(map[string]store.DocumentState, *[]store.VehicleState) {}, false, nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			docs, vehicles := approvedDocs(), approvedVehicle()
			tc.mutate(docs, &vehicles)
			v := evaluateApproval(ApprovalInput{PartnerStatus: tc.status, Documents: docs, Vehicles: vehicles})
			if v.Approve != tc.approve {
				t.Fatalf("approve = %v want %v (%+v)", v.Approve, tc.approve, v)
			}
			if strings.Join(v.Pending, ",") != strings.Join(tc.pending, ",") || strings.Join(v.Missing, ",") != strings.Join(tc.missing, ",") {
				t.Fatalf("pending=%v missing=%v want %v / %v", v.Pending, v.Missing, tc.pending, tc.missing)
			}
		})
	}
}

// The face comparer refuses to boot in production without media-service and
// refuses the mock there; the threshold is validated.
func TestFaceCompareFromEnv(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if _, _, err := FaceCompareFromEnv(env(map[string]string{}), true, "k"); err == nil || !strings.Contains(err.Error(), EnvMediaServiceURL) {
		t.Fatalf("production without MEDIA_SERVICE_URL must refuse: %v", err)
	}
	if _, _, err := FaceCompareFromEnv(env(map[string]string{EnvFaceCompareMode: "mock", EnvMediaServiceURL: "http://m"}), true, "k"); err == nil {
		t.Fatal("production must refuse the mock")
	}
	c, min, err := FaceCompareFromEnv(env(map[string]string{EnvFaceCompareMode: "mock"}), false, "k")
	if err != nil || min != DefaultSelfieMinSimilarity {
		t.Fatalf("dev mock: %v %v", err, min)
	}
	if _, ok := c.(*MockFaceComparer); !ok {
		t.Fatalf("dev mock comparer = %T", c)
	}
	c, min, err = FaceCompareFromEnv(env(map[string]string{EnvMediaServiceURL: "http://media", EnvSelfieMinSimilarity: "85"}), true, "k")
	if err != nil || min != 85 {
		t.Fatalf("production http: %v %v", err, min)
	}
	if _, ok := c.(*HTTPFaceComparer); !ok {
		t.Fatalf("production comparer = %T", c)
	}
	for _, bad := range []string{"0", "101", "abc"} {
		if _, _, err := FaceCompareFromEnv(env(map[string]string{EnvSelfieMinSimilarity: bad}), false, "k"); err == nil {
			t.Fatalf("threshold %q accepted", bad)
		}
	}
}
