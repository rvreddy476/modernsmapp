package service

import (
	"encoding/json"
	"errors"
	"testing"
)

func boolPtr(b bool) *bool { return &b }

// TestParseDistributionPolicy_Validation covers the reject paths: unknown
// fields, wrong version, unsupported true flags, malformed JSON. All must
// fail loudly (Codex P0-1: "reject unsupported behavior, never silently
// ignore").
func TestParseDistributionPolicy_Validation(t *testing.T) {
	cases := []struct {
		name    string
		raw     string
		wantErr error // nil = expect success
	}{
		{"absent", "", nil},
		{"json null", "null", nil},
		{"minimal valid", `{"version":1}`, nil},
		{"full valid", `{"version":1,"main_feed":false,"notify_subscribers":true,"create_reel_preview":false}`, nil},
		{"missing version", `{"main_feed":true}`, ErrInvalidDistribution},
		{"wrong version", `{"version":2,"main_feed":true}`, ErrInvalidDistribution},
		{"unknown field", `{"version":1,"communities":["x"]}`, ErrInvalidDistribution},
		{"visibility not allowed in policy", `{"version":1,"visibility":"public"}`, ErrInvalidDistribution},
		{"reel preview unsupported", `{"version":1,"create_reel_preview":true}`, ErrUnsupportedDistribution},
		{"malformed", `{"version":1,`, ErrInvalidDistribution},
		{"trailing garbage", `{"version":1} {"x":2}`, ErrInvalidDistribution},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var raw json.RawMessage
			if tc.raw != "" {
				raw = json.RawMessage(tc.raw)
			}
			p, err := ParseDistributionPolicy(raw)
			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error %v, got policy %+v", tc.wantErr, p)
			}
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("expected %v, got %v", tc.wantErr, err)
			}
		})
	}
}

// TestResolveDistribution_Precedence is the new-policy vs legacy truth
// table (Codex P0-1 acceptance: "define and test exact new-policy versus
// legacy-field precedence").
//
// Contract: no policy → legacy behavior (main_feed=true, notify=true;
// the legacy posts.publish_to_feed column was write-only — feed-service
// never read it — so preserving live behavior means ignoring it).
// A policy is authoritative for the fields it sets; omitted fields take
// the same legacy defaults.
func TestResolveDistribution_Precedence(t *testing.T) {
	cases := []struct {
		name       string
		policy     *DistributionPolicy
		wantFeed   bool
		wantNotify bool
	}{
		{"nil policy → legacy: feed on, notify on", nil, true, true},
		{"empty policy → same legacy defaults", &DistributionPolicy{Version: 1}, true, true},
		{"main_feed=false wins over legacy", &DistributionPolicy{Version: 1, MainFeed: boolPtr(false)}, false, true},
		{"main_feed=true explicit", &DistributionPolicy{Version: 1, MainFeed: boolPtr(true)}, true, true},
		{"notify=false wins", &DistributionPolicy{Version: 1, NotifySubscribers: boolPtr(false)}, true, false},
		{"both off (PostTube-only, quiet)", &DistributionPolicy{Version: 1, MainFeed: boolPtr(false), NotifySubscribers: boolPtr(false)}, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ResolveDistribution(tc.policy)
			if got.MainFeed != tc.wantFeed || got.NotifySubscribers != tc.wantNotify {
				t.Fatalf("got main_feed=%v notify=%v, want main_feed=%v notify=%v",
					got.MainFeed, got.NotifySubscribers, tc.wantFeed, tc.wantNotify)
			}
		})
	}
}

// TestMarshalPolicy_RoundTrip pins storage round-trip stability: what we
// store re-parses to an identical policy (no field loss, no reordering
// surprises for the rev-guarded consumers).
func TestMarshalPolicy_RoundTrip(t *testing.T) {
	in := &DistributionPolicy{Version: 1, MainFeed: boolPtr(false), NotifySubscribers: boolPtr(true)}
	raw, err := MarshalPolicy(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out, err := ParseDistributionPolicy(raw)
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if out.Version != 1 || out.MainFeed == nil || *out.MainFeed || out.NotifySubscribers == nil || !*out.NotifySubscribers {
		t.Fatalf("round-trip mismatch: %+v", out)
	}
	// nil stays nil (SQL NULL, legacy row)
	if raw, err := MarshalPolicy(nil); err != nil || raw != nil {
		t.Fatalf("nil policy should marshal to nil, got %s err=%v", raw, err)
	}
}
