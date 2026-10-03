package http

import (
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/google/uuid"
)

// Mutual picks (DATING_PICKS_MUTUAL_ENABLED): a pick must suit the picked
// person too, and nobody is everyone's pick. Same harness as m7_picks_test.go.

func mutualConfig(on bool, exposureCap int) service.MechanicsConfig {
	cfg := m7Config(true)
	cfg.PicksMutual = on
	cfg.PicksExposureCap = exposureCap
	return cfg
}

func picked(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// Guard: each of the picked person's own preferences can keep the viewer
// out. With the flag off the same person is picked, as before.
func TestMutualPicksFollowTheirPreferences(t *testing.T) {
	cases := []struct {
		name     string
		restrict func(d *m1Deck, id uuid.UUID)
	}{
		{"age range", func(d *m1Deck, id uuid.UUID) {
			d.exec(`UPDATE dating_preferences SET min_age = 90, max_age = 99 WHERE user_id = $1`, id)
		}},
		{"intents", func(d *m1Deck, id uuid.UUID) {
			// The viewer is casual.
			d.exec(`UPDATE dating_preferences SET intent_filter = '{marriage}' WHERE user_id = $1`, id)
		}},
		{"verified only", func(d *m1Deck, id uuid.UUID) {
			d.exec(`UPDATE dating_profiles SET verified_only_filter = true WHERE user_id = $1`, id)
			d.exec(`UPDATE dating_profiles SET trust_tier = 'phone' WHERE user_id = $1`, d.viewer)
		}},
		{"distance", func(d *m1Deck, id uuid.UUID) {
			// About 57 km apart: inside the viewer's 100 km, outside their 10.
			d.exec(`UPDATE dating_preferences SET distance_km = 100 WHERE user_id = $1`, d.viewer)
			// Past the 15-minute gap between location changes, as in
			// d7_location_privacy_it_test.go.
			d.exec(`UPDATE dating_location_changes SET changed_at = changed_at - INTERVAL '16 minutes' WHERE user_id = $1`, id)
			d.locate(id, 17.9)
			d.exec(`UPDATE dating_preferences SET distance_km = 10 WHERE user_id = $1`, id)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, on := range []bool{true, false} {
				d := newM1Deck(t, mutualConfig(on, 30))
				open, closed := d.candidate(), d.candidate()
				tc.restrict(d, closed)
				got := d.picks("UTC").ids()
				if !picked(got, open) {
					t.Fatalf("mutual=%v: the unrestricted person is not a pick: %v", on, got)
				}
				if picked(got, closed) == on {
					t.Fatalf("mutual=%v: restricted by %s, picked = %v, want %v", on, tc.name, on, !on)
				}
			}
		})
	}
}

// Guard: someone already in the cap's worth of picks today is not picked
// again; below the cap, or with the flag off, they are.
func TestMutualPicksSpreadAttention(t *testing.T) {
	today := time.Now().UTC().Format("2006-01-02")
	for _, on := range []bool{true, false} {
		d := newM1Deck(t, mutualConfig(on, 2))
		popular, other := d.candidate(), d.candidate()
		for i := 0; i < 2; i++ {
			d.exec(`INSERT INTO dating_daily_picks (user_id, pick_date, candidate_id, position)
                VALUES ($1, $2::date, $3, 1)`, uuid.New(), today, popular)
		}
		got := d.picks("UTC").ids()
		if !picked(got, other) {
			t.Fatalf("mutual=%v: the other person is not a pick: %v", on, got)
		}
		if picked(got, popular) == on {
			t.Fatalf("mutual=%v: a person at the cap, picked = %v, want %v", on, on, !on)
		}
	}
	// One below the cap is still picked.
	d := newM1Deck(t, mutualConfig(true, 2))
	nearly := d.candidate()
	d.exec(`INSERT INTO dating_daily_picks (user_id, pick_date, candidate_id, position)
        VALUES ($1, $2::date, $3, 1)`, uuid.New(), today, nearly)
	if got := d.picks("UTC").ids(); !picked(got, nearly) {
		t.Fatalf("a person below the cap is not picked: %v", got)
	}
}

// The flag and the cap come from the environment like every mechanic.
func TestMutualPicksBootConfig(t *testing.T) {
	env := map[string]string{"ENV": "production"}
	get := func(k string) string { return env[k] }
	cfg, err := ResolveMechanicsConfig(get)
	if err != nil || cfg.PicksMutual || cfg.PicksExposureCap != service.DefaultPicksExposureCap {
		t.Fatalf("production default = %v cap %d (err %v), want off and %d", cfg.PicksMutual, cfg.PicksExposureCap, err, service.DefaultPicksExposureCap)
	}
	env["DATING_PICKS_MUTUAL_ENABLED"], env["DATING_PICKS_EXPOSURE_CAP"] = "true", "12"
	if cfg, err = ResolveMechanicsConfig(get); err != nil || !cfg.PicksMutual || cfg.PicksExposureCap != 12 {
		t.Fatalf("explicit = %v cap %d (err %v)", cfg.PicksMutual, cfg.PicksExposureCap, err)
	}
	env["DATING_PICKS_EXPOSURE_CAP"] = "0"
	if _, err := ResolveMechanicsConfig(get); err == nil {
		t.Fatal("a cap of 0 was accepted")
	}
}
