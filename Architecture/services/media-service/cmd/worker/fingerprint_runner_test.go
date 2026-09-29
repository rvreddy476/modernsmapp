package main

import (
	"testing"
	"time"
)

func testFingerprintConfig() fingerprintConfig {
	return fingerprintConfig{
		Enabled: true, IdleGrace: time.Minute, MaxQueuedWait: 60 * time.Second,
		BreakerTripAt: 120 * time.Second, BreakerHold: 15 * time.Minute,
	}
}

// Admission (plan 13): every gate refuses on its own, and the kill switch
// wins over everything (T13-1).
func TestFingerprintAdmissionGates(t *testing.T) {
	now := time.Now()
	r := &fingerprintRunner{}
	cfg := testFingerprintConfig()
	if got := r.admission(cfg, now, false, 0); got != "" {
		t.Fatalf("idle worker, empty queue: refused with %q", got)
	}
	if got := r.admission(cfg, now, true, 0); got != "worker_busy" {
		t.Fatalf("busy worker: %q", got)
	}
	if got := r.admission(cfg, now, false, 61); got != "transcode_waiting" {
		t.Fatalf("transcode waiting 61 s: %q", got)
	}
	if got := r.admission(cfg, now, false, 60); got != "" {
		t.Fatalf("transcode waiting exactly 60 s must pass: %q", got)
	}
	off := cfg
	off.Enabled = false
	if got := r.admission(off, now, false, 0); got != "disabled" {
		t.Fatalf("kill switch: %q", got)
	}
}

// T13-4: the breaker trips at > 120 s of queue wait and holds for 15 min,
// even once the queue has drained.
func TestFingerprintCircuitBreaker(t *testing.T) {
	now := time.Now()
	r := &fingerprintRunner{}
	cfg := testFingerprintConfig()
	if got := r.admission(cfg, now, false, 121); got != "breaker_tripped" {
		t.Fatalf("121 s: %q", got)
	}
	if got := r.admission(cfg, now.Add(14*time.Minute), false, 0); got != "breaker_open" {
		t.Fatalf("14 min later with an empty queue: %q", got)
	}
	if got := r.admission(cfg, now.Add(16*time.Minute), false, 0); got != "" {
		t.Fatalf("after the hold: %q", got)
	}
	// Exactly 120 s does not trip (it does refuse as transcode_waiting).
	r2 := &fingerprintRunner{}
	if got := r2.admission(cfg, now, false, 120); got != "transcode_waiting" {
		t.Fatalf("120 s: %q", got)
	}
}

func TestFingerprintConfigDefaultsOff(t *testing.T) {
	for _, k := range []string{"COPYRIGHT_FINGERPRINT_ENABLED", "COPYRIGHT_MATCHING_ENABLED", "COPYRIGHT_INDEX_LOOKUP_ENABLED", "COPYRIGHT_FINGERPRINT_BACKFILL_ENABLED"} {
		t.Setenv(k, "")
	}
	cfg := loadFingerprintConfig()
	if cfg.Enabled || cfg.MatchingEnabled || cfg.BackfillEnabled {
		t.Fatalf("flags default on: %+v", cfg)
	}
	if cfg.MaxCandidates != 50 || cfg.OriginalMaxBytes != 2048<<20 || cfg.OriginalMaxMs != 180*60_000 || cfg.Threads != 1 {
		t.Fatalf("defaults: %+v", cfg)
	}
	t.Setenv("COPYRIGHT_FINGERPRINT_ENABLED", "true")
	t.Setenv("COPYRIGHT_INDEX_LOOKUP_ENABLED", "on")
	cfg = loadFingerprintConfig()
	if !cfg.Enabled || !cfg.MatchingEnabled {
		t.Fatalf("flags not read: %+v", cfg)
	}
	t.Setenv("COPYRIGHT_FINGERPRINT_DEADLINE_FACTOR", "abc")
	if cfg := loadFingerprintConfig(); cfg.DeadlineFactor != 0.5 {
		t.Fatalf("bad factor not defaulted: %v", cfg.DeadlineFactor)
	}
}

func TestReadinessObservationsSkipMissingAnchors(t *testing.T) {
	// Nil anchors must not observe (a legacy asset with no confirm time);
	// present ones must not panic on a negative delta.
	observeQueueWait(nil, time.Now())
	observeReadyLatency(nil, time.Now(), "ready")
	future := time.Now().Add(time.Hour)
	observeQueueWait(&future, time.Now())
	observeReadyLatency(&future, time.Now(), "ready")
}
