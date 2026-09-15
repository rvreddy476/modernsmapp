// Lane D7 service tests: deck cards and explain carry distance and last-active
// buckets only, explain answers only for the viewer's current deck and is rate
// limited, a trilateration attempt is rate limited and never sees better than
// a bucket, and new profiles hide last active and have Echoes off. Integration
// tests skip without TEST_PG_DSN (a *_test database); REDIS_ADDR (DB 15) adds
// the cached-deck path.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	d7KmFigureRe   = regexp.MustCompile(`\d\s*km`)
	d7TimestampRe  = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`)
	d7BucketLabels = []string{"Less than 5 km", "< 5 km", "5–10 km", "10–25 km", "25+ km"}
	d7BucketCodes  = map[string]bool{
		store.DistanceBucketUnder5: true, store.DistanceBucket5To10: true,
		store.DistanceBucket10To25: true, store.DistanceBucketOver25: true,
	}
)

// d7AssertBucketsOnly marshals v and fails on any distance key holding a
// non-string, any coordinate / geohash / last_active_at key, or any string
// naming a km figure other than a bucket label.
func d7AssertBucketsOnly(t *testing.T, what string, v any) {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("%s: marshal: %v", what, err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: unmarshal: %v", what, err)
	}
	var walk func(path string, n any)
	walk = func(path string, n any) {
		switch x := n.(type) {
		case map[string]any:
			for k, val := range x {
				lk, p := strings.ToLower(k), path+"."+k
				switch {
				case strings.Contains(lk, "distance"):
					if _, ok := val.(string); !ok {
						t.Errorf("%s: %s = %v is not a bucket string", what, p, val)
					}
				case lk == "latitude" || lk == "longitude" || lk == "lat" || lk == "lng" || lk == "lon" ||
					strings.Contains(lk, "geohash") || lk == "last_active_at":
					t.Errorf("%s: %s is present", what, p)
				}
				walk(p, val)
			}
		case []any:
			for _, e := range x {
				walk(path+"[]", e)
			}
		case string:
			s := x
			for _, label := range d7BucketLabels {
				s = strings.ReplaceAll(s, label, "")
			}
			if d7KmFigureRe.MatchString(s) {
				t.Errorf("%s: %s = %q names a km figure", what, path, x)
			}
		}
	}
	walk("$", doc)
}

func d7SetLocation(t *testing.T, st *store.Store, id uuid.UUID, lat, lng float64) {
	t.Helper()
	if _, err := st.UpsertProfile(context.Background(), id, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng}); err != nil {
		t.Fatalf("set location %v,%v: %v", lat, lng, err)
	}
}

func d7Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D7 service tests")
	}
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	if !strings.HasSuffix(cfg.ConnConfig.Database, "_test") {
		t.Fatalf("refusing to run against database %q: name must end in _test", cfg.ConnConfig.Database)
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func d7Card(resp *PulseResponse, id uuid.UUID) *PulseCard {
	for i := range resp.Data {
		if resp.Data[i].CandidateID == id {
			return &resp.Data[i]
		}
	}
	return nil
}

// d7Deck seeds an active viewer and candidates of a unique gender the viewer
// wants, so only they can be in the viewer's deck.
func d7Deck(t *testing.T, st *store.Store, candidates int) (uuid.UUID, []uuid.UUID, string) {
	t.Helper()
	ctx := context.Background()
	gender := "d7-" + uuid.NewString()[:8]
	viewer := uuid.New()
	seedActiveProfile(t, st, viewer)
	if _, err := st.UpsertPreferences(ctx, viewer, store.UpsertPreferencesParams{InterestedInGender: &gender}); err != nil {
		t.Fatalf("viewer preference: %v", err)
	}
	ids := make([]uuid.UUID, candidates)
	for i := range ids {
		ids[i] = uuid.New()
		seedActiveProfile(t, st, ids[i])
		if _, err := st.UpsertProfile(ctx, ids[i], store.UpsertProfileParams{Gender: &gender}); err != nil {
			t.Fatalf("candidate gender: %v", err)
		}
	}
	return viewer, ids, gender
}

func TestD7_DeckCardsCarryDistanceAndLastActiveBucketsOnly(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	viewer, ids, _ := d7Deck(t, st, 2)
	near, mid := ids[0], ids[1]
	d7SetLocation(t, st, viewer, 17.385, 78.4867) // snaps to 17.39, 78.49
	d7SetLocation(t, st, near, 17.41, 78.4867)    // 0.02 deg north: ~2.2 km
	d7SetLocation(t, st, mid, 17.50, 78.4867)     // 0.11 deg north: ~12.2 km

	deck, err := svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("deck: %v", err)
	}
	nearCard, midCard := d7Card(deck, near), d7Card(deck, mid)
	if nearCard == nil || midCard == nil {
		t.Fatalf("seeded candidates missing from the deck (%d cards)", len(deck.Data))
	}
	if nearCard.Profile.DistanceBucket != "lt_5_km" || nearCard.Profile.DistanceLabel != "< 5 km" {
		t.Fatalf("near card distance = %q / %q, want lt_5_km / < 5 km", nearCard.Profile.DistanceBucket, nearCard.Profile.DistanceLabel)
	}
	if midCard.Profile.DistanceBucket != "km_10_25" || midCard.Profile.DistanceLabel != "10–25 km" {
		t.Fatalf("mid card distance = %q / %q, want km_10_25 / 10–25 km", midCard.Profile.DistanceBucket, midCard.Profile.DistanceLabel)
	}
	d7AssertBucketsOnly(t, "deck", deck)

	// New profiles hide last active: no bucket at all.
	for _, card := range []*PulseCard{nearCard, midCard} {
		raw, _ := json.Marshal(card)
		if strings.Contains(string(raw), "last_active") {
			t.Fatalf("a default profile's card shows last active: %s", raw)
		}
	}

	// Shown, it is a coarse bucket, never a timestamp.
	show := false
	if _, err := st.UpdatePrivacy(ctx, mid, store.PrivacyUpdate{HideLastActive: &show}); err != nil {
		t.Fatalf("show last active: %v", err)
	}
	deck, err = svc.computePulseToday(ctx, viewer)
	if err != nil {
		t.Fatalf("deck: %v", err)
	}
	midCard = d7Card(deck, mid)
	if midCard == nil || midCard.Profile.LastActiveBucket != LastActiveToday || midCard.Profile.LastActiveLabel != "Active today" {
		t.Fatalf("shown last active = %+v, want today / Active today", midCard)
	}
	for _, card := range deck.Data {
		raw, _ := json.Marshal(card)
		if d7TimestampRe.Match(raw) {
			t.Fatalf("a card carries a timestamp: %s", raw)
		}
	}
	d7AssertBucketsOnly(t, "deck with last active", deck)
}

func TestLastActiveBucketFor(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		at   time.Time
		want string
	}{
		"just now":   {now.Add(-time.Minute), LastActiveToday},
		"23h ago":    {now.Add(-23 * time.Hour), LastActiveToday},
		"future":     {now.Add(time.Hour), LastActiveToday},
		"25h ago":    {now.Add(-25 * time.Hour), LastActiveThisWeek},
		"6 days ago": {now.Add(-6 * 24 * time.Hour), LastActiveThisWeek},
		"8 days ago": {now.Add(-8 * 24 * time.Hour), LastActiveAWhileAgo},
		"unknown":    {time.Time{}, LastActiveAWhileAgo},
	} {
		if got := LastActiveBucketFor(tc.at, now); got.Code != tc.want || got.Label == "" {
			t.Errorf("%s: %+v, want %s", name, got, tc.want)
		}
	}
}

func TestD7_ExplainOnlyForCandidatesInTheCurrentDeck(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	viewer, ids, _ := d7Deck(t, st, 1)
	inDeck := ids[0]
	outsider := uuid.New() // active and close by, but not the viewer's preference
	seedActiveProfile(t, st, outsider)
	d7SetLocation(t, st, viewer, 17.385, 78.4867)
	d7SetLocation(t, st, inDeck, 17.41, 78.4867)
	d7SetLocation(t, st, outsider, 17.41, 78.4867)

	out, err := svc.ExplainCandidate(ctx, viewer, inDeck)
	if err != nil {
		t.Fatalf("explain a deck candidate: %v", err)
	}
	if out.DistanceBucket != "lt_5_km" || out.DistanceLabel != "< 5 km" {
		t.Fatalf("explain distance = %q / %q, want lt_5_km / < 5 km", out.DistanceBucket, out.DistanceLabel)
	}
	d7AssertBucketsOnly(t, "explain", out)

	for name, target := range map[string]uuid.UUID{"not in the deck": outsider, "no such user": uuid.New()} {
		if _, err := svc.ExplainCandidate(ctx, viewer, target); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("explain %s: err=%v, want ErrCandidateUnavailable", name, err)
		}
	}
	// Passed, the candidate has left the deck.
	if _, err := svc.PassCandidate(ctx, viewer, inDeck, ""); err != nil {
		t.Fatalf("pass: %v", err)
	}
	if _, err := svc.ExplainCandidate(ctx, viewer, inDeck); !errors.Is(err, ErrCandidateUnavailable) {
		t.Fatalf("explain a passed candidate: err=%v, want ErrCandidateUnavailable", err)
	}
}

func TestD7_ExplainIsRateLimited(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	cfg := DefaultLocationPrivacyConfig()
	cfg.ExplainDailyLimit = 3
	svc.SetLocationPrivacyConfig(cfg)
	viewer := uuid.New()
	seedActiveProfile(t, st, viewer)
	// Refused requests count too, so probing is bounded.
	for i := 0; i < 3; i++ {
		if _, err := svc.ExplainCandidate(ctx, viewer, uuid.New()); !errors.Is(err, ErrCandidateUnavailable) {
			t.Fatalf("explain %d: err=%v, want ErrCandidateUnavailable", i+1, err)
		}
	}
	var limited *store.ExplainRateLimitError
	if _, err := svc.ExplainCandidate(ctx, viewer, uuid.New()); !errors.Is(err, ErrExplainRateLimited) || !errors.As(err, &limited) || limited.Limit != 3 {
		t.Fatalf("explain 4 of 3: err=%v, want *store.ExplainRateLimitError{3}", err)
	}
}

// An attacker walks their own location around a victim, reading the deck and
// explain after every move. The walk is rate limited (1 per 15 minutes, 10 a
// day) and every observation is one of the four bucket codes.
func TestD7_TrilaterationAttemptIsRateLimitedAndNeverFinerThanABucket(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	pool := d7Pool(t)
	ctx := context.Background()
	attacker, ids, _ := d7Deck(t, st, 1)
	victim := ids[0]
	d7SetLocation(t, st, victim, 12.90, 77.60)

	move := func(k int) error {
		lat, lng := 12.90+float64(k)*0.015, 77.60
		_, err := svc.UpsertProfile(ctx, attacker, store.UpsertProfileParams{Latitude: &lat, Longitude: &lng})
		return err
	}
	ageChanges := func() {
		t.Helper()
		if _, err := pool.Exec(ctx, `
            UPDATE dating_location_changes SET changed_at = changed_at - INTERVAL '16 minutes'
            WHERE user_id = $1`, attacker); err != nil {
			t.Fatalf("age location changes: %v", err)
		}
	}
	observe := func(k int) {
		t.Helper()
		deck, err := svc.GetPulseToday(ctx, attacker)
		if err != nil {
			t.Fatalf("move %d deck: %v", k, err)
		}
		card := d7Card(deck, victim)
		if card == nil {
			t.Fatalf("move %d: victim not in the attacker's deck", k)
		}
		d7AssertBucketsOnly(t, "attacker deck", deck)
		out, err := svc.ExplainCandidate(ctx, attacker, victim)
		if err != nil {
			t.Fatalf("move %d explain: %v", k, err)
		}
		d7AssertBucketsOnly(t, "attacker explain", out)
		if !d7BucketCodes[out.DistanceBucket] || out.DistanceBucket != card.Profile.DistanceBucket {
			t.Fatalf("move %d: explain %q / card %q; want one bucket code on both", k, out.DistanceBucket, card.Profile.DistanceBucket)
		}
	}

	if err := move(1); err != nil {
		t.Fatalf("move 1: %v", err)
	}
	observe(1)
	if err := move(2); !errors.Is(err, ErrLocationChangeRateLimited) {
		t.Fatalf("move 2 within 15 minutes: err=%v, want ErrLocationChangeRateLimited", err)
	}
	accepted := 1
	for k := 2; k <= 12; k++ {
		ageChanges()
		err := move(k)
		if accepted < 10 {
			if err != nil {
				t.Fatalf("move %d (change %d of 10): %v", k, accepted+1, err)
			}
			accepted++
			observe(k)
			continue
		}
		if !errors.Is(err, ErrLocationChangeRateLimited) {
			t.Fatalf("move %d (change %d in 24h): err=%v, want ErrLocationChangeRateLimited", k, accepted+1, err)
		}
	}
	if accepted != 10 {
		t.Fatalf("accepted %d moves in a day, want 10", accepted)
	}
}

func TestD7_NewProfileHidesLastActiveAndEchoesOff(t *testing.T) {
	svc, st, _ := newD3Svc(t)
	ctx := context.Background()
	id := uuid.New()
	intent := "casual"
	dob := time.Date(1996, 3, 1, 0, 0, 0, 0, time.UTC)
	if _, err := svc.UpsertProfile(ctx, id, store.UpsertProfileParams{Intent: &intent, BirthDate: &dob}); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	p, err := svc.GetPrivacy(ctx, id)
	if err != nil {
		t.Fatalf("privacy: %v", err)
	}
	if !p.HideLastActive || p.EchoesConsent || !p.ApproximateLocation {
		t.Fatalf("new profile privacy = %+v; want hide_last_active on, echoes_consent off, approximate_location on", p)
	}
	on := true
	if p, err = svc.UpdatePrivacy(ctx, id, store.PrivacyUpdate{EchoesConsent: &on}); err != nil || !p.EchoesConsent {
		t.Fatalf("Echoes opt-in = %+v, %v", p, err)
	}
	entries, err := st.ListConsentForUser(ctx, id)
	if err != nil {
		t.Fatalf("consent log: %v", err)
	}
	if len(entries) == 0 || entries[0].ConsentType != EchoesConsentType || !entries[0].Granted {
		t.Fatalf("consent log after the Echoes opt-in = %+v", entries)
	}
}
