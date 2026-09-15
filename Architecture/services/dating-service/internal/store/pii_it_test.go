// Lane D9 store tests: religion, community, exact points and device signals
// are sealed at rest and read back opened; plaintext written before D9 is
// sealed by the backfill (idempotently) and still reads during the cutover;
// without keys the writes that must seal are refused. Integration tests need
// TEST_PG_DSN on a database whose name ends in _test.
package store

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/atpost/dating-service/database"
	"github.com/atpost/dating-service/internal/datingpii"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// testPII is the fixed test key ring shared by every D9 test package.
func testPII(t *testing.T) *datingpii.Crypto {
	t.Helper()
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	c, err := datingpii.New(context.Background(),
		[]datingpii.VersionedKey{{Version: 1, Key: mustDecode(t, key)}}, []byte("dating-it-test-lookup-salt-0001"))
	if err != nil {
		t.Fatalf("test pii: %v", err)
	}
	return c
}

func mustDecode(t *testing.T, s string) []byte {
	t.Helper()
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func newPIIStore(t *testing.T) (*Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set; skipping lane D9 store tests")
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
	if err := database.BootstrapSchema(context.Background(), pool); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	s := New(pool)
	s.SetPII(testPII(t))
	return s, pool
}

func d9Str(v string) *string { return &v }

func timeNowPlusHour() time.Time { return time.Now().Add(time.Hour) }

func queryBool(t *testing.T, pool *pgxpool.Pool, sql string, args ...any) bool {
	t.Helper()
	var b bool
	if err := pool.QueryRow(context.Background(), sql, args...).Scan(&b); err != nil {
		t.Fatalf("query %q: %v", sql, err)
	}
	return b
}

func TestD9_ProfileSensitiveFieldsSealedAtRest(t *testing.T) {
	s, pool := newPIIStore(t)
	ctx := context.Background()
	id := uuid.New()
	p, err := s.UpsertProfile(ctx, id, UpsertProfileParams{Religion: d9Str("Buddhist"), Community: d9Str("Kodava")})
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if p.Religion == nil || *p.Religion != "Buddhist" || p.Community == nil || *p.Community != "Kodava" {
		t.Fatalf("owner read = religion %v community %v, want both opened", p.Religion, p.Community)
	}
	if !queryBool(t, pool, `SELECT religion IS NULL AND community IS NULL
        AND religion_sealed IS NOT NULL AND community_sealed IS NOT NULL
        AND position('Buddhist'::bytea in religion_sealed) = 0
        FROM dating_profiles WHERE user_id = $1`, id) {
		t.Fatalf("religion/community are not sealed at rest (plaintext column written or blob holds plaintext)")
	}
	// A blank value clears the field.
	if p, err = s.UpsertProfile(ctx, id, UpsertProfileParams{Religion: d9Str("  ")}); err != nil || p.Religion != nil {
		t.Fatalf("clear religion = %v err=%v", p.Religion, err)
	}
	if !queryBool(t, pool, `SELECT religion IS NULL AND religion_sealed IS NULL FROM dating_profiles WHERE user_id = $1`, id) {
		t.Fatalf("blank religion left a value behind")
	}
}

func TestD9_WithoutKeysSensitiveWritesAreRefused(t *testing.T) {
	s, pool := newPIIStore(t)
	s.SetPII(nil)
	ctx := context.Background()
	id := uuid.New()
	if _, err := s.UpsertProfile(ctx, id, UpsertProfileParams{Religion: d9Str("Jain")}); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("religion without keys err = %v, want ErrPIINotConfigured", err)
	}
	if queryBool(t, pool, `SELECT EXISTS (SELECT 1 FROM dating_profiles WHERE user_id = $1)`, id) {
		t.Fatalf("a refused write still created the profile")
	}
	other := uuid.New()
	if _, err := s.CreateLocationShare(ctx, id, other, ShareRecipientMatch, 12.9, 77.6, 60e9); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("share without keys err = %v", err)
	}
	if _, err := s.ScheduleMeet(ctx, id, other, timeNowPlusHour(), 12.9, 77.6, "cafe"); !errors.Is(err, ErrPIINotConfigured) {
		t.Fatalf("meet without keys err = %v", err)
	}
	if err := s.UpsertDeviceFingerprint(ctx, id, "fp-nokeys-"+id.String(), "198.51.100.1"); err != nil {
		t.Fatalf("fingerprint without keys: %v", err)
	}
	if queryBool(t, pool, `SELECT EXISTS (SELECT 1 FROM dating_device_fingerprints WHERE user_id = $1)`, id) {
		t.Fatalf("a fingerprint was stored without sealing")
	}
	// A panic is still recorded (responders must be paged) but without its point.
	lat, lng := 12.971599, 77.594566
	out, err := s.RecordPanicIncident(ctx, RecordPanicParams{UserID: id, Source: PanicSourcePanic, Latitude: &lat, Longitude: &lng})
	if err != nil || out.Incident.HasLocation() {
		t.Fatalf("panic without keys = %+v err=%v, want recorded without a point", out, err)
	}
	if !queryBool(t, pool, `SELECT latitude IS NULL AND location_sealed IS NULL FROM dating_panic_incidents WHERE id = $1`, out.Incident.ID) {
		t.Fatalf("panic point stored in plaintext without keys")
	}
}

