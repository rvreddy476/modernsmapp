package runmode

import (
	"errors"
	"strings"
	"testing"
)

// Maintenance must win over writes. The runbook enables BOTH flags — writes
// so the admin routes accept a correction, maintenance so nothing else does —
// and the reviewer's correction B is precisely that no worker, producer or
// fraud counter starts in that state. An earlier test only covered
// maintenance with writes OFF, which is not the configuration the operator
// runs under and passed with the gate neutered.
func TestMaintenanceWinsOverWrites(t *testing.T) {
	m, err := Resolve(Config{Environment: "development", InternalKey: "k", WritesEnabled: true, Maintenance: true})
	if err != nil {
		t.Fatal(err)
	}
	if m.StartWorkers {
		t.Fatalf("maintenance with writes on still starts the workers: %+v", m)
	}
	if m.StartKafka || m.RequireRedis {
		t.Fatalf("maintenance with writes on still opens kafka/redis: %+v", m)
	}
	if !m.AdminKeyRequired {
		t.Fatalf("maintenance must require the admin key: %+v", m)
	}
	if !strings.Contains(m.BootLine(), "workers=false") {
		t.Fatalf("boot line must say workers=false: %s", m.BootLine())
	}
}

func TestWritesWithoutMaintenanceStartsWorkers(t *testing.T) {
	m, err := Resolve(Config{Environment: "development", InternalKey: "k", WritesEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if !m.StartWorkers || !m.StartKafka || !m.RequireRedis {
		t.Fatalf("writes on without maintenance must start everything: %+v", m)
	}
	if m.AdminKeyRequired {
		t.Fatalf("admin key is a maintenance-mode requirement only: %+v", m)
	}
}

// The memberships switch matrix (23 Oct 2026): MEMBERSHIPS_ENABLED opens the
// membership path (consumer, producer, membership workers) whatever the
// writes flag says, never starts the legacy money workers or Redis on its
// own, and maintenance wins over it.
func TestMembershipsSwitchMatrix(t *testing.T) {
	cases := []struct {
		name                                    string
		writes, memberships, maintenance        bool
		wantMemberships, wantWorkers, wantKafka bool
		wantMembershipWorkers, wantConsumer     bool
		wantRedis                               bool
	}{
		{"all off", false, false, false, false, false, false, false, false, false},
		{"memberships only", false, true, false, true, false, true, true, true, false},
		{"writes only", true, false, false, false, true, true, false, false, true},
		{"both", true, true, false, true, true, true, true, true, true},
		{"memberships in maintenance", false, true, true, false, false, false, false, false, false},
		{"both in maintenance", true, true, true, false, false, false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m, err := Resolve(Config{Environment: "development", InternalKey: "k",
				WritesEnabled: tc.writes, MembershipsEnabled: tc.memberships, Maintenance: tc.maintenance})
			if err != nil {
				t.Fatal(err)
			}
			got := []bool{m.MembershipsEnabled, m.StartWorkers, m.StartKafka, m.StartMembershipWorkers, m.StartPaymentsConsumer, m.RequireRedis}
			want := []bool{tc.wantMemberships, tc.wantWorkers, tc.wantKafka, tc.wantMembershipWorkers, tc.wantConsumer, tc.wantRedis}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("mode %+v: field %d = %v, want %v", m, i, got[i], want[i])
				}
			}
			if !strings.Contains(m.BootLine(), "memberships_enabled=") {
				t.Fatalf("boot line must state memberships: %s", m.BootLine())
			}
		})
	}
}

func TestContradictoryFlagsRefuseToBoot(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		want error
	}{
		{"payouts need writes", Config{InternalKey: "k", PayoutsEnabled: true}, ErrPayoutsNeedWrites},
		{"maintenance needs payouts off", Config{InternalKey: "k", WritesEnabled: true, PayoutsEnabled: true, Maintenance: true}, ErrMaintenanceNeedsPayoutsOff},
		{"maintenance needs the key", Config{WritesEnabled: true, Maintenance: true}, ErrMaintenanceNeedsKey},
		{"staging needs the key", Config{Environment: "staging"}, ErrKeyRequiredOutsideDev},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Resolve(tc.c)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}
