//go:build integration

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Hashtag and sound reports (T-13 / T-03) against the real schema
// (setup.sql + migrations through 014) on a *_test database:
//
//	M7_TRUST_POSTGRES_DSN=…/trust_safety_it_test go test -tags integration ./internal/service/ -run ReportEntities -v
func TestReportEntitiesHashtagAndSoundAgainstPostgres(t *testing.T) {
	pool := openM7TrustDB(t)
	ctx := context.Background()
	svc := New(postgres.New(pool), nil)
	alice, bob := uuid.New(), uuid.New()
	tag := "it" + uuid.NewString()[:8] // unique per run, still a valid tag
	sound := uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM trust.reports WHERE reporter_id = ANY($1)`, []uuid.UUID{alice, bob})
	})

	// A hashtag: stored by its normalised text, entity_id derived from it.
	r, err := svc.FileReportRef(ctx, alice, "hashtag", "#"+upper(tag), "spam", "tag abuse")
	if err != nil {
		t.Fatalf("hashtag report: %v", err)
	}
	if r.EntityKey != tag || r.EntityID != ReportEntityID("hashtag", tag) {
		t.Fatalf("stored (%s, %q), want (%s, %q)", r.EntityID, r.EntityKey, ReportEntityID("hashtag", tag), tag)
	}
	var storedKey *string
	if err := pool.QueryRow(ctx, `SELECT entity_key FROM trust.reports WHERE id = $1`, r.ID).Scan(&storedKey); err != nil || storedKey == nil || *storedKey != tag {
		t.Fatalf("entity_key column = %v (%v), want %q", storedKey, err, tag)
	}

	// The same tag spelled differently is the same entity: the one-active-
	// report rule applies exactly as it does to a post.
	if _, err := svc.FileReportRef(ctx, alice, "hashtag", tag, "harassment", ""); !errors.Is(err, ErrActiveReportExists) {
		t.Fatalf("second active report on the same tag: %v, want ErrActiveReportExists", err)
	}
	if _, err := svc.FileReportRef(ctx, bob, "hashtag", tag, "spam", ""); err != nil {
		t.Fatalf("another reporter on the same tag: %v", err)
	}

	// A sound: its audio id is the entity_id; no key.
	s, err := svc.FileReportRef(ctx, alice, "sound", sound.String(), "intellectual_property", "")
	if err != nil {
		t.Fatalf("sound report: %v", err)
	}
	if s.EntityID != sound || s.EntityKey != "" {
		t.Fatalf("sound stored (%s, %q)", s.EntityID, s.EntityKey)
	}
	if _, err := svc.FileReportRef(ctx, alice, "sound", sound.String(), "spam", ""); !errors.Is(err, ErrActiveReportExists) {
		t.Fatalf("second active report on the same sound: %v", err)
	}

	// UUID entities are untouched: no key, same duplicate rule.
	post := uuid.New()
	p, err := svc.FileReport(ctx, alice, post, "reel", "spam", "")
	if err != nil || p.EntityType != "post" || p.EntityKey != "" {
		t.Fatalf("post report: %+v %v", p, err)
	}

	// Reads carry the key: the reporter's own list, one report, the queue.
	own, err := svc.ListOwnReports(ctx, alice, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]string{}
	for _, rep := range own {
		keys[rep.EntityType] = rep.EntityKey
	}
	if keys["hashtag"] != tag || keys["sound"] != "" || keys["post"] != "" {
		t.Fatalf("own list keys = %v", keys)
	}
	got, err := svc.GetReport(ctx, r.ID.String())
	if err != nil || got.EntityKey != tag {
		t.Fatalf("GetReport: %+v %v", got, err)
	}
	if _, err := svc.ListReports(ctx, 500, 0); err != nil {
		t.Fatalf("admin queue with hashtag rows: %v", err)
	}

	// The CHECK keeps entity_type and entity_key in step.
	for _, q := range []string{
		`INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, reason, status) VALUES ($1, $2, 'hashtag', $3, 'spam', 'dismissed')`,
		`INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, entity_key, reason, status) VALUES ($1, $2, 'post', $3, 'x', 'spam', 'dismissed')`,
		`INSERT INTO trust.reports (id, reporter_id, entity_type, entity_id, entity_key, reason, status) VALUES ($1, $2, 'hashtag', $3, '#x', 'spam', 'dismissed')`,
	} {
		if _, err := pool.Exec(ctx, q, uuid.New(), alice, uuid.New()); err == nil {
			t.Fatalf("CHECK admitted: %s", q)
		}
	}
}

func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}
