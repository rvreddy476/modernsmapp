package claimsync

// The reconcile pass over real live_streams rows (LIVE_V2_TEST_DSN on a
// *_test database, CI only). media-service is the claim-keeping fake.

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	dbschema "github.com/atpost/live-service-v2/database"
	"github.com/atpost/live-service-v2/internal/mediaclient"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

func itStore(t *testing.T) (*postgres.Store, *pgxpool.Pool) {
	t.Helper()
	dsn := os.Getenv("LIVE_V2_TEST_DSN")
	if dsn == "" {
		t.Skip("LIVE_V2_TEST_DSN not set")
	}
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal("LIVE_V2_TEST_DSN does not parse")
	}
	if name := strings.TrimPrefix(u.Path, "/"); !strings.HasSuffix(name, "_test") {
		t.Fatalf("refusing to run against database %q: the name must end in _test", name)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, dbschema.SetupSQL, dbschema.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	return postgres.New(pool), pool
}

func TestIntegrationBackfillClaimsCoversAndRepairsStaleClaims(t *testing.T) {
	st, _ := itStore(t)
	ctx := context.Background()
	creator := uuid.New()
	mk := func(cover *uuid.UUID) uuid.UUID {
		id := uuid.New()
		if _, err := st.CreateStream(ctx, postgres.CreateStreamParams{
			ID: id, CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.New().String(),
			Title: "it", Visibility: "public", CoverMediaID: cover,
		}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	a, gone := uuid.New(), uuid.New()
	withCover := mk(&a)
	dangling := mk(&gone)
	cleared := mk(nil)

	c := newFakeClaims()
	c.gone[gone] = true
	stale := uuid.New()
	c.held[key(mediaclient.KindLiveCover, cleared.String())] = map[uuid.UUID]bool{stale: true}

	// Dry run: nothing is sent.
	if _, err := Run(ctx, NewStoreSource(st, true), c, false); err != nil || len(c.calls) != 0 {
		t.Fatalf("dry run sent %v (err %v)", c.calls, err)
	}

	rep, err := Run(ctx, NewStoreSource(st, true), c, true)
	if err != nil {
		t.Fatal(err)
	}
	if !c.holds(mediaclient.KindLiveCover, withCover.String(), a) {
		t.Fatal("the backfill did not claim a stream's cover")
	}
	if len(c.held[key(mediaclient.KindLiveCover, cleared.String())]) != 0 {
		t.Fatal("-all did not release a stale claim of a cleared cover")
	}
	found := false
	for _, d := range rep.Dangling {
		if d.Ref == dangling.String() && d.MediaID == gone {
			found = true
		}
	}
	if !found {
		t.Fatalf("the stream naming a missing asset was not reported: %+v", rep.Dangling)
	}
}
