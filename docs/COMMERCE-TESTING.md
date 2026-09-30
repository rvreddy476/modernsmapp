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

### The `_test` rule (enforced)

Every integration suite runs `internal/testdsn.Refuse` before it opens a pool,
and the process exits unless the database named in `COMMERCE_TEST_DSN` **ends in
`_test`** (`commerce_db`, `app` and `identity_db` are also refused by name, so
the message says what you nearly did). The old guard was a denylist of those
three names and admitted anything else; `order_transitions_db_test.go` and
`order_fulfilment_db_test.go` read their own `COMMERCE_TEST_POSTGRES_DSN` with no
guard and no build tag. All of them now use `COMMERCE_TEST_DSN`, the
`integration` tag and the same guard. CI's scratch database is
`commerce_p0_test` for the same reason.

Run one package per command, serially, so two suites never race on the same
rows:

```bash
for p in ./internal/store/postgres/ ./internal/service/ ./internal/http/ ./internal/piibackfill/; do
  go test -tags=integration -p 1 -count=1 "$p" || break
done
```

### Golden contract fixtures

`internal/http/testdata/contracts/` holds the response bodies the web and
Android clients copy byte for byte. `TestContractFixtures` creates a scratch
database `commerce_contract_<8 hex>_test` on the same server, bootstraps it,
seeds fixed ids and timestamps, renders every fixture through the production
route table and drops the database again, so it never touches the rows other
suites leave behind.

```bash
go test -tags=integration -p 1 -count=1 ./internal/http/ -run TestContractFixtures -v       # compare
go test -tags=integration -p 1 -count=1 ./internal/http/ -run TestContractFixtures -update  # rewrite
```

Never edit a fixture by hand; change the handler, rerun with `-update`, review
the diff. `TestContractInventoryMatchesTheFixtureList` (no database) fails when
a file has no fixture or a fixture has no file.

### The route inventory

`internal/http/testdata/routes.txt` is the registered (method, path) table.
`TestRouteInventoryMatchesTheCheckedInList` fails when a route is added or
removed until the list is regenerated on purpose:

```bash
go test ./internal/http/ -run TestRouteInventory -update-routes
```

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

## Seeding a demo catalogue

A fresh commerce database has categories (migration 023) and three imageless
banners (024) but no seller, no products and nothing for the home page's
rails, the category strip's counts or `discount_pct` to draw. `cmd/seed-demo`
in commerce-service fills that in idempotently: one approved seller, sixteen
products across eight categories with variants, stock and placeholder images,
half of them discounted, and three more banners. It refuses to run without
`--yes`, and refuses any database whose name neither contains `dev` nor ends
in `_test` unless that exact name is repeated in `--allow-db`; anything with
`prod` in the name is refused outright. Run it twice and the second run
inserts nothing.

```bash
cd Architecture/services/commerce-service
POSTGRES_DSN="postgres://postgres:postgres@127.0.0.1:5432/commerce_db?sslmode=disable" \
  go run ./cmd/seed-demo --yes --allow-db=commerce_db
```

Every row it writes has an id in the `00000000-0000-4000-8000-0000000de*`
block, so `DELETE FROM sellers WHERE id LIKE '00000000-0000-4000-8000-0000000de0%'`
(cascades to the products) plus the same on `commerce_banners` resets it.
