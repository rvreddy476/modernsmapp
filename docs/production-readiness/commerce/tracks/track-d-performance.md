# Track D: performance and scale (commerce: MStore / MSeller)

- **Date:** 17 Nov 2026
- **Trees read:**
  - `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `be57a46bf`. Between the map's `d83616ad6` and this commit only `media-service/cmd/worker/rungs*.go` changed; nothing on the commerce, payments or media delivery paths. commerce-service has no uncommitted edits, so its working tree is HEAD. For payments-service and api-gateway, whose files are dirty, every line cited was read with `git show HEAD:<path>` or from a file that is clean.
  - `C:\workspace\wt\pb-echo` (web, branch `feat/echo-web`, clean).
- **Method:** read-only, starting from `docs/production-readiness/commerce/00-map.md`. Every file:line below was read in this pass. Nothing was delegated.
- **Paths:** backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Shorthand as in the map: `C/` = `commerce-service/`, `H/` = `commerce-service/internal/http/`, `S/` = `commerce-service/internal/service/`, `P/` = `commerce-service/internal/store/postgres/`, `DB/` = `commerce-service/database/`, `PAY/` = `payments-service/`, `MS/` = `media-service/`. Web paths are relative to `C:\workspace\wt\pb-echo`. Android paths are relative to `mobile/android/`.
- **Executed (VERIFIED-RUN):**
  1. `go test -count=1 -run 'TestMoreThanTheBatchLimitIsChunkedNotTruncated|TestOneStolenIdFailsTheWholeBatch|TestAMediaIdResolvesToADisplayURL' ./internal/media/` in commerce-service: ok.
  2. `go test -count=1 -v -run 'TestEstimateCacheShiprocketOnly|TestClampListPagination|Home' ./internal/service/` in commerce-service: `TestEstimateCacheShiprocketOnly`, `TestClampListPagination` (8 subtests, including "caps at 200") and `TestTheWholeHomePageResolvesInOneMediaCall` passed.
  3. `git ls-files | grep -iE "k6|vegeta|locust|gatling|loadtest|load-test|artillery|ghz|bench"` in both trees, then a grep of every load-test file for `commerce` or `/v1/commerce`, and a search for `func Benchmark` in commerce-service, payments-service, `shared/outbox` and `shared/kafka`.
- **Not executed:** no EXPLAIN, no database, no Docker, no load, no device, no browser. No new measurement was taken. The one timing figure used below (2.08 ms per CloudFront RSA signature) is the long-video Track D measurement (its step 4), taken on this laptop. It is reused, not re-measured.
- **What the numbers mean:** every latency, throughput or count below is arithmetic from the code plus a stated assumption. **Nothing in this file shows that MStore or MSeller supports any particular number of shoppers, sellers, orders or products.**

## 1. Verdict for this track

**Commerce has never been load tested** (COM-D-22). The money path is built for correctness: one transaction, deterministic lock order, durable outboxes, idempotency. Reading it for scale shows four problems that would surface at launch traffic, not only at large scale:

1. **Every product picture is authorised on every view, through five services, and the last hop is a query that probably scans the whole catalogue.** Product images are stored as protected media. Each batch of image URLs makes media-service ask post-service, chat, profile and then commerce. Commerce answers with an OR-join that no index serves (COM-D-1). If any of the five is slow, the whole batch fails and the shop renders grey boxes.
2. **A flash sale serialises on one `products` row.** Quote and checkout both take `FOR UPDATE` on the product, and checkout holds that lock, the inventory row and the coupon row across about 17 database round trips for a one-line cart. There is no `lock_timeout` or `statement_timeout`, and the waiting checkouts hold connections from the same 25-connection pool that serves browsing (COM-D-2, COM-D-3).
3. **Every checkout needs a live Shiprocket call.** The quote calls the courier each time, with no cache, no breaker and a 20 s timeout. The code itself says Shiprocket rate-limits that API (COM-D-4).
4. **The storefront's first screens recompute the whole live catalogue per request.** Home, the category strip and the browse grid are uncached. Their indexes are partial on `products.status`, which stopped being the predicate when reads moved to `product_offers` (COM-D-5, COM-D-6, COM-D-7).

## 2. Cost model

### 2.1 Opening the shop (Android `StoreHomeViewModel.load`)

Android fires `GET /categories`, `GET /home` and `GET /products` together, plus `GET /cart` for the badge (`feature/commerce/.../home/StoreHomeViewModel.kt:140-160`).

| Request | Server cost | Evidence |
|---|---|---|
| `GET /categories` | One recursive CTE over the taxonomy, plus one `COUNT(*)` of live products per root category, each applying the live rule (`EXISTS` on sellers) | `P/storefront.go:506-537` |
| `GET /home` | Run in sequence: `LiveBanners` (an `EXISTS` per product banner), `DealProducts`, `BestSellerProducts` (falls back to `MostViewedProducts` when it is empty), `NewArrivalProducts`. Each of the three rails selects the shared summary projection: two `LATERAL` subqueries per product (cheapest variant; stock summed over variants × inventory) and a correlated cover-image subquery, over every live product, then sorts. Best sellers adds a `LATERAL` sum over `order_items` × `orders` per product. Then one media batch (5-authority chain, section 2.3), one favourites query and one coupon query | `S/storefront.go:422-523`, `P/storefront.go:563-659,690-701`, `P/storefront.go:88-137` |
| `GET /products` (first page, cursor mode) | The same summary projection over the live catalogue, `ORDER BY p.created_at DESC, p.id DESC LIMIT 21`, plus one media batch, favourites and coupons | `P/products.go:371-469`, `S/catalogue_products.go:377-381` |
| `GET /cart` | One query | `P/cartview.go:93-122` |

None of these is cached on the server. The web caches `/home` for 60 s and `/categories` for 5 min **per browser tab** (`src/features/shop/hooks/storefront.ts:37-51`).

### 2.2 Viewing one product (`GET /products/:id`)

About nine sequential Postgres statements:

1. `productReadable`
2. `GetProductByID`, with three correlated subqueries
3. variants
4. favourite
5. reaction
6. gallery, plus a media batch
7. typed attributes
8. try-on
9. a fire-and-forget Redis view increment

Evidence: `H/handler_catalogue.go:71-139`, `S/catalogue_products.go:303-311`, `P/products.go:64-118`.

The web product page also fetches the same route server-side through the gateway for link-preview metadata, cached 60 s per id (`src/app/shop/products/[id]/page.tsx:22-36`).

### 2.3 Resolving product images (every grid, cart, order and detail page)

| Hop | Cost | Evidence |
|---|---|---|
| commerce → media-service `POST /v1/media/batch` | At most 50 ids per call, chunks sent sequentially; 3 s timeout; Go default transport; fail-soft to placeholders | `C/internal/media/media.go:111-129,343-437` |
| media-service gate | Every product image is a protected key (nothing is written under `public/`), so all ids go to the content authorities, asked **in order**: post-service, chat, profile, commerce, group. Each is a separate HTTP batch call with a 3 s timeout. An id that any authority leaves unresolved fails the **whole** batch | `MS/cmd/server/main.go:537-543`, `MS/internal/delivery/authz.go:171-185,437-492,690-730`, `MS/internal/delivery/signer.go:63,80-85` |
| commerce `POST /internal/media-access/batch` | `VisibleProductMediaIDs`: `JOIN products p ON (p.primary_image_media_id = a.media_id OR EXISTS(product_media …) OR EXISTS(product_variants …))`, with no index on any of the three media columns | `H/handler_storefront.go:340-362`, `P/storefront.go:767-807` (COM-D-1) |
| Signing | One RSA signature per key. An image carries `original` plus its renditions (`thumb_150`, `small_480`, `medium_1080`), so about 4 signatures per image. Signatures are reused for 2 min; URLs live 5 min | `MS/internal/service/media_batch.go:76-90,199-204`, `MS/internal/delivery/sign_cache.go:55-60`, `signer.go:72` |

Arithmetic: a 24-card web page is about 96 signatures on a cold sign cache. At the measured 2.08 ms each, that is about 200 ms of one media-service core, repeated for each new set of products every 2 minutes.

### 2.4 Buying one item (one-line cart)

| Step | Requests | Server cost |
|---|---|---|
| Quote | `POST /checkout/quote` | Cart meta, decrypt address, **one Shiprocket serviceability call** (no cache), then a transaction that locks the cart lines and `FOR UPDATE OF v, p` on the variant and product, then an `INSERT` into `shipping_quotes` (`S/checkout_p0.go:101-190`, `P/quote.go:301-371`) |
| Checkout | `POST /v2/orders/checkout` | One transaction (`P/checkout.go:207-677`). After the product lock is taken (step 5), there are about 6N + 11 more statements before commit (N = cart lines). For N = 1 that is about 17 round trips holding the `products`, `product_variants`, `inventory_items` and (with a coupon) `coupons` rows |
| Intent | `POST /orders/:id/payment/intent` | 3 to 4 reads, then commerce → payments (15 s timeout) → Razorpay `CreateOrder` (30 s timeout) (`S/checkout_p0.go:428-473`) |
| Confirm | polls of `GET /orders/:id/payment/status` | Android waits 1 s, then +1 s per read up to 5 s, for up to 180 s (`core/payments/.../PaymentCoordinator.kt:108-140`). The web order page refetches every 5 s (`src/features/shop/hooks/orders.ts:28-44`). Each poll is 2 commerce reads plus a payments-service `GetIntent` call (`S/checkout_p0.go:487-506`) |
| Webhook → order | 1 webhook | payments: DB only, no outbound call (`PAY/internal/http/handler.go:1150-1220` at HEAD, `PAY/internal/service/p0.go:110`). Then outbox → `social.events.v1` → commerce consumer: one sequential handler per pod, `FOR UPDATE` on the order, then an update to the inventory row to commit the reservation (`P/p0.go:551-610`) |
| Fulfilment | 1 job | Invoice (sequence upsert, 1 to 2 S3 uploads), then Shiprocket create plus AWB assignment (2 calls, 20 s timeout each), inside a 30 s budget, **one job at a time per pod** (`S/jobs.go:43-57`, `C/internal/workers/fulfillment.go`) |

## 3. Findings

### COM-D-1

- **ID:** COM-D-1
- **Title:** Every product image is authorised through a chain of five services on every resolution. The last hop, commerce's `VisibleProductMediaIDs`, is an OR-join that no index can serve. And one slow authority fails the whole batch, so the shop renders placeholders.
- **Severity:** P1
- **Category:** performance / resilience
- **Evidence level:** VERIFIED-READ (query, indexes, chain order, batch failure rule); SUSPECTED (the plan is a sequential scan of `products` per asked id; no EXPLAIN was run)
- **Evidence:**
  - `P/storefront.go:777-794`: `FROM asked a JOIN products p ON (p.primary_image_media_id = a.media_id OR EXISTS (SELECT 1 FROM product_media pm WHERE pm.product_id = p.id AND pm.media_id = a.media_id) OR EXISTS (… product_variants pv … pv.image_media_id = a.media_id))`. The join condition is a disjunction containing correlated subqueries, so neither a hash join nor an index lookup on `p` is possible.
  - Indexes, from a full grep of `DB/setup.sql` and `DB/migrations/*.sql`: `product_media` has only `(product_id, sort_order)` (`setup.sql:242`); `product_variants` has `(product_id)`, `(sku)` and `(offer_id)` (`setup.sql:230-231`, `027:125`). There is **no index on `product_media.media_id`, `products.primary_image_media_id` or `product_variants.image_media_id`.**
  - The chain: `MS/cmd/server/main.go:537-543` builds `AnyContentAuthorizer{post, chat, profile, commerce, group}`. `authz.go:437-492` asks each in turn with the ids still unresolved, so a product id costs a post-service, a chat and a profile batch before commerce sees it. Any unresolved id at the end returns `ErrDeliveryUnresolved` for the whole batch (`:486-492`). Each authorizer has a 3 s client timeout (`:171-177`).
  - Product images are never public. `ClassForKey` treats everything outside `public/` as protected (`signer.go:63,80-85`), and no code writes under `public/` (grep).
  - On the commerce side, `ResolveURLs` swallows every failure and returns no URL (`C/internal/media/media.go:345-350,405-416`), which clients draw as a placeholder.
  - The cart (`P/cartview.go:100-103`), the order list (`P/orders.go:242-246`) and order items resolve through the same path.
- **How to reproduce:** Seed `commerce_it_test` with 100,000 products (each with 1 to 8 `product_media` rows and 1 to 5 variants). Run `EXPLAIN (ANALYZE, BUFFERS)` of the `VisibleProductMediaIDs` statement with 50 ids. Expect a nested loop over a sequential scan of `products` with SubPlans executed per row. Then time `POST /v1/media/batch` with 50 product ids through media-service.
- **Impact:** Arithmetic: 50 ids × 100,000 products × up to 2 subplan probes is up to about 10 million index probes per batch. That is far beyond the 3 s authorizer timeout. At launch catalogue sizes (a few thousand products) it is slower than it needs to be, not yet broken. Past roughly 10,000 products it would likely exceed 3 s. Then every grid, cart and order screen loses its images at once, and the media pods hold three upstream calls per batch while waiting. All of this needs EXPLAIN to confirm.
- **Fix (smallest first):**
  - (a) Rewrite as a `UNION` of three indexed lookups: `products WHERE primary_image_media_id = ANY($1)`, `product_media WHERE media_id = ANY($1)` and `product_variants WHERE image_media_id = ANY($1)`. Join each to `products`/`product_offers` for the live rule. Add `CREATE INDEX CONCURRENTLY` on `product_media(media_id)`, `products(primary_image_media_id) WHERE primary_image_media_id IS NOT NULL` and `product_variants(image_media_id) WHERE image_media_id IS NOT NULL`.
  - (b) Let media-service route an asset to its owning authority, for example from an `owner_kind` / claim on the asset (media claims exist), instead of trying five in turn.
  - (c) Later, as a product decision: give live product images a public delivery class (a viewer-independent, cacheable URL), since the rule already says they are visible to anyone, signed in or not (`P/storefront.go:750-757`).
- **Acceptance:**
  - On the 100k-product fixture, EXPLAIN shows index scans only, and the statement for 50 ids runs in under 20 ms.
  - A media-service test with a counting fake for each authority: a batch of product-only ids makes at most 2 authority calls (fails now: 4).
- **Effort:** S for (a), M for (b), M for (c)

### COM-D-2

- **ID:** COM-D-2
- **Title:** A flash sale on one product serialises every quote and every checkout on that product's `products` row. Checkout holds that row, the variant, the inventory row and the coupon row across about 6N + 11 statements. Payment captures and expiry releases then queue on the same inventory row.
- **Severity:** P1
- **Category:** performance / scale (lock contention)
- **Evidence level:** VERIFIED-READ (locks and statement order); SUSPECTED (the ceiling, which is arithmetic)
- **Evidence:**
  - `P/checkout.go:859-913`: `priceLinesTx(..., lock=true)` adds `FOR UPDATE OF v, p` (`:861-864`). The lock is on the **product** row, so every variant (size, colour) of the product shares it.
  - Quote takes the same lock: `P/quote.go:301-318` calls `lockAndPriceLines`. The file says the transaction "exists only to take the same row locks checkout takes" (`:292-294`).
  - Checkout order after the product lock (`P/checkout.go:422-667`):
    1. `lockInventory`, `FOR UPDATE` per variant (`:1006-1039`)
    2. `claimCoupon`, `UPDATE coupons SET uses_count = uses_count + 1` (`P/coupons.go:424-430`), plus a per-user usage count (`:305-306`)
    3. seller state
    4. `generate_order_number()`
    5. `INSERT orders`
    6. history
    7. N `order_items`
    8. 3N reservation writes, including `UPDATE inventory_items SET reserved_qty = reserved_qty + $2` (`:691-724`)
    9. quote consume
    10. coupon usage
    11. `DELETE cart_items`
    12. outbox
    13. commit
  - Same row, other writers:
    - The payments consumer commits the reservation by updating `inventory_items` (`P/p0.go:606-612`).
    - The expiry sweeper releases by updating it (`P/p0.go:552-590`, `:1331`).
    - A seller's stock edit locks it `FOR UPDATE` (`P/inventory_adjust.go:156-161`).
    - The view-count flush writes `UPDATE products SET view_count` on the same product row every ~10 s (`P/products.go:546-549`, `C/cmd/server/main.go:480-494`).
  - The oversell guard is correct: `CHECK reserved_qty <= total_qty` plus the lock (map section 10). This finding is about throughput, not correctness.
- **How to reproduce:** Load test L3 (section 6): 5,000 virtual buyers with the same SKU in their carts quote, then check out within 60 s, against 500 units. Record `pg_locks` waits on the product row, p95 of quote and checkout, and how many quotes return within 2 s.
- **Impact:** Arithmetic, assuming about 0.5 ms per statement round trip to Aurora in the same AZ and about 2 ms per commit: a one-line checkout holds the product lock for roughly 10 ms, so one product admits at most about 100 checkouts per second. Every quote for that product also waits its turn. Buyers on a hot product see the quote and checkout spinners grow linearly with the queue. Captures for already-placed orders wait behind new checkouts, which delays "order confirmed" (see COM-D-18). With a coupon in play, every checkout using that code, on any product, also serialises on the coupon row for the rest of its transaction.
- **Fix (smallest first):**
  - (a) Take the product row `FOR SHARE` (or `FOR KEY SHARE`) instead of `FOR UPDATE` in `priceLinesTx`. The comment says the purpose is to see a moderation change at its committed head; a share lock still blocks a concurrent status `UPDATE` and does not serialise buyers against each other.
  - (b) Quote: price with `lock=false`, as `CartCoupons` already does (`P/checkout.go:853-858`). Checkout re-prices under lock and answers `PRICE_CHANGED` anyway.
  - (c) Move `claimCoupon` and the reservation `UPDATE` to the last statements before commit, so the hot rows are held for the shortest time.
- **Acceptance:**
  - An integration test: two concurrent checkouts of two different variants of one product both commit without either waiting on the other's product row (`pg_locks` shows no `RowExclusiveLock` wait on `products`).
  - L3 thresholds met.
- **Effort:** S for (a) and (b); M for (c)

### COM-D-3

- **ID:** COM-D-3
- **Title:** No `statement_timeout`, `lock_timeout` or `idle_in_transaction_session_timeout` exists anywhere in commerce. Checkouts queued on a hot row each hold one of the pod's 25 pool connections for as long as the wait lasts, and nothing upstream bounds the wait. Browsing on that pod stalls with them.
- **Severity:** P1
- **Category:** resilience / performance
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - Pool: `MaxConns = POSTGRES_MAX_CONNS` (default 25), with no `AfterConnect` or `RuntimeParams` (`C/cmd/server/main.go:66-78`). `POSTGRES_MAX_CONNS` is not set in the prod values at HEAD (only replicas 2-6, CPU 100m-500m, `deploy/services/commerce-service/values-prod.yaml:25-38`).
  - `grep -rn "statement_timeout\|lock_timeout\|idle_in_transaction"` in `C/internal` and `C/database` finds only the migration guards in `050`, `053` and `054`, which set `lock_timeout` for their own `ALTER`s.
  - Every catalogue read, checkout, quote and worker shares the one pool (`commercehttp.New(svc)`, the workers in `main.go:385-438`).
  - The HTTP server's 30 s `WriteTimeout` (`shared/server/server.go:16`) does not cancel the handler's context.
  - The gateway proxies with `http.DefaultTransport` (no `ResponseHeaderTimeout`) (`api-gateway/cmd/server/main.go:399` at HEAD). The wait therefore ends only when the shopper's client gives up.
- **How to reproduce:** In an integration test, hold `SELECT … FROM products WHERE id=$1 FOR UPDATE` open in one connection. Fire 30 checkouts for that product at one commerce instance, then `GET /products` at the same instance. The browse request waits for a pool connection (fails now); after the fix, the checkouts fail fast with a retryable 409/503.
- **Impact:** A hot product or a stuck transaction converts into a pod-wide outage for browsing, because pool acquisition blocks. With HPA scaling on CPU (65%), a lock-bound stall does not even scale out.
- **Fix:**
  - Set `RuntimeParams["statement_timeout"] = "5s"`, `lock_timeout = "2s"` and `idle_in_transaction_session_timeout = "15s"` on the pool. Map a `lock_not_available` / `query_canceled` from checkout to a retryable 503 `CHECKOUT_BUSY`.
  - Optionally give checkout a separate, smaller pool so it cannot starve reads.
  - Set `ResponseHeaderTimeout` on the gateway's upstream transport (platform-wide).
- **Acceptance:** The integration test above. A unit test asserts the pool config carries the three runtime parameters.
- **Effort:** S

### COM-D-4

- **ID:** COM-D-4
- **Title:** Every quote, and so every checkout, makes a live Shiprocket serviceability call: no cache, no breaker, no retry, a 20 s timeout. The code itself says Shiprocket rate-limits that API. Token refresh holds a global mutex across a network call. One user may quote 60 times a second.
- **Severity:** P1
- **Category:** performance / resilience (outbound)
- **Evidence level:** VERIFIED-READ; UNVERIFIED (Shiprocket's actual rate limit)
- **Evidence:**
  - `S/checkout_p0.go:101-150`: `PrepareQuote` calls `s.courier.CheckServiceability` on every call. An error becomes `ErrCourierUnavailable` (503).
  - The only cache is for the delivery-date estimate, and it is explicitly not used by the quote. "Shiprocket rate-limits the serviceability API" … "The quote calls the carrier itself and never reads it" (`S/engagement.go:230-262`).
  - `C/internal/courier/shiprocket.go:46-48`: `http.Client{Timeout: 20 * time.Second}` over `http.DefaultTransport`. `authToken` takes `c.mu.Lock()` and performs the `/auth/login` request while holding it (`:94-119`), so every courier call in the pod waits during a refresh. A cached token is not dropped on a 401.
  - No per-route limit exists on `/checkout/quote` (map section 2.1: only the engagement routes have in-process limits). The gateway allows 60 requests per second per user (map). Each quote also takes the product lock (COM-D-2) and inserts a `shipping_quotes` row (`S/checkout_p0.go:186`).
- **How to reproduce:** Point the courier at a fake that sleeps 15 s or answers 429. Run L3's quote phase and record quote p95 and the share of 503s. In a unit test, two goroutines call `CheckServiceability` while the fake login sleeps 5 s; the second waits for the whole login.
- **Impact:**
  - The shop's checkout throughput is capped by Shiprocket's rate limit for the whole platform, not per seller.
  - A Shiprocket slowdown holds a buyer's quote for up to 20 s and then shows "try again".
  - In a flash sale, thousands of buyers quoting at once are likely to trip the carrier's limiter. Then nobody can check out, including buyers of other products.
  - A single client loop can burn the shared carrier budget.
- **Fix:**
  - (a) Cache the serviceability result for quoting, keyed by (pickup pin, drop pin, weight band, payment mode), for a short TTL (for example 10 min), with `singleflight` per key. Product decides whether a rate that is up to 10 minutes old may be charged; the quote still binds it.
  - (b) A breaker around the Shiprocket client, plus a 5 s per-call timeout for serviceability.
  - (c) Refresh the token outside the mutex (double-checked), and invalidate it on 401.
  - (d) A per-user limit on `/checkout/quote` (for example 10 per minute).
- **Acceptance:**
  - Unit test: 100 concurrent quotes for the same lane make 1 carrier call.
  - Unit test: a 401 from the carrier triggers exactly one re-login.
  - L3 passes with the carrier fake limited to 10 rps.
- **Effort:** S to M

### COM-D-5

- **ID:** COM-D-5
- **Title:** The shop's first screen (`/home`, plus `/categories`) is rebuilt from the live catalogue on every request: five catalogue-wide statements, run one after another, each computing two `LATERAL` subqueries per live product before sorting. Nothing is cached on the server.
- **Severity:** P1
- **Category:** performance
- **Evidence level:** VERIFIED-READ (queries, ordering, no cache); SUSPECTED (latency at catalogue size)
- **Evidence:**
  - `S/storefront.go:422-459`: banners, deals, best sellers (with the most-viewed fallback), then new arrivals, in sequence, then `hydrateHome`. A search finds no cache, `singleflight` or TTL in commerce for these (`grep -rn "singleflight\|ttlcache\|homeCache" C/internal`: none).
  - `P/storefront.go:563-659`: each rail is `SELECT productSummaryColumns FROM productSummaryFrom WHERE productSummaryLive … ORDER BY <expression over the laterals or counters> LIMIT 12`. The projection is two `LATERAL`s per product (`:119-137`). The deals sort key is computed from the lateral output (`:571`); best sellers adds a `LATERAL` over `order_items ⋈ orders` per product (`:595-603`). No index can produce these orders, so every live product is visited.
  - `P/storefront.go:506-537`: the category strip counts live products per root category, with the seller `EXISTS` per product.
  - Android requests categories, home and the product grid on every store open (`feature/commerce/.../home/StoreHomeViewModel.kt:153-160`). The web caches only per tab (section 2.1).
- **How to reproduce:** On the 100k-product fixture with 1M `order_items` in the last 30 days, run `EXPLAIN ANALYZE` for each of the five statements, then L1 (section 6).
- **Impact:** Arithmetic: with 50,000 live products, one home load evaluates about 150,000 to 200,000 lateral subqueries and scans the 30-day order lines once per product. The home rails are the same for everyone (only `is_favourite` differs), so all of that work is repeated per shopper for identical output. This is the screen every app open lands on.
- **Fix:**
  - (a) Cache the viewer-independent `HomePage` (banners, sections, resolved image URLs excluded) and the category cards in-process for 60 s with `singleflight`. Apply favourites and URLs per request.
  - (b) Add maintained columns `products.min_price_minor`, `products.total_available` (or an offer-level equivalent) updated with variant and stock writes, so the rails sort on indexed columns.
- **Acceptance:**
  - Unit test: 100 concurrent `Home` calls make one call per store method within the TTL.
  - L1 home p95 under 150 ms at 200 opens/s on the 100k fixture.
- **Effort:** S for (a), M for (b)

### COM-D-6

- **ID:** COM-D-6
- **Title:** Browse and category pages cannot use an index. The storefront predicate moved to `product_offers`, but the only `created_at` and `category_id` indexes on `products` are partial on `products.status = 'active'`, which the query no longer states. So the first page of any listing sorts the whole live set, with two laterals per row.
- **Severity:** P1
- **Category:** performance (indexes)
- **Evidence level:** VERIFIED-READ (predicates and index definitions); SUSPECTED (resulting plan)
- **Evidence:**
  - Indexes: `idx_products_category ON products(category_id) WHERE status = 'active'` and `idx_products_created ON products(created_at DESC) WHERE status = 'active'` (`DB/setup.sql:203,205`). `product_offers` has only `(seller_id)` and `(product_id, status)` (`DB/migrations/027_product_offers.sql:103-108`).
  - The query: `conds := []string{productSummaryLive}` = `po.status = 'active' AND po.approval_status = 'approved' AND po.visibility = 'public' AND EXISTS (sellers …)` (`P/storefront.go:177-182`). It adds `p.category_id = $n` and the keyset `(p.created_at, p.id) < (…)`, then `ORDER BY p.created_at DESC, p.id DESC LIMIT n+1` (`P/products.go:371-453`). There is no `p.status` term, so neither partial index qualifies.
  - The price and in-stock filters apply to the `LATERAL` outputs (`v.min_selling_price`, `s.total_stock`) in the outer `WHERE` (`P/products.go:424-441`), after the laterals have run for every candidate row.
- **How to reproduce:** `EXPLAIN (ANALYZE, BUFFERS)` of `ListProductsFiltered` on the 100k fixture: (a) no filter, (b) one category, (c) `in_stock=true&min_price=500`. Expect a sequential scan of `products ⋈ product_offers`, laterals per row, then Sort.
- **Impact:** Every browse page and every category tap costs time proportional to the live catalogue (or the category), not to the page size. Keyset pagination keeps deep pages from getting worse, but page 1 is already O(catalogue).
- **Fix:**
  - (a) `CREATE INDEX CONCURRENTLY idx_products_created_id ON products(created_at DESC, id DESC)` and `idx_products_category_created ON products(category_id, created_at DESC, id DESC)`, without the stale partial predicate. Add `idx_product_offers_live ON product_offers(product_id) WHERE status='active' AND approval_status='approved' AND visibility='public'`.
  - (b) Move the price and stock filters onto maintained columns (COM-D-5 (b)).
- **Acceptance:** EXPLAIN on the fixture shows an index scan driving a nested loop with `LIMIT` (no Sort node) for (a) and (b). L1 browse p95 under 150 ms.
- **Effort:** S for (a), M for (b)

### COM-D-7

- **ID:** COM-D-7
- **Title:** Product search is `ILIKE '%q%'` across nine expressions and three `EXISTS` subqueries, with no trigram or full-text index. Every search scans the whole live catalogue, and Android searches as the shopper types (300 ms debounce).
- **Severity:** P2
- **Category:** performance (search)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `P/products.go:351-364`: `p.title`, `short_title`, `description`, `short_description`, `brand_name` and `array_to_string(search_keywords)` are each `ILIKE $n`, plus `EXISTS` on categories, sellers and `product_variants` (SKU or barcode `ILIKE`). The pattern is `"%"+q+"%"` (`:389-392`).
  - No `pg_trgm` or `tsvector` index on products exists (grep of `DB/`; the only trigram indexes are on attribute labels, `025_attribute_definitions.sql:294-296`).
  - Android: `debounce(SEARCH_DEBOUNCE_MS)` with `SEARCH_DEBOUNCE_MS = 300L`, then a full reload (`feature/commerce/.../browse/StoreBrowseViewModel.kt:76-84,192`). The web searches on submit (`src/features/shop/storefront/BrowseScreen.tsx:15,25`).
  - search-service already indexes commerce products from events (map section 1.1), but neither client uses it for the shop.
- **How to reproduce:** `EXPLAIN ANALYZE` of `ListProductsFiltered` with `Query = "shirt"` on the 100k fixture. Then L2 at 50 searches/s.
- **Impact:** Search cost grows with catalogue size times description length. A burst of typed queries from many shoppers becomes a burst of sequential scans on the same Aurora writer that takes checkouts.
- **Fix:**
  - (a) Serve `?q=` from search-service (OpenSearch), keeping the Postgres live rule as a filter on the returned ids.
  - (b) Failing that, a generated `tsvector` column with a GIN index over title, brand and keywords, and drop `description` from the `ILIKE`.
- **Acceptance:** EXPLAIN shows an index (GIN or OpenSearch) path. L2 search p95 under 200 ms.
- **Effort:** M

### COM-D-8

- **ID:** COM-D-8
- **Title:** The public product list still serves a legacy offset path whenever a client sends `?offset=`. The offset has no upper bound, and every such request runs a full `COUNT(*)` of the live catalogue (including the search `ILIKE`).
- **Severity:** P2
- **Category:** performance (pagination)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_catalogue.go:567-580`: `if cursor == "" && c.Query("offset") != ""` → `ListProducts(... limit, offset)`. Only a negative offset is clamped (`:569-571`). `P/products.go:494-497` runs `SELECT COUNT(*) … WHERE <live> [AND search]` on every call, then `LIMIT … OFFSET` (`:510-515`). The route is anonymous (map 2.1).
- **How to reproduce:** `GET /v1/commerce/products?offset=1000000&q=a`, then `?offset=0&q=a`. Compare query time in `pg_stat_statements`.
- **Impact:** Any anonymous caller can force the most expensive form of the listing query at the gateway's per-IP rate (100 rps). No first-party client needs this path (both use cursors).
- **Fix:** Cap `offset` at 1,000 on the public route, or remove the offset branch from the public route and keep it only behind the internal or admin group.
- **Acceptance:** A handler test: `?offset=5000` answers 400 (or the cursor path). No `COUNT(*)` statement on the public route.
- **Effort:** S

### COM-D-9

- **ID:** COM-D-9
- **Title:** The public reviews list takes an unbounded `limit`, pages by `OFFSET`, counts every review per request, and sorts by `helpful_count`, which no index covers.
- **Severity:** P2
- **Category:** performance (unbounded list)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `H/handler_reviews.go:27-28` parses `limit` and `offset` with no clamp. `S/engagement.go:453-454` passes them straight to the store. `P/reviews.go:46-59` runs `COUNT(*)` and then `ORDER BY helpful_count DESC, created_at DESC, id DESC LIMIT $2 OFFSET $3`. The only index is `(product_id, is_published, rating)` (`DB/setup.sql:626`). Every other list in commerce is clamped (`S/orders_read.go:272-283`, VERIFIED-RUN `TestClampListPagination`).
- **How to reproduce:** `GET /v1/commerce/products/<id>/reviews?limit=1000000` on a product with 50,000 reviews. The response holds all of them.
- **Impact:** One anonymous request can return a product's entire review set, with per-review vote hydration (`S/engagement.go:459-470`). A popular product's reviews page sorts all its reviews on every page view.
- **Fix:** Use `clampListPagination` (cap 50 for this public route). Add an index `(product_id, helpful_count DESC, created_at DESC, id DESC) WHERE is_published`. Move to a keyset cursor when the web pager allows.
- **Acceptance:** A handler test: `limit=1000000` returns at most 50. EXPLAIN on a 50,000-review product shows no Sort node.
- **Effort:** S

### COM-D-10

- **ID:** COM-D-10
- **Title:** Seller order lists recompute over the seller's entire order history on every page: a discarded `COUNT(DISTINCT)`, then `SELECT DISTINCT … ORDER BY … OFFSET`. The fulfilment tabs (unshipped, in transit, …) are filtered in Go after the page is fetched, so a tab can show an empty page while matching orders exist further back.
- **Severity:** P2
- **Category:** performance (pagination)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `P/orders.go:320-357`: `COUNT(DISTINCT o.id) … WHERE oi.seller_id=$1`, then `SELECT DISTINCT o.* … JOIN order_items … ORDER BY o.created_at DESC LIMIT $2 OFFSET $3`. `DISTINCT` forces the whole set to be deduplicated and sorted before `LIMIT`. Callers discard the count (`S/orders_read.go:150,211`).
  - `S/orders_read.go:207-262`: `ListSellerFulfillment` fetches a page (limit up to 200, `:272-283`), then drops rows by `fulfillmentMatchesStage` (`:260`).
  - `H/handler_seller.go:178-179,204-205`: offset paging from the client.
- **How to reproduce:** A seller with 100,000 order lines. `GET /seller/fulfillment?stage=unshipped&limit=20` when the 20 newest are all delivered returns `[]`. EXPLAIN shows HashAggregate/Unique plus Sort over all 100,000 rows.
- **Impact:** A successful seller's dashboard slows linearly with sales and shows misleading empty tabs. The client would have to page through history to find work.
- **Fix:** Drop the count. Query `orders` with `EXISTS (SELECT 1 FROM order_items WHERE order_id = o.id AND seller_id = $1)`, push the stage predicate into SQL (on `orders.status` and the shipment state), and keyset on `(created_at, id)`. A `seller_id` column on `orders` is possible, since carts are single-seller (D2).
- **Acceptance:** An integration test: the 21st-newest order, which is the only unshipped one, appears on the first `stage=unshipped` page. EXPLAIN shows no full-history Sort.
- **Effort:** M

### COM-D-11

- **ID:** COM-D-11
- **Title:** A cart has no line cap, so the checkout transaction's length, and the number of rows it locks, grow without bound with the cart.
- **Severity:** P2
- **Category:** performance (unbounded work in a lock)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `S/cart.go:16-80` checks sale eligibility, seller and stock, but not line count. `H/handler_cart.go:32-33` binds `quantity min=1` and nothing else. Checkout loops per line in price, lock, item insert and three reservation writes (`P/checkout.go:422-724`). The quote does the per-line pricing under lock too (`P/quote.go:317`).
- **How to reproduce:** Add 1,000 distinct variants from one seller to a cart, quote, and check out. Time the transaction and the lock count.
- **Impact:** One buyer can hold locks on hundreds of a seller's products for seconds, blocking every other buyer of those products (COM-D-2) and occupying a pool connection (COM-D-3).
- **Fix:** Cap distinct lines (for example 50) and per-line quantity (for example 20, or a per-variant maximum) at add-to-cart and update, with a 409 `CART_LIMIT`.
- **Acceptance:** A service test: adding the 51st distinct line answers `CART_LIMIT`.
- **Effort:** S

### COM-D-12

- **ID:** COM-D-12
- **Title:** The payment-intent call chain has inverted timeouts: commerce gives payments 15 s, but payments gives Razorpay 30 s. A slow PSP makes the buyer see an error while payments-service goes on to create the order. Neither hop has a breaker, and both use Go's default transport (2 idle connections per host).
- **Severity:** P2
- **Category:** resilience (outbound)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - `C/internal/payments/p0client.go:141-148`: `Timeout: paymentsclient.DefaultTimeout` over `otelhttp.NewTransport(http.DefaultTransport)`. `shared/paymentsclient/client.go:95-96`: `DefaultTimeout = 15 * time.Second`.
  - `PAY/internal/gateway/razorpay_provider.go:60`: `&http.Client{Timeout: 30 * time.Second}`; `PAY/internal/gateway/razorpay.go:31`: same.
  - `PAY/internal/service/service.go:417-430`: `CreateOrder` in the intent path. Idempotency is per attempt key (`C/internal/payments/p0client.go:204-235`), so a retry does collapse. The cost is the buyer's experience, not a double order.
  - The status poll calls `GetIntent` on every read (`S/checkout_p0.go:500-504`).
- **How to reproduce:** Fake Razorpay sleeping 20 s. `POST /orders/:id/payment/intent` returns 5xx at 15 s, and payments logs a successful `CreateOrder` at 20 s.
- **Impact:** During PSP latency spikes, which are common at peak, buyers get "payment could not start" and retry, adding load to a PSP that is already slow. Status polls (Android every 1 to 5 s for up to 180 s, web every 5 s) multiply payments-service traffic per waiting buyer.
- **Fix:**
  - Razorpay client timeout 10 s, commerce → payments 12 s (outer greater than inner).
  - A shared transport with `MaxIdleConnsPerHost` of at least 32.
  - A breaker on payments → Razorpay that returns a clear "payments are busy, retry".
  - Answer `/payment/status` from the order row alone while it is `payment_pending` and the intent is younger than 30 s.
- **Acceptance:** A unit test asserts the commerce timeout exceeds the payments provider timeout. L4 (section 6) passes with the PSP fake at p95 = 3 s.
- **Effort:** S

### COM-D-13

- **ID:** COM-D-13
- **Title:** The payments reconciler, the only path that repairs a lost webhook, takes the 50 **oldest** stale intents each minute. An intent that never reaches a terminal outcome (an authorised-but-unsettled payment, or a capture that does not verify) stays at the head forever, so newer intents with lost webhooks may never be looked at. Every payments pod also repeats the same 50 provider fetches.
- **Severity:** P2
- **Category:** resilience / scale (worker)
- **Evidence level:** VERIFIED-READ (ordering, limit, no claim); SUSPECTED (how long such intents stay non-terminal in production)
- **Evidence:**
  - `PAY/internal/store/postgres/p0.go:1439-1452`: `WHERE status IN ('pending','processing') AND created_at < NOW() - age … ORDER BY created_at LIMIT $2`, with no `SKIP LOCKED` and no attempt column.
  - `PAY/internal/service/p0.go:700-770`: `StalePending(ctx, pendingAge, 50)`. A provider error only backs off in memory (`s.recon.backOff`). A non-terminal outcome is `continue` with no state change (`:766-770`).
  - `reconcileOutcome` returns `""` when any payment is authorised or created (`unsettled > 0`) or a capture fails verification (`:921-931`).
  - The reconciler runs on a ticker in every pod (`:643-660`); grep finds no advisory or leader lock in `PAY/internal/service/p0.go` or `PAY/cmd/server/main.go`.
  - Commerce fixed the same head-of-line pattern in its own sweeper (migration 053, `P/p0.go:1251-1254`).
- **How to reproduce:** An integration test on `payments_it_test`: insert 50 old intents whose fake provider reports an `authorized` payment, plus one newer intent whose fake reports `captured`. Run `reconcileOnce` three times. The captured intent is never reconciled (fails now).
- **Impact:** After a flash sale with many abandoned or stuck intents, a paid order whose webhook was lost is not repaired. Commerce expires it at 20 minutes and the late capture becomes a refund (map section 10). The buyer pays, loses the item and waits for a refund. Each extra pod adds redundant Razorpay API calls.
- **Fix:** Persist `reconcile_attempts` and `reconcile_next_at` on the intent and order by `reconcile_next_at`, as commerce migration 053 does. Claim with `FOR UPDATE SKIP LOCKED`, or under an advisory lock so one pod reconciles.
- **Acceptance:** The integration test above passes. A test with two reconciler instances shows each intent fetched once per tick.
- **Effort:** M

### COM-D-14

- **ID:** COM-D-14
- **Title:** Expired holds are released at most 200 orders per minute, one transaction each. Every pod scans and races for the same 200, and each release writes the hot inventory row. After a large sale, unpaid stock stays unavailable long after the 20-minute hold.
- **Severity:** P2
- **Category:** scale (sweeper)
- **Evidence level:** VERIFIED-READ (batch and interval); SUSPECTED (backlog in practice)
- **Evidence:** `S/checkout_p0.go:696-717` runs `ExpireStaleOrders(ctx, 200)` every minute (`C/cmd/server/main.go:399`). `P/p0.go:1255-1297` selects candidates without a claim; `expireOne` locks per order (`:1313-1350`) and releases reservations (`:1331`, which writes `inventory_items`). There is no advisory lock, so N pods select the same rows; the losers find `status != payment_pending` and skip (`:1328`). The reservation TTL is 20 min (`P/checkout.go:680`).
- **How to reproduce:** Seed 10,000 `payment_pending` orders whose reservations lapsed at the same moment. Count released orders per minute with 1 and with 3 commerce instances.
- **Impact:** Arithmetic: a 50,000-unit sale with 30% abandonment leaves 15,000 holds. At 200 per minute they take about 75 minutes to return after the TTL, and the product shows out of stock meanwhile. Each release also queues on the hot inventory row behind live checkouts (COM-D-2).
- **Fix:** Claim candidates with `FOR UPDATE SKIP LOCKED` (or `UPDATE … RETURNING` with a lease) so pods share the work. Loop while full batches come back (as the outbox relay does). Raise the batch to 500.
- **Acceptance:** In an integration test, 10,000 lapsed orders are expired within 5 minutes by two instances, with no order processed twice.
- **Effort:** S

### COM-D-15

- **ID:** COM-D-15
- **Title:** Paid orders are fulfilled one job at a time per pod: an invoice (S3 uploads) plus two Shiprocket calls with 20 s timeouts, inside a 30 s budget. A sale's paid orders form a queue measured in hours before sellers get AWBs.
- **Severity:** P2
- **Category:** scale (worker)
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (per-job time)
- **Evidence:**
  - `C/internal/workers/fulfillment.go`: one `drain` loop per pod, `ClaimNextJob` one at a time (`FOR UPDATE SKIP LOCKED LIMIT 1`, `P/jobs.go:63-69`), `pollEvery = 1s`, `maxAttempts = 5`.
  - `S/jobs.go:43-57`: `IssueInvoice`, then `CreateShipmentForOrder`, with `context.WithTimeout(ctx, 30*time.Second)`.
  - `S/shipments.go:640-689`: a sequence upsert on one row per financial year (`P/shipments.go:17-28`), the HTML render and upload, a best-effort PDF render and upload, then the DB insert. `wkhtmltopdf` is not installed in the image (`C/Dockerfile:22-25`, alpine with only `/server`), so the PDF branch always fails fast; the PDF never exists (a note for Track A).
  - `C/internal/courier/shiprocket.go:169,201,565`: create, then assign AWB.
  - Prod commerce is 2 to 6 pods with a 500m CPU limit (values-prod at HEAD `:25-38`).
- **How to reproduce:** Enqueue 5,000 `fulfill_paid_order` jobs with a courier fake answering in 800 ms. Measure drain time with 2 pods.
- **Impact:** Arithmetic: about 1 to 2 s per job gives about 1 to 2 jobs per second with 2 pods. 5,000 paid orders take roughly 40 to 80 minutes; a Shiprocket slowdown multiplies that. The fulfilment jobs share pods (and the DB pool) with buyer traffic.
- **Fix:** Run N concurrent drainers per pod (for example 4, configurable). Split invoice and shipment into separate jobs so one failure does not redo the other. Add a queue-age gauge and alert (`fulfillment_jobs` oldest pending).
- **Acceptance:** The 5,000-job test drains in under 10 minutes with 2 pods and a 800 ms fake. The gauge appears in `/metrics`.
- **Effort:** S to M

### COM-D-16

- **ID:** COM-D-16
- **Title:** Product images get a new signed URL every 2 minutes (5-minute lifetime), so phone and browser image caches, which key on the full URL, miss on every revisit. A lazily loaded tile in a list fetched more than about 3 minutes earlier can fail with an expired signature. And each image costs about 4 RSA signatures to resolve.
- **Severity:** P2
- **Category:** performance / cost (images)
- **Evidence level:** VERIFIED-READ (TTL, reuse window, client cache keys, CDN policy); SUSPECTED (expiry visible to users)
- **Evidence:**
  - `MS/internal/delivery/signer.go:72`: `MaxProtectedTTL = 5 * time.Minute`. `sign_cache.go:55-60`: `SignatureReuseWindow = 2 * time.Minute`, `GuaranteedProtectedTTL = MaxProtectedTTL - SignatureReuseWindow` (3 min).
  - Android `AsyncImage(model = url)` uses Coil's default URL key, with no custom `diskCacheKey` anywhere in the app (`core/commerce-ui/.../CommerceComponents.kt:87-95`; grep for `diskCacheKey|memoryCacheKey` finds only Media3 video caches). The web uses plain `<img src>` (`src/features/shop/components/storefront/ProductPhoto.tsx:31`), and its product lists stay in memory for TanStack's default 5 min (`staleTime` 30 s, `src/features/shop/hooks/storefront.ts:60-67`).
  - The CDN side is fine: the media distribution uses the `CachingOptimized` policy, which excludes query strings from the cache key (`infra/terraform/modules/media/main.tf:297,316`), so CloudFront itself keeps hitting.
  - Signing: `original` plus renditions per image (`MS/internal/service/media_batch.go:76-90,199-204`).
- **How to reproduce:** On QA, open the web browse grid, wait 4 minutes, and scroll to tiles below the fold (lazy). Count 403s from the CDN. On Android, open a product grid twice 3 minutes apart and count image bytes downloaded (Coil disk cache misses).
- **Impact:** Every return to a shop screen after 2 minutes re-downloads its images on mobile data. Arithmetic: 24 tiles × about 20-60 KB (`thumb`) on Android; on the web, 24 × the 1080 px rendition (COM-D-17). The re-signing CPU is about 200 ms of media-service per new 24-tile page (section 2.3).
- **Fix:**
  - The structural fix is COM-D-1 (c): public, stable URLs for live product images.
  - Short of that, give Coil a disk-cache key of the URL without its query string (`ImageRequest.diskCacheKey`) and do the same in a web image loader.
  - Refetch the list when its oldest URL is older than 3 minutes.
- **Acceptance:** On Android, a second open of the same grid after 3 minutes downloads 0 image bytes (instrumented test with MockWebServer). The web shows no CDN 403 in the 4-minute scroll scenario.
- **Effort:** S (client keys), M (public delivery class)

### COM-D-17

- **ID:** COM-D-17
- **Title:** The web product grid draws `image_url` (the 1080 px rendition) in every tile; the thumbnail it is also sent is the fallback, not the choice.
- **Severity:** P2
- **Category:** performance (images, web)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `src/features/shop/model/storefront.ts:93-97`: `return p.image_url || p.thumbnail_url || p.source_image_url || null`, used by `toProductCard` (`:128-137`). `image_url` is `Resolved.URL()`, which prefers `medium_1080` (`C/internal/media/media.go:303-327`). Android uses `thumbnailUrl ?: imageUrl` (`core/commerce-ui/.../ProductCard.kt:74`). The category strip already prefers the thumbnail (`storefront.ts:325`).
- **How to reproduce:** Open `/shop/browse` on a throttled 4G profile and compare transferred image bytes for a 24-tile page against the same page drawing `thumbnail_url`.
- **Impact:** Several times the bytes per tile against `small_480` on every web grid, including the home rails. That means slower first paint and more CDN egress cost.
- **Fix:** Use `thumbnail_url` (or `small_480` through `srcset`) for cards, and `image_url` on the product page only.
- **Acceptance:** A unit test of `toProductCard`: a summary with both URLs yields the thumbnail.
- **Effort:** S

### COM-D-18

- **ID:** COM-D-18
- **Title:** Order confirmation depends on one sequential Kafka handler per commerce pod. It reads the whole shared `social.events.v1` firehose (6 partitions) to find payment events, retries a failing event forever in place, and during a sale its inventory update queues behind checkouts. A burst can leave buyers on "confirming" past the 180 s client window.
- **Severity:** P2
- **Category:** scale / resilience (consumer)
- **Evidence level:** VERIFIED-READ (structure); SUSPECTED (lag under load)
- **Evidence:**
  - `C/internal/consumers/payments_p0.go:91-125`: group `commerce-payments`, topic `social.events.v1`, `RetryForever: true`. Non-payment types return early (`:118-123`) but are still fetched and decoded.
  - `shared/kafka/consumer.go:186-201,283-355`: `FetchMessage` then handle, one message at a time, with exponential backoff in place.
  - Partitions: 6 in prod (`infra/terraform/modules/msk/topics-job/variables.tf:50-51`). commerce HPA is at most 6 pods.
  - The capture applies `commitReservationsTx` (`P/p0.go:606-612`) on the hot inventory row (COM-D-2).
  - The Android poll gives up at 180 s (`PaymentCoordinator.kt:108-110`).
- **How to reproduce:** L5 (section 6): 2,000 signed webhooks in 60 s while L1 social traffic runs. Measure webhook-to-`confirmed` p99 and consumer lag for `commerce-payments`.
- **Impact:** Confirmation latency is coupled to unrelated platform traffic and to checkout contention. A transient DB error on one event pauses its whole partition, including other buyers' confirmations.
- **Fix:**
  - (a) Publish payment events to a dedicated `payments.events.v1` topic (it also helps the map's event-trust question).
  - (b) Process per-key concurrently within a partition (bounded worker pool keyed by `reference_id`).
  - (c) Alert on `commerce-payments` lag.
- **Acceptance:** L5: webhook-to-confirmed p99 under 10 s, lag back to 0 within 60 s of the burst ending.
- **Effort:** M

### COM-D-19

- **ID:** COM-D-19
- **Title:** The seller dashboard runs eight sequential aggregates per load, including an all-time revenue `SUM` over every order line the seller ever had, and ignores every error.
- **Severity:** P3
- **Category:** performance (per-request aggregates)
- **Evidence level:** VERIFIED-READ
- **Evidence:** `P/onboarding.go:682-711`: four `COUNT(*)` on offers, a low-stock count over `inventory_items ⋈ variants ⋈ products ⋈ offers`, today's orders, and `SUM(oi.final_price)` over all paid lines. Every `Scan` error is discarded (`_ =`).
- **How to reproduce:** A seller with 100,000 lines; `GET /v1/commerce/dashboard`; `pg_stat_statements` shows the revenue statement's rows read.
- **Impact:** Dashboard latency grows with lifetime sales, and a timeout reads as zero rather than as an error.
- **Fix:** Run the counts in one statement with `FILTER` clauses. Keep revenue in a per-seller daily roll-up updated by the paid transition. Return an error instead of zero.
- **Acceptance:** One statement for the product counts. Revenue is read from the roll-up (EXPLAIN reads fewer than 400 rows).
- **Effort:** M
- **Note for Track A:** that `SUM` reads the legacy NUMERIC `final_price`, which checkout now writes as `0` (`P/checkout.go:608-609`), so the figure is likely zero for every order placed through `/v2/orders/checkout`.

### COM-D-20

- **ID:** COM-D-20
- **Title:** When Redis errors, every product view falls back to `UPDATE products SET view_count = view_count + 1` on the same row checkout locks. The update runs in an unbounded goroutine with `context.Background()`.
- **Severity:** P3
- **Category:** resilience
- **Evidence level:** VERIFIED-READ
- **Evidence:** `S/catalogue_products.go:311` (`go s.adjustProductViewCount(context.Background(), productID)`). `S/service.go:213-220` falls back to `s.store.IncrProductViewCount` on any `Inc` error. `P/products.go:539-541`.
- **How to reproduce:** Stop Redis on a dev stack and run L1's product-detail mix. Record goroutine count and lock waits on `products`.
- **Impact:** A Redis blip during a sale turns product views into row-lock contention with checkout (COM-D-2) and an unbounded number of goroutines waiting on the pool.
- **Fix:** On Redis error, drop the view (counts are approximate by design). Bound the goroutine with a 200 ms context.
- **Acceptance:** A unit test: with a failing counter, no `UPDATE products` is issued.
- **Effort:** S

### COM-D-21

- **ID:** COM-D-21
- **Title:** Several append-only tables are never pruned: `outbox_events` (published rows), `shipping_quotes` (one row per quote), `payment_event_inbox`, `inventory_ledger`, and released or committed `inventory_reservations`. One gauge scans reservations every 30 s per pod without a supporting index.
- **Severity:** P3
- **Category:** scale (retention)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - grep for `DELETE FROM` on these tables in `C/` and `shared/outbox` finds only targeted reservation deletes (`P/jobs.go:210`, `P/variants_stock.go:382,432`).
  - The outbox relay is protected by the partial index `idx_outbox_unpublished … WHERE published_at IS NULL` (`DB/migrations/005_outbox_and_perf.sql:29-31`), so it does not slow down; the table only grows.
  - `PrepareQuote` inserts on every quote (`S/checkout_p0.go:186`).
  - `OldestLiveReservationAge` runs `WHERE released_at IS NULL AND committed_at IS NULL` (`P/p0.go:1354-1366`) every 30 s in every pod (`C/cmd/server/main.go:606-630`), and the reservation indexes are on `variant_id`, `order_id` and `user_id` only (`DB/setup.sql:282-284`).
- **How to reproduce:** After L3 and L5, record table sizes and the gauge query's duration.
- **Impact:** Storage and vacuum cost grow with traffic. The gauge query's cost grows with all reservations ever made.
- **Fix:** A daily retention job (for example published outbox rows older than 7 days, consumed or expired quotes older than 30 days, inbox rows older than 90 days, subject to Track C retention rules). Add a partial index `inventory_reservations(created_at) WHERE released_at IS NULL AND committed_at IS NULL`.
- **Acceptance:** The retention job exists with a test. EXPLAIN of the gauge uses the partial index.
- **Effort:** S

### COM-D-22

- **ID:** COM-D-22
- **Title:** No load test exists, or has ever been run, for any commerce or payments endpoint, the checkout transaction, a flash sale, or a webhook burst. There is no benchmark in commerce or payments. Every capacity statement about MStore or MSeller is unproven.
- **Severity:** P1
- **Category:** tests / performance
- **Evidence level:** VERIFIED-RUN (executed step 3)
- **Evidence:**
  - Load tests in modernsmapp: `dating-service/loadtest/*.js` (3), `rider-service/loadtest/*.js` (3), and `Architecture/tools/loadtest/{lib,long-video,reels}`. None names `/v1/commerce` or payments (the one hit, `rider-service/loadtest/load_ride_create.js:58`, is a rider payment-method label). pb-echo has none.
  - No `func Benchmark` in commerce-service, payments-service, `shared/outbox` or `shared/kafka`.
  - The map's section 12 says "Load: none for commerce or payments". The dev smoke stops at the quote (`scripts/smoke/dev-smoke.mjs:884-952`).
  - The existing k6 guard (`Architecture/tools/loadtest/lib/guard.js`) is reusable.
- **How to reproduce:** Run the search in step 3.
- **Impact:** The hot-product checkout ceiling (COM-D-2), the image-authorisation latency (COM-D-1), the carrier's limit (COM-D-4) and the home page cost (COM-D-5) are all unknown numbers.
- **Fix:** Write and run L1 to L9 (section 6) against a QA stack sized like production, with Razorpay and Shiprocket fakes. Commit the scripts and the results.
- **Acceptance:**
  - k6 scripts exist under `Architecture/tools/loadtest/commerce/` and use `assertSafeTarget`.
  - A results file in `docs/production-readiness/commerce/` records p50/p95/p99, error rate, CPU, Postgres statements per second, lock waits, consumer lag and queue ages for each scenario, and meets the section 6 thresholds.
- **Effort:** L

### COM-D-23

- **ID:** COM-D-23
- **Title:** Smaller unbounded or per-row work:
  - the bag's coupon list prices each line and counts each coupon's per-user usage separately (N + C queries);
  - the internal media-access batch takes any number of ids;
  - the admin seller and product queues take an unclamped `limit` with `OFFSET` and a `COUNT` per page;
  - the cursor product list ignores Android's `sort` parameter.
- **Severity:** P3
- **Category:** performance (N+1 / unbounded)
- **Evidence level:** VERIFIED-READ
- **Evidence:**
  - Coupons: `P/coupons.go:480-600`, with `priceLinesTx` per line (`:511`) and `evaluateCoupon`'s per-coupon `SELECT count(*) FROM coupon_usages …` (`:305-306`). The candidate coupons are unbounded (`ORDER BY code`, no `LIMIT`).
  - Media access: `H/handler_storefront.go:340-362` has no length check (media-service sends at most 50, but the route accepts more).
  - Admin queues: `H/handler_onboarding.go:475-478,600-603` → `S/onboarding.go:333-334,520-521` → `P/onboarding.go:296-305,515-523`.
  - Android sort: `core/commerce/.../network/CommerceApi.kt:49-56` sends `sort`. `H/handler_catalogue.go:525-600` never reads it.
- **How to reproduce:** A bag with 20 lines and a seller with 30 public coupons: count statements for `GET /cart/coupons` (expect about 53).
- **Impact:** Small at launch; each grows with seller behaviour.
- **Fix:** Price the lines in one statement (`WHERE v.id = ANY($1)`). Fetch per-user usage for all candidate coupons in one `GROUP BY`. Cap candidate coupons at 20. Cap the media-access batch at 50. Clamp admin limits with `clampListPagination`. Either implement `sort` or stop sending it.
- **Acceptance:** A statement-count test for `CartCoupons`: at most 5 statements regardless of lines and coupons.
- **Effort:** S

## 4. Checked and found sound

| Area | What holds | Evidence |
|---|---|---|
| Buyer order list | Keyset on `(created_at, id)`, limit at most 50, no `COUNT`, served by `idx_orders_customer (customer_user_id, created_at DESC)`; item counts in one `LATERAL` per row on the page | `P/orders.go:231-294`, `S/orders_read.go:43-46`, `DB/setup.sql:461` |
| Cart read | One query with joins; no N+1 | `P/cartview.go:93-160` |
| Seller order detail and fulfilment hydration | Items for the page in one `ANY` query, images in one media batch (the comment's "41 to 3 round trips") | `S/orders_read.go:220-240` |
| Favourites | Keyset, limit at most 100 | `P/storefront.go:387-400` |
| Media batching | Deduplicated and chunked at 50, never truncated; the home page resolves in one media call | VERIFIED-RUN `TestMoreThanTheBatchLimitIsChunkedNotTruncated`, `TestTheWholeHomePageResolvesInOneMediaCall`; `C/internal/media/media.go:351-377` |
| List clamps | Seller, buyer and fulfilment lists clamp at 200 (except COM-D-9 and COM-D-23) | VERIFIED-RUN `TestClampListPagination`; `S/orders_read.go:272-283` |
| Images not proxied | Product image bytes go CDN → client by signed CloudFront URL. commerce streams bytes only for KYC documents on the admin route. The CDN cache ignores the signature query | `C/internal/media/media.go:280-337`, map section 6, `infra/terraform/modules/media/main.tf:297` |
| Rendition choice on Android | Grid, cart and order lines use `thumbnail_url` | `core/commerce-ui/.../ProductCard.kt:74`, `C/internal/media/media.go:303-337` |
| Checkout correctness under concurrency | Per-(user, key) advisory lock instead of a global one; deterministic lock order (cart, then variants ascending, then inventory ascending); oversell guarded by the CHECK; address encryption done before the transaction, so there is no KMS call inside it | `P/checkout.go:256-265,791-810,1004-1039,342-347` |
| A cached price is never charged | The cart view shows the current price; quote and checkout re-price under lock; `expected_total_minor` must match or the answer is `PRICE_CHANGED`. Delivery-estimate cache entries never price an order | `P/cartview.go:146-155`, `P/checkout.go:437-520`, `S/engagement.go:230-262` |
| Delivery estimate cache | 6 h TTL, 20,000-entry bound, carrier only (stub never cached) | VERIFIED-RUN `TestEstimateCacheShiprocketOnly`; `S/engagement.go:240-275` |
| Visibility in queries (no cross-viewer cache to leak) | No commerce response is cached across viewers today; the live rule (offer state, seller approved and active) is in every listing's SQL, so unpublished products and suspended sellers drop out on the next request | `P/storefront.go:177-182`; grep finds no response cache |
| Outbox relay | Claim with lease and `SKIP LOCKED`, 100 rows × up to 50 rounds per 500 ms pass, served by a partial index on unpublished rows; `RequireAll` acks | `shared/outbox/publisher.go:24-27,81-86,127-139`, `shared/outbox/relay.go:20-60`, `DB/migrations/005_outbox_and_perf.sql:29-31` |
| Fulfilment job claim | `FOR UPDATE SKIP LOCKED LIMIT 1`, stuck-job recovery every 5 min | `P/jobs.go:63-69`, `C/internal/workers/fulfillment.go` |
| PSP webhook path | DB only, no outbound call; intent lookup by `(provider, provider_order_id)` is indexed | `PAY/internal/http/handler.go:1150-1220` (HEAD), `PAY/database/migrations/007_provider_neutral_and_refund_durability.sql:49` |
| Product view counter | 32 Redis shards, flushed about every 10 s (but see COM-D-20 for its fallback) | `S/service.go:200-220`, `C/cmd/server/main.go:480-494` |
| PII data keys | Cached per (scope, version) for 10 min; one KMS call per key per pod per 10 min (no `singleflight`, so a brief burst at expiry) | `C/internal/pii/envelope.go:105-110,183-208` |
| Client paging and search | Web and Android use `next_cursor` for the grid; the web searches on submit; Android debounces 300 ms | `src/features/shop/hooks/storefront.ts:60-67`, `feature/commerce/.../browse/StoreBrowseViewModel.kt:76-84,137-155` |

## 5. Outbound calls on the commerce paths

| Caller → callee | Timeout | Pool | Retry | Breaker | Buyer sees when slow | Evidence |
|---|---|---|---|---|---|---|
| commerce → media-service (`/v1/media/batch`) | 3 s | Go default (2 idle per host) | none | none | Placeholders (fail-soft) | `C/internal/media/media.go:111-129,405-416` |
| media-service → post, chat, profile, commerce, group (authz) | 3 s each, sequential | Go default | none | none | The whole batch fails → placeholders | `MS/internal/delivery/authz.go:171-177,437-492` |
| commerce → payments-service | 15 s | Go default (`otelhttp` over `DefaultTransport`) | Caller retries collapse on the attempt key | none | 5xx on "Pay" after up to 15 s | `C/internal/payments/p0client.go:141-148`, `shared/paymentsclient/client.go:95-96` |
| payments → Razorpay | 30 s | Go default | Idempotent `FetchByIdempotencyKey` recovery | none | As above (outer timeout fires first) | `PAY/internal/gateway/razorpay_provider.go:60`, `PAY/internal/service/service.go:417-450` |
| commerce → Shiprocket (quote, shipment, AWB) | 20 s | Go default | none (fulfilment job retries up to 5 attempts) | none | Quote spinner up to 20 s, then 503 "try again" | `C/internal/courier/shiprocket.go:46-48,94-119`, `S/checkout_p0.go:122-140` |
| commerce → S3 (invoice) | AWS SDK default | — | SDK default | none | Not on a buyer path (job) | `S/shipments.go:653-664` |
| commerce → AWS KMS (PII) | AWS SDK default | — | SDK default | none | Address and quote calls fail on a key-cache miss | `C/internal/kmsclient/kmsclient.go:170` |
| commerce → identity-auth (role grant) | not traced | — | durable intents table | none | Not on a buyer path | `C/cmd/server/main.go:440-462` |
| notifications | Asynchronous via the outbox and Kafka; no synchronous call from commerce | — | — | — | Nothing (late notification) | map section 4 |
| gateway → commerce | no response-header timeout | Go default | none | none | Waits as long as the client does | `api-gateway/cmd/server/main.go:382,399` (HEAD) |
| Postgres | no statement or lock timeout; 25 connections per pod (commerce and payments each, on the shared `commerce_db`) | — | — | — | See COM-D-3 | `C/cmd/server/main.go:74-77`, `PAY/cmd/server/main.go:61-62` |

No hop on the commerce paths has a circuit breaker. The one shared breaker helper (`shared/httpclient.NewWithBreaker`, used by feed-service) is not used here.

## 6. The load test that is needed

Run against a QA stack sized like production: Aurora writer as in prod, MSK with 6 partitions, Redis, CloudFront, commerce and payments with prod values. Use a **Razorpay fake** that signs webhooks with a test secret and has configurable latency, and a **Shiprocket fake** with configurable latency and a configurable rate limit. Never call the real providers. All scripts are k6 under `Architecture/tools/loadtest/commerce/`, guarded by `assertSafeTarget` with `ALLOW_WRITES=yes` for writes.

**Seed data:**

- 100,000 live products (5% with 8 images, the rest with 1 to 4), 1 to 5 variants each, across 40 categories and 2,000 approved sellers;
- 20,000 draft or rejected products and 50 suspended sellers (for visibility checks);
- 1,000,000 orders over 90 days (1,000,000 `order_items` in the last 30 days), one seller holding 100,000 order lines;
- one product with 50,000 reviews;
- 200,000 buyers with addresses;
- 30 public coupons on one seller.

| ID | Scenario | Rate | Pass thresholds |
|---|---|---|---|
| L1 | Shop open and browse, signed-in and anonymous mix: `/home`, `/categories`, `/products` (first page and 3 cursor pages, 30% with a category), `/products/:id` (Zipf over the catalogue), `/products/:id/reviews?limit=20` | Ramp to 300 rps over 10 min, hold 20 min | p95 < 200 ms and p99 < 600 ms per route; `/home` p95 < 150 ms; errors < 0.5%; commerce CPU < 70%; Aurora CPU < 60%; no sequential scan of `products` in `pg_stat_statements` top 10 |
| L2 | Search: `/products?q=` with 1 to 3 words from a dictionary, 20% with `in_stock=true&min_price` | 50 rps | p95 < 200 ms, p99 < 500 ms |
| L3 | **Flash sale on one product:** 500 units on one variant (plus 4 sibling variants). 20,000 buyers, each with that SKU in the cart, quote then check out within 120 s; 10% use one seller coupon; 30% of buyers who get an order never pay | Peak at about 300 quotes/s and 300 checkouts/s | No oversell (`reserved_qty <= total_qty` and orders ≤ 500); quote p95 < 1 s; checkout p95 < 1.5 s; losers get `OUT_OF_STOCK` within 2 s; no request waits on a lock longer than `lock_timeout` (COM-D-3); `/products` on the same pods keeps p95 < 300 ms; expired holds released within 5 min of TTL (COM-D-14) |
| L4 | Payment start and confirm: 2,000 buyers open an intent, the Razorpay fake answers in p50 300 ms / p95 3 s, 85% pay, and Android-shape polling runs (1 s + 1 s steps, 5 s cap) | about 30 intents/s | intent p95 < 4 s; 0 intent 5xx while the fake is under 10 s; `/payment/status` p95 < 100 ms; payments-service CPU < 60% |
| L5 | **Webhook burst:** 2,000 signed `payment.captured` webhooks in 60 s plus 200 duplicates and 100 replays, while L1 runs at 50% | about 35/s, peaks at 100/s | webhook p99 < 300 ms; 0 double captures applied; webhook-to-`confirmed` p99 < 10 s; `commerce-payments` lag back to 0 within 60 s of the burst ending (COM-D-18) |
| L6 | Fulfilment drain: 5,000 `fulfill_paid_order` jobs with the courier fake at 800 ms | as stated | queue drained in under 10 min; 0 dead letters; buyer routes unaffected (L1 p95 under threshold) |
| L7 | Seller surfaces for the 100,000-line seller: `/seller/orders`, `/seller/fulfillment?stage=unshipped`, `/dashboard`, `/seller/products`, `PATCH /seller/variants/:id/stock` | 50 rps | p95 < 300 ms; stock PATCH p95 < 100 ms during L3 |
| L8 | Image resolution: `POST /v1/media/batch` with 24 to 50 product ids, Zipf over the catalogue | 200 rps | p95 < 150 ms; ≤ 2 authority calls per batch (COM-D-1); commerce media-access statement p95 < 20 ms; 0 unresolved batches |
| L9 | Soak: L1 + L4 + L7 at 50% for 6 h, with Redis restarted once and the Shiprocket fake returning 429 for 5 min | — | no memory growth > 10% per hour in commerce or payments; checkout recovers within 1 min after the 429 window; no `UPDATE products SET view_count` during the Redis restart (COM-D-20) |

For each run, record:

- p50, p95, p99 and error rate per route;
- CPU and memory per service;
- Postgres statements per second, lock waits (`pg_locks`, `pg_stat_activity.wait_event`) and pool acquire time;
- Kafka consumer lag for `commerce-payments`;
- `fulfillment_jobs` and reservation queue ages;
- outbound call counts to the fakes.

Commit the results in a file in `docs/production-readiness/commerce/`.

## Not verified

- **Every number is arithmetic or an estimate.** No latency, throughput or plan was measured. The only timing reused is the 2.08 ms RSA signature from long-video Track D (this laptop, Go `crypto/rsa`).
- **Query plans** for `VisibleProductMediaIDs`, the summary projection, the home rails, `ListProductsFiltered` (with and without filters), the search `ILIKE`, `GetOrdersBySeller` and `ExpireStaleOrders`. No EXPLAIN was run, and no database was opened.
- **Whether the product-row `FOR UPDATE` could be relaxed to `FOR SHARE`** without reopening the moderation race the comments describe (COM-D-2 fix (a)). This needs Track A or Track B review.
- **Shiprocket's actual serviceability rate limit and latency.** Only the in-code comment was found (`S/engagement.go:232-233`).
- **Razorpay's API rate limits and the latency of `CreateOrder` and `FetchOrderPayments`.**
- **The prod Aurora instance class and `max_connections`.** `aurora_instance_class` and the serverless ACU range are variables (`infra/terraform/envs/prod/main.tf:114-117`). commerce and payments together can open 300 connections at maximum HPA.
- **Gateway behaviour** for a slow upstream beyond the default transport (no response-header timeout found). The per-IP limit of 100 rps under Indian mobile carrier-grade NAT, where many shoppers share one IP during a sale, was not tested.
- **Android:** the request count of the product and checkout screens, the Coil cache configuration beyond the default key, and list rendering cost (`LazyVerticalGrid` was not profiled). No device was used.
- **Web:** bundle size and render cost of the shop routes; whether the Next server's per-product metadata fetch through the gateway counts toward the gateway's per-IP limit (one Next pod IP for every visitor).
- **Whether `products.status`** is still kept equal to `product_offers.status` on every write path (COM-D-6 assumes the partial indexes are merely unused, not wrong).
- **The `commerce.order.*` notification consumers'** throughput in notification-service.

## Notes for other tracks

- **Track A:**
  - The seller dashboard's revenue sums the legacy NUMERIC `order_items.final_price`, which checkout writes as `0`, so revenue is likely always zero for v2 orders (COM-D-19; `P/checkout.go:608-609`, `P/onboarding.go:704-707`).
  - Fulfilment tabs filter after paging and can show empty pages (COM-D-10).
  - `wkhtmltopdf` is not in the image, so no invoice PDF is ever produced; the buyer gets the HTML (`C/Dockerfile:22-25`, `S/shipments.go:658-668`).
  - The invoice sequence is one row per financial year for the whole platform, incremented outside the invoice insert, so a failed job leaves a gap in the GST invoice series (`P/shipments.go:17-28`, `S/shipments.go:642-644`).
  - Android sends `sort` on the product list and the server ignores it (COM-D-23).
  - An expired signed image URL can break lazily loaded tiles (COM-D-16).
- **Track B:**
  - Any anonymous caller can force a full live-catalogue `COUNT` plus deep `OFFSET` (COM-D-8), or fetch every review of a product in one request (COM-D-9).
  - One signed-in user can spend the platform's shared Shiprocket budget and hold product row locks by looping quotes (COM-D-4).
  - The internal media-access batch has no size cap (COM-D-23).
- **Track C:** No retention exists for `shipping_quotes` (which embed address hashes and pincodes), `payment_event_inbox`, `inventory_ledger` or published `outbox_events` (COM-D-21).
- **Track E:**
  - No circuit breaker exists on any commerce outbound call (section 5).
  - No `statement_timeout` or `lock_timeout` exists (COM-D-3).
  - Inverted payment timeouts (COM-D-12).
  - Reconciler head-of-line starvation and duplicate work across pods (COM-D-13).
  - Single-threaded payment consumer with `RetryForever` on a shared topic (COM-D-18).
  - No queue-age alerts for `fulfillment_jobs` or lapsed reservations (COM-D-14, COM-D-15).
  - The gateway's upstream transport has no response-header timeout (platform-wide).
- **Track F:** No test covers concurrent checkouts on sibling variants (lock scope), the media-access query plan, the reconciler with non-terminal intents at the head, a capped cart, or the reviews `limit` clamp. All the scenarios in section 6 are missing.
