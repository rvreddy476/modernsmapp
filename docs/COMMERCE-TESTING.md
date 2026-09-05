# Running the tests

```bash
go vet ./... && go test ./...
```

Unit tests need nothing but Go.

## Integration tests

```bash
COMMERCE_TEST_DSN="postgres://postgres:postgres@127.0.0.1:5432/commerce_it_test?sslmode=disable" \
  go test -tags=integration ./...
```

**Use `commerce_it_test`, never `commerce_db`.** That is the whole point of this
file.

`commerce_db` is the database the running stack serves the app and the web
storefront from. The integration fixtures seed products with
`status='active'` and `approval_status='approved'`, and almost none of them
tear down, so every run against `commerce_db` permanently adds live listings to
the real catalogue. On 2026-09-06 a day of suite runs left it with 4,355 live
products, of which roughly 4,000 were fixtures — "Test Product" ×1113, "Surface
Live Product" ×978 — crowding the storefront's grid and the phone's home page.
Nothing was broken; the shop simply filled up with things nobody is selling.

The fixtures are not going to grow teardown retrospectively: they insert
straight into `products` with raw SQL from a dozen files, and several of them
are asserting on what the catalogue looks like as a whole. Pointing them at a
throwaway database is the fix that actually holds.

### Keeping `commerce_it_test` current

Migrations run on service boot against whatever `DATABASE_URL` the container
has, which is `commerce_db`. `commerce_it_test` is not migrated by anything, so
after adding a migration, apply it there too:

```bash
cd database/migrations
docker exec -i atpost_stack-postgres-1 psql -U postgres -d commerce_it_test \
  -v ON_ERROR_STOP=1 -q < 0NN_your_migration.sql
docker exec atpost_stack-postgres-1 psql -U postgres -d commerce_it_test -c \
  "INSERT INTO schema_migrations (service, filename) \
   VALUES ('commerce-service','0NN_your_migration.sql') ON CONFLICT DO NOTHING;"
```

A suite run against a database missing a migration fails in ways that look like
code faults, so check `schema_migrations` there first when something fails only
in integration.

## Three failures that are not yours

```
TestC3DatabaseRefusesAnUnsupportedMethodDirectly
TestProofFencedSurfacesRefuseWrites
TestProofNegativeControl_FenceRemovedAllowsTheWrite
```

All three assert constraints that live in `database/gated/998_contract_triggers_and_fences.sql`,
which has never been applied to either development database — the third says so
outright, failing on `constraint "orders_payment_method_prepaid_only" ... does
not exist`. They fail identically on a clean checkout. Everything else must pass.
