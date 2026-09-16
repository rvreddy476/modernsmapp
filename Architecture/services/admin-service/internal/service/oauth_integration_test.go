//go:build integration

package service_test

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/atpost/admin-service/database"
	"github.com/atpost/admin-service/internal/secrethash"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// OAuth client secrets against a real Postgres.
//
//	POSTGRES_DSN=postgres://…/admin_it_test go test -tags integration ./internal/service/ -v

func openOAuthDB(t *testing.T) (*pgxpool.Pool, *service.Service) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN not set")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("POSTGRES_DSN does not parse")
	}
	if db := cfg.Database; db == "" || db == "app" || !strings.HasSuffix(db, "_test") {
		t.Fatalf("refusing to run integration tests against database %q: the name must end in _test (e.g. admin_it_test)", db)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := postgres.BootstrapSchema(ctx, pool, database.SetupSQL, database.Migrations); err != nil {
		t.Fatalf("bootstrap: %v", err)
	}
	// The Kafka writer is lazy; nothing here emits an event.
	return pool, service.New(postgres.New(pool), "127.0.0.1:1", nil)
}

func storedSecret(t *testing.T, pool *pgxpool.Pool, clientID string) string {
	t.Helper()
	var s string
	if err := pool.QueryRow(context.Background(),
		`SELECT client_secret_hash FROM oauth_clients WHERE client_id = $1`, clientID).Scan(&s); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestClientSecretIsStoredHashedAndVerifiedByHash(t *testing.T) {
	pool, svc := openOAuthDB(t)
	ctx := context.Background()
	clientID := "it-" + uuid.NewString()
	const secret = "it-client-secret-value"

	client := &postgres.OAuthClient{DeveloperID: uuid.New(), Name: "it", ClientID: clientID,
		RedirectURIs: []string{}, Scopes: []string{}}
	if err := svc.CreateOAuthClient(ctx, client, secret); err != nil {
		t.Fatal(err)
	}

	stored := storedSecret(t, pool, clientID)
	if stored == secret || strings.Contains(stored, secret) || !secrethash.IsHash(stored) {
		t.Fatal("client_secret_hash does not hold an argon2id hash")
	}
	if ok, err := svc.VerifyOAuthClientSecret(ctx, clientID, secret); err != nil || !ok {
		t.Fatalf("right secret: ok=%v err=%v", ok, err)
	}
	for _, wrong := range []string{"", "it-client-secret-valuE", stored} {
		if ok, err := svc.VerifyOAuthClientSecret(ctx, clientID, wrong); err != nil || ok {
			t.Fatalf("wrong secret verified: ok=%v err=%v", ok, err)
		}
	}
	if ok, err := svc.VerifyOAuthClientSecret(ctx, "no-such-"+uuid.NewString(), secret); err != nil || ok {
		t.Fatalf("unknown client verified: ok=%v err=%v", ok, err)
	}
}

func TestPlaintextSecretsAreHashedIdempotently(t *testing.T) {
	pool, svc := openOAuthDB(t)
	ctx := context.Background()
	clientID := "legacy-" + uuid.NewString()
	const secret = "legacy-plaintext-secret"

	if _, err := pool.Exec(ctx, `INSERT INTO oauth_clients (id, developer_id, name, client_id, client_secret_hash)
		VALUES ($1, $2, 'legacy', $3, $4)`, uuid.New(), uuid.New(), clientID, secret); err != nil {
		t.Fatal(err)
	}
	// A plaintext row never verifies, even with its own value.
	if ok, _ := svc.VerifyOAuthClientSecret(ctx, clientID, secret); ok {
		t.Fatal("a plaintext stored secret verified")
	}

	n, err := svc.HashPlaintextOAuthSecrets(ctx)
	if err != nil || n < 1 {
		t.Fatalf("first run: n=%d err=%v", n, err)
	}
	first := storedSecret(t, pool, clientID)
	if !secrethash.IsHash(first) {
		t.Fatal("legacy row not hashed")
	}
	if n, err := svc.HashPlaintextOAuthSecrets(ctx); err != nil || n != 0 {
		t.Fatalf("second run rewrote rows: n=%d err=%v", n, err)
	}
	if storedSecret(t, pool, clientID) != first {
		t.Fatal("second run changed an already hashed row")
	}
	if ok, err := svc.VerifyOAuthClientSecret(ctx, clientID, secret); err != nil || !ok {
		t.Fatalf("migrated secret does not verify: ok=%v err=%v", ok, err)
	}
}
