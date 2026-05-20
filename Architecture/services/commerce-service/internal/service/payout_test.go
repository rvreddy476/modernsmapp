package service

import (
	"testing"
)

func TestCalculateSellerPayout_DefaultsMatchHistoricalConstants(t *testing.T) {
	// Phase 4.1 regression guard — service with no overrides must match the
	// previous hard-coded 5% / 2% / 1% so deployments that don't set env vars
	// see identical numbers.
	s := &Service{}
	gross := 1000.0
	net, comm, fee, tds := s.CalculateSellerPayout(gross, 0, 0)
	if comm != 50.0 {
		t.Errorf("commission = %v, want 50.0", comm)
	}
	if fee != 20.0 {
		t.Errorf("platform_fee = %v, want 20.0", fee)
	}
	// TDS = (1000 - 50 - 20) * 1% = 9.30
	if tds != 9.3 {
		t.Errorf("tds = %v, want 9.30", tds)
	}
	// Net = 1000 - 50 - 20 - 9.30 = 920.70
	if net != 920.70 {
		t.Errorf("net = %v, want 920.70", net)
	}
}

func TestCalculateSellerPayout_RespectsConfiguredRates(t *testing.T) {
	s := (&Service{}).WithPayoutConfig(PayoutConfig{
		CommissionPct:  10.0,
		PlatformFeePct: 3.0,
		TDSPct:         2.0,
	})
	net, comm, fee, tds := s.CalculateSellerPayout(1000.0, 0, 0)
	if comm != 100.0 {
		t.Errorf("commission = %v, want 100.0", comm)
	}
	if fee != 30.0 {
		t.Errorf("platform_fee = %v, want 30.0", fee)
	}
	// TDS = (1000 - 100 - 30) * 2% = 17.40
	if tds != 17.4 {
		t.Errorf("tds = %v, want 17.40", tds)
	}
	if net != 852.6 {
		t.Errorf("net = %v, want 852.60", net)
	}
}

func TestCalculateSellerPayout_PerCallOverrideWinsForRates(t *testing.T) {
	s := (&Service{}).WithPayoutConfig(PayoutConfig{
		CommissionPct: 5.0, PlatformFeePct: 2.0, TDSPct: 1.0,
	})
	_, comm, fee, _ := s.CalculateSellerPayout(1000.0, 8.0, 4.0)
	if comm != 80.0 {
		t.Errorf("per-call commission override ignored: got %v want 80.0", comm)
	}
	if fee != 40.0 {
		t.Errorf("per-call fee override ignored: got %v want 40.0", fee)
	}
}

func TestWithPayoutConfig_ClampsInvalidValues(t *testing.T) {
	// Negative commission, 0 platform fee, >100 TDS all get clamped to
	// fallback so a misconfigured env can't ship negative payouts.
	s := (&Service{}).WithPayoutConfig(PayoutConfig{
		CommissionPct: -5.0, PlatformFeePct: 0, TDSPct: 200.0,
	})
	cfg := s.payoutConfig()
	if cfg.CommissionPct != fallbackPayoutConfig.CommissionPct {
		t.Errorf("invalid commission not clamped: %v", cfg.CommissionPct)
	}
	if cfg.PlatformFeePct != fallbackPayoutConfig.PlatformFeePct {
		t.Errorf("invalid platform_fee not clamped: %v", cfg.PlatformFeePct)
	}
	if cfg.TDSPct != fallbackPayoutConfig.TDSPct {
		t.Errorf("invalid tds not clamped: %v", cfg.TDSPct)
	}
}
