package service

import (
	"strings"
	"testing"

	"github.com/atpost/rider-service/internal/store"
	"github.com/google/uuid"
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
		{"no Aadhaar is required with reviewed licence", "pending_verification", func(d map[string]store.DocumentState, _ *[]store.VehicleState) {
			delete(d, DocAadhaar)
		}, true, nil, nil},
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

// The selfie face-match decision (founder, 25 Oct: a match approves with
// no person; borderline or failed goes to a person). Mutation guards: the
// threshold comparison flipped or loosened, the face counts ignored, a
// missing result treated as a pass, the default threshold dropped.
func TestSelfieMatches(t *testing.T) {
	res := func(sim float64, src, dst int) *FaceCompareResult {
		return &FaceCompareResult{Similarity: sim, FaceCountSource: src, FaceCountTarget: dst, Provider: "test"}
	}
	cases := []struct {
		name string
		res  *FaceCompareResult
		min  float64
		want bool
	}{
		{"clear match approves", res(96, 1, 1), 80, true},
		{"exactly at the threshold approves", res(80, 1, 1), 80, true},
		{"just below the threshold goes to a person", res(79.9, 1, 1), 80, false},
		{"failed match goes to a person", res(12, 1, 1), 80, false},
		{"two faces in the selfie go to a person", res(96, 2, 1), 80, false},
		{"no face on the licence photo goes to a person", res(96, 1, 0), 80, false},
		{"no result goes to a person", nil, 80, false},
		{"unset threshold falls back to the default (borderline)", res(DefaultSelfieMinSimilarity-0.5, 1, 1), 0, false},
		{"unset threshold falls back to the default (match)", res(DefaultSelfieMinSimilarity, 1, 1), 0, true},
		{"a stricter configured threshold is honoured", res(85, 1, 1), 90, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := selfieMatches(tc.res, tc.min); got != tc.want {
				t.Fatalf("selfieMatches(%+v, %v) = %v want %v", tc.res, tc.min, got, tc.want)
			}
		})
	}
}

// The RC decision: only a pending vehicle whose latest RC DigiLocker
// returned (approved) is approved with no person. Mutation guards: a
// hand-uploaded RC (pending, or approved without the vehicle) approving the
// vehicle, a pending RC counting, a rejected vehicle re-approved.
func TestAutoApprovableVehicles(t *testing.T) {
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New(), uuid.New()}
	vehicles := []store.VehicleState{
		{ID: ids[0], Status: "pending", RCStatus: "approved", RCSource: store.DocSourceDigiLocker},  // DigiLocker RC: approve
		{ID: ids[1], Status: "pending", RCStatus: "pending", RCSource: store.DocSourceUpload},       // hand-uploaded: a person
		{ID: ids[2], Status: "pending", RCStatus: "approved", RCSource: store.DocSourceUpload},      // hand-uploaded RC approved alone: a person
		{ID: ids[3], Status: "pending", RCStatus: "pending", RCSource: store.DocSourceDigiLocker},   // not approved: no
		{ID: ids[4], Status: "rejected", RCStatus: "approved", RCSource: store.DocSourceDigiLocker}, // an admin's rejection stands
		{ID: ids[5], Status: "approved", RCStatus: "approved", RCSource: store.DocSourceDigiLocker}, // already approved: nothing to do
		{ID: uuid.New(), Status: "pending", RCStatus: "", RCSource: ""},                             // no RC at all
	}
	got := autoApprovableVehicles(vehicles)
	if len(got) != 1 || got[0] != ids[0] {
		t.Fatalf("auto approvable = %v, want only the DigiLocker RC vehicle %v", got, ids[0])
	}
	if got := autoApprovableVehicles([]store.VehicleState{vehicles[1], vehicles[2]}); len(got) != 0 {
		t.Fatalf("a hand-uploaded RC was approved automatically: %v", got)
	}
}