func TestD9_ExactPointsSealedAtRest(t *testing.T) {
	s, pool := newPIIStore(t)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	lat, lng := 12.971599, 77.594566

	out, err := s.RecordPanicIncident(ctx, RecordPanicParams{UserID: user, Source: PanicSourcePanic, Latitude: &lat, Longitude: &lng})
	if err != nil {
		t.Fatal(err)
	}
	inc, err := s.GetPanicIncident(ctx, out.Incident.ID)
	if err != nil || inc.Latitude == nil || *inc.Latitude != lat || *inc.Longitude != lng {
		t.Fatalf("panic read = %+v err=%v, want the exact point opened", inc, err)
	}
	if !queryBool(t, pool, `SELECT latitude IS NULL AND longitude IS NULL AND location_sealed IS NOT NULL
        FROM dating_panic_incidents WHERE id = $1`, inc.ID) {
		t.Fatalf("panic point stored in plaintext")
	}
	summaries, err := s.ListPanicIncidents(ctx, "", 200, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, sm := range summaries {
		if sm.ID == inc.ID && !sm.HasLocation {
			t.Fatalf("admin list lost has_location for a sealed point")
		}
	}

	share, err := s.CreateLocationShare(ctx, user, other, ShareRecipientMatch, lat, lng, 3600e9)
	if err != nil || share.Latitude == nil || *share.Latitude != lat {
		t.Fatalf("share = %+v err=%v", share, err)
	}
	got, err := s.GetLocationShareForRecipient(ctx, share.ShareID, other)
	if err != nil || got.Longitude == nil || *got.Longitude != lng {
		t.Fatalf("recipient read = %+v err=%v", got, err)
	}
	if !queryBool(t, pool, `SELECT latitude IS NULL AND location_sealed IS NOT NULL FROM dating_location_shares WHERE id = $1`, share.ShareID) {
		t.Fatalf("share point stored in plaintext")
	}
	if _, err := s.StopLocationShare(ctx, share.ShareID, user); err != nil {
		t.Fatal(err)
	}
	if !queryBool(t, pool, `SELECT location_sealed IS NULL FROM dating_location_shares WHERE id = $1`, share.ShareID) {
		t.Fatalf("stopping a share kept its sealed point")
	}

	meetID, err := s.ScheduleMeet(ctx, user, other, timeNowPlusHour(), lat, lng, "cafe")
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.GetMeet(ctx, meetID)
	if err != nil || m.Latitude == nil || *m.Latitude != lat {
		t.Fatalf("meet = %+v err=%v", m, err)
	}
	if !queryBool(t, pool, `SELECT latitude IS NULL AND location_sealed IS NOT NULL FROM dating_meets WHERE id = $1`, meetID) {
		t.Fatalf("meet point stored in plaintext")
	}
}

func TestD9_DeviceSignalsSealedAndStillCounted(t *testing.T) {
	s, pool := newPIIStore(t)
	ctx := context.Background()
	fp := "fp-d9-" + uuid.NewString()
	ip := "203.0.113." + uuid.NewString()[:2]
	users := []uuid.UUID{uuid.New(), uuid.New()}
	for _, u := range users {
		if err := s.UpsertDeviceFingerprint(ctx, u, fp, ip); err != nil {
			t.Fatal(err)
		}
		if err := s.UpsertDeviceFingerprint(ctx, u, fp, ip); err != nil { // refresh, no second row
			t.Fatal(err)
		}
	}
	if queryBool(t, pool, `SELECT EXISTS (SELECT 1 FROM dating_device_fingerprints
        WHERE fingerprint = $1 OR ip = $2 OR position(convert_to($1, 'UTF8') in fingerprint_sealed) > 0)`, fp, ip) {
		t.Fatalf("fingerprint or ip stored in plaintext")
	}
	if n, err := s.CountUsersByFingerprint(ctx, fp); err != nil || n != 2 {
		t.Fatalf("CountUsersByFingerprint = %d err=%v, want 2", n, err)
	}
	if n, err := s.CountDistinctUsersOnIPLastHour(ctx, ip); err != nil || n != 2 {
		t.Fatalf("CountDistinctUsersOnIPLastHour = %d err=%v, want 2", n, err)
	}
	list, err := s.ListFingerprintsForUser(ctx, users[0])
	if err != nil || len(list) != 1 || list[0].Fingerprint != fp || list[0].IP != ip {
		t.Fatalf("ListFingerprintsForUser = %+v err=%v, want one opened row", list, err)
	}
}

func TestD9_BackfillSealsLegacyPlaintextIdempotently(t *testing.T) {
	s, pool := newPIIStore(t)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	// Rows exactly as a pre-D9 writer left them.
	exec(`INSERT INTO dating_profiles (user_id, religion, community) VALUES ($1, 'Sikh', 'Jat')`, user)
	var incID, shareID, meetID, fpID uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO dating_panic_incidents (user_id, source, latitude, longitude)
        VALUES ($1, 'panic', 13.0827, 80.2707) RETURNING id`, user).Scan(&incID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO dating_location_shares (user_id, recipient_id, recipient_kind, latitude, longitude, expires_at)
        VALUES ($1, $2, 'match', 13.0827, 80.2707, now() + interval '1 hour') RETURNING id`, user, other).Scan(&shareID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO dating_meets (user_id, with_user_id, scheduled_at, latitude, longitude)
        VALUES ($1, $2, now() + interval '1 day', 13.0827, 80.2707) RETURNING id`, user, other).Scan(&meetID); err != nil {
		t.Fatal(err)
	}
	legacyFP := "legacy-fp-" + uuid.NewString()
	if err := pool.QueryRow(ctx, `INSERT INTO dating_device_fingerprints (user_id, fingerprint, ip)
        VALUES ($1, $2, '192.0.2.44') RETURNING id`, user, legacyFP).Scan(&fpID); err != nil {
		t.Fatal(err)
	}

	// During the cutover the plaintext still reads.
	if p, err := s.GetProfile(ctx, user); err != nil || p.Religion == nil || *p.Religion != "Sikh" {
		t.Fatalf("cutover read = %+v err=%v", p, err)
	}
	if n, err := s.CountUsersByFingerprint(ctx, legacyFP); err != nil || n != 1 {
		t.Fatalf("cutover count = %d err=%v", n, err)
	}

	res, err := s.BackfillSealedPII(ctx, 2) // tiny batches exercise the key walk
	if err != nil {
		t.Fatalf("backfill: %v", err)
	}
	if res.Profiles < 1 || res.PanicIncidents < 1 || res.LocationShares < 1 || res.Meets < 1 || res.DeviceFingerprints < 1 {
		t.Fatalf("backfill result = %+v, want every table touched", res)
	}
	checks := map[string]string{
		"profile":     `SELECT religion IS NULL AND community IS NULL AND religion_sealed IS NOT NULL AND community_sealed IS NOT NULL FROM dating_profiles WHERE user_id = $1`,
		"panic":       `SELECT latitude IS NULL AND location_sealed IS NOT NULL FROM dating_panic_incidents WHERE id = $1`,
		"share":       `SELECT latitude IS NULL AND location_sealed IS NOT NULL FROM dating_location_shares WHERE id = $1`,
		"meet":        `SELECT latitude IS NULL AND location_sealed IS NOT NULL FROM dating_meets WHERE id = $1`,
		"fingerprint": `SELECT fingerprint IS NULL AND ip IS NULL AND fingerprint_lookup IS NOT NULL AND ip_sealed IS NOT NULL FROM dating_device_fingerprints WHERE id = $1`,
	}
	ids := map[string]uuid.UUID{"profile": user, "panic": incID, "share": shareID, "meet": meetID, "fingerprint": fpID}
	for name, sql := range checks {
		if !queryBool(t, pool, sql, ids[name]) {
			t.Fatalf("backfill left %s plaintext", name)
		}
	}
	if p, err := s.GetProfile(ctx, user); err != nil || p.Community == nil || *p.Community != "Jat" {
		t.Fatalf("read after backfill = %+v err=%v", p, err)
	}
	if inc, err := s.GetPanicIncident(ctx, incID); err != nil || inc.Latitude == nil || *inc.Latitude != 13.0827 {
		t.Fatalf("panic after backfill = %+v err=%v", inc, err)
	}
	if n, err := s.CountUsersByFingerprint(ctx, legacyFP); err != nil || n != 1 {
		t.Fatalf("count after backfill = %d err=%v", n, err)
	}

	again, err := s.BackfillSealedPII(ctx, 50)
	if err != nil || again.Total() != 0 {
		t.Fatalf("second backfill = %+v err=%v, want nothing left to seal", again, err)
	}
}
