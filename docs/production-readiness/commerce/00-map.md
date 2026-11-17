# Commerce (MStore / MSeller): Stage 0 map

- **Audit date:** 2026-11-17
- **Backend and Android tree:** `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `d83616ad6`. 682 dirty paths in `git status --porcelain` (mostly admin-service, vendor and `deploy/services/*/values-*.yaml`). **This map describes the committed code (HEAD).** For files with uncommitted edits, the committed version was read with `git show HEAD:<path>`. Those files are listed in section 14, and their uncommitted hunks were not audited.
- **Web tree:** `C:\workspace\wt\pb-echo` (repo `postbook-ui`), branch `feat/echo-web` @ `1b596b4`, clean. The shop lives under `src/app/shop` and `src/features/shop`.
- **How this map was built:**
  - Read directly: the commerce schema (setup.sql, migrations 001-055, gated 998-1002), the order state machine, inventory reservation, GST, coupons, the paid path, the workers, the blob store, the KYC validator, the Razorpay webhook verifier, the Android and web client inventory, and the committed prod values.
  - From three delegated read-only passes: the commerce route table (section 2.1), payments-service and admin-service (2.2, 2.3), and deploy, Terraform and CI (8, 12, 13). A sample of their claims was re-checked against code.
  - One delegated claim was **corrected**. The payments pass said the RFQ accept route (`POST /rfqs/:rfqId/quotes/:quoteId/accept`, which creates unpaid `cod` or `credit` orders) is not fenced. It is fenced: `/v1/commerce/rfqs` is in `FencedPrefixes` (`commerce-service/internal/http/handler_p0.go:43-45`), and the matcher covers sub-paths (`:247-249`).
  - Treat any other line taken from those passes as read once, by one reader.
- Nothing was executed except `git rev-parse`, `git status`, `git show HEAD:`, `git diff --stat` and read-only searches. No secret file was opened, and no secret value appears below.

Backend paths are relative to `Architecture/services/` unless they start with another top-level folder. Shorthand:
- `C/` = `commerce-service/`
- `H/` = `commerce-service/internal/http/`
- `S/` = `commerce-service/internal/service/`
- `P/` = `commerce-service/internal/store/postgres/`
- `PAY/` = `payments-service/`
- `GW` = `api-gateway/cmd/server/main.go` at HEAD

Android paths are relative to `mobile/android/`. Web paths are relative to `C:\workspace\wt\pb-echo`.

**Shared platform facts re-checked from the sibling maps:**
- **Gateway chain:** the token is verified and `X-User-Id` is re-derived from the JWT; inbound copies are deleted (`GW:854,1076`).
- **Rate limits:** per IP 100 rps, per user 60 rps, with a Redis fleet limiter that fails open (`ratelimit_redis.go:63,77-97`).
- **Body cap:** 10 MiB (`pkg/edgelimits/edgelimits.go:48`).
- **`/internal/` segments:** refused at the edge (`pkg/internalroutes/internalroutes.go:72-79,204-211`).
- **Deploy:** the chart is `charts/atpost-service`, there is no CronJob anywhere, and every worker loop runs inside the server pod.

## 1. Stack and name mapping

### 1.1 Stack

| Layer | Fact | Evidence |
|---|---|---|
| Commerce API, catalogue, cart, checkout, orders, shipping, invoices, coupons, reviews, seller onboarding/KYC, delivery partners | **one Go service, `commerce-service`** (gin, pgx, port 8109); all routes under `/v1/commerce` | `C/cmd/server/main.go:517-580`, `H/handler.go:84+` |
| Database | Postgres database `commerce_db`, **shared with payments-service** (roles `commerce_service`, `payments_service`); payments tables live in schema `payments.` | `infra/terraform/envs/prod/variables.tf:334`, `tools/prodsecrets/manifest.yaml:548,598` |
| Payments | `payments-service` (port 8102): Razorpay adapter, intents, refunds, reconciler, bank offers. Reached only in-cluster, by service token. The PSP webhook comes in on its own ALB ingress | `PAY/cmd/server/main.go`, `PAY/internal/gateway/razorpay_provider.go`, `charts/atpost-service/templates/webhook-ingress.yaml` |
| Money type | integer paise: Go `money.Paise` (`C/internal/money/paise.go`), Android `Paise` (`core/commerce/…/model/Paise.kt`); legacy `NUMERIC(12,2)` columns are still present (section 3) | |
| GST | `C/internal/tax/gst.go` (`Compute` :149, `Allocate` :303, `RefundComponents` :378); interstate means seller state ≠ destination state | `P/checkout.go:497-512` |
| Courier | Shiprocket adapter or a stub, chosen by `COURIER_PROVIDER` | `C/internal/courier/courier.go:125-161`, `shiprocket.go` |
| KYC | **format-only `kyc.StubValidator` in every environment**; no vendor (Karza/Signzy/IDfy) is wired | `C/cmd/server/main.go:358`, `C/internal/kyc/validator.go:1-9,60-69` |
| PII | envelope encryption with AWS KMS; scopes `profile`, `order_snapshot`, `kyc`; dual-write cutovers | `C/internal/pii/*`, `C/internal/kmsclient`, `infra/terraform/modules/commerce-pii-kms/main.tf:84` |
| Blob | `COMMERCE_BLOB_BACKEND=s3` with the invoice bucket; MinIO on dev | `C/internal/store/blob/config.go:22-40` |
| Media (product images, KYC documents) | media-service objects referenced by media id; commerce checks ownership and serves access through `/v1/commerce/internal/media-access` | `C/internal/media/media.go`, `media-service/internal/delivery/authz.go:774-775` |
| Events | outbox `outbox_events` relayed to `social.events.v1` (500 ms poll) | `C/cmd/server/main.go:433-438` |
| Search | no search engine of its own; `GET /products?q=`. search-service builds documents from commerce product events plus IK read-back routes | `H/handler_catalogue.go:525,572-590`, `search-service/internal/events/consumer.go:133,1006` |
| Notifications | notification-service consumes `commerce.order.*` and `commerce.delivery.*` | `notification-service/internal/events/commerce_consumer.go:9-15`, `commerce_delivery.go:14` |
| Admin | admin-service `/v1/admin/commerce/*` and `/v1/admin/payments/*` → admin-token routes; a second, **internal-key-only** admin group inside commerce | `admin-service/internal/http/handler_commerce.go:75-135`, `handler_payments.go:82-118` (HEAD), `H/handler_onboarding.go:36-73` |
| Wallet | **not used** by commerce or payments; Android removed WALLET as a method | `feature/commerce/…/checkout/CheckoutState.kt:193-207` |

### 1.2 Name mapping

| Harness name | Backend | Android | Web |
|---|---|---|---|
| buyer app "MStore" | `/v1/commerce/*`; payments application key `mstore` (`PAY/database/migrations/010_applications.sql:63-66`), reference type `order` | `:feature:commerce`, `MStoreRoute`, `LauncherApp.MSTORE` | `/shop`, `/shop/*` |
| seller app "MSeller" | `sellers` row looked up by user id (`H/sellerresolve.go:34`), `seller` identity role granted by the role worker | `:feature:seller`, `MSellerRoute`, `LauncherApp.MSELLER` | `/shop/sell/*` |
| cart / "bag" | `carts`, `cart_items`, `/cart*` | `BagRoute` | `/shop/bag` |
| wishlist / "favourites" | `commerce_favourites` (024); legacy `wishlists` tables unused | `FavouritesRoute` | `/shop/favourites` |
| checkout | `shipping_quotes` (quote) + `POST /v2/orders/checkout` (P0). Legacy `POST /orders/checkout` is fenced | `CheckoutRoute` | `/shop/checkout` |
| bank offers | payments-service `payment_offers` (014), `GET /v1/commerce/payment-offers` | — | `BankOffers.tsx` |
| local delivery | `commerce_delivery_*` (040-044), `/delivery/*`, behind `COMMERCE_LOCAL_DELIVERY_ENABLED` (off) | `core/commerce-ui/…/delivery/LocalDelivery.kt` | `orders/LocalDeliveryTracking.tsx` |
| sell scope ("books only" etc.) | `045_books_only_sellers.sql`, `/onboarding/step/sell-scope` | `SellScopeRoute` | `SellScope.tsx` |
| try-on | `product_try_on` (034), Banuba Face AR | `TryOnRoute`, `:core:facear` | — |
| RFQ, organizations (B2B), affiliate, bulk import, returns, earnings, payout preview | built but **fenced** (404 before routing) | Android still calls returns and earnings (section 11) | — |

## 2. Route tables

### 2.0 Gateway

- **One prefix covers commerce:** `/v1/commerce` → `COMMERCE_SERVICE_URL` (`GW:764`). The gateway stamps `X-Internal-Service-Key` on every request, anonymous ones included (`pkg/internalroutes/internalroutes.go:489-490`).
- **Retired and forbidden prefixes:**
  - `/v1/shop` and `/v1/orders` are retired (`GW:698-705`).
  - There is no `/v1/cart` or `/v1/seller` prefix.
  - **No `/v1/payments` route exists.** The boot guard refuses to start if one is added (`pkg/routepolicy/routepolicy.go:40-44,76`; `GW:136-154`), and requests get 404 (`GW:441-459`).
- **Not switched off anywhere:**
  - `/v1/commerce` is in no environment's `GATEWAY_DISABLED_PREFIXES` (`deploy/services/api-gateway/values-prod.yaml:124`, qa :127, staging :64).
  - It is not on the dormant-product list (`GW:1365-1405`; commerce is named a launch product at `:1392-1393`).
- **No commerce-specific edge limit** applies.

### 2.1 commerce-service

**Auth words:**

| Word | Meaning | Evidence |
|---|---|---|
| **GT** | gateway-trust key on every `/v1/commerce*` path. It proves the request came through the edge, not who sent it. Exempt: `/v1/commerce/internal/admin*`, health and metrics. A managed ENV refuses to boot without the key | `H/gatewaytrust.go:77-114`, `C/cmd/server/main.go:548-575` |
| **UID** | `X-User-Id` required (401 otherwise) | `H/handler.go:248-256` |
| **SEL** | caller's `sellers` row; 403 `NO_SELLER` if none. There is no token role check | — |
| **IK** | `/v1/commerce/internal/*` with the shared `INTERNAL_SERVICE_KEY`; the actor comes from an unverified `X-User-Id` | `H/internal_guard.go:42-74` |
| **AT(perm)** | admin-service Ed25519 token, aud `commerce`. **Every AT route answers 401 today**, because `SERVICE_CALLERS` is commented out in every committed values file | `H/admin_token.go:121-171`; `deploy/services/commerce-service/values-prod.yaml:158-160` |
| **Fence** | 404 before routing, for the prefixes `/rfqs`, `/seller/rfqs`, `/organizations`, `/seller/bulk-import`, `/affiliate`, `/payout`, `/seller/cod-remittances`, `/seller/earnings`, `/returns`, `/seller/returns`, `/me/returns` and the exact routes `POST /orders/checkout`, `POST /orders/:id/returns`, `POST /orders/:id/payment/confirm` (unless stub) | `H/handler_p0.go:43-59,85-120,213-234,247-249` |

**Exposure and rate limits:**
- **E** = reachable at the edge. **IC** = in-cluster only. **E-fenced** = reachable at the edge but answers 404.
- **Rate limit:** unless stated, only the edge limits apply. commerce-service has in-process, per-pod limits on engagement routes only.
- Path prefix `/v1/commerce` is omitted below.

#### Catalogue, browse, search (public)

| Method | Path | Handler | Caller / scoping | Exposure |
|---|---|---|---|---|
| GET | `/categories`, `/categories/:id/attribute-schema`, `/tax-classes`, `/home` | `H/handler_catalogue.go:42`, `H/handler_attributes.go:49`, `H/handler_inventory.go:172`, `H/handler_storefront.go:148` | public (GT); `/home` uses an optional UID | E |
| GET | `/products` (also `?q=` search) | `H/handler_catalogue.go:525` | public | E |
| GET | `/products/:id`, `/preview`, `/media`, `/attributes`, `/variants`, `/reviews`, `/delivery-estimate` | `H/handler_catalogue.go:71,427,488`, `H/affiliate_handler.go:78`, `H/handler_variants.go:25`, `H/handler_reviews.go:19`, `H/handler_engagement.go:139` | public; `productReadable` returns 404 for a product that is not live, except to its owner (`H/handler.go:288`) | E |
| GET | `/variants/:id/price-tiers` | `H/handler_variants.go:278` | public | E |
| GET | `/sellers/:sellerId/products` | `H/handler_seller.go:248` | public, `publicOnly` (`S/catalogue_products.go:328`) | E |
| GET | `/serviceability` | `H/handler_orders.go:50` | UID | E |
| GET | `/payment-offers?amount_minor=` | `H/handler_coupons_offers.go:69` | public; the amount is display-only | E |

#### Seller catalogue and inventory (SEL; the product's or variant's seller must equal the caller's)

| Method | Path | Handler | Ownership evidence | Exposure |
|---|---|---|---|---|
| POST | `/products` | `H/handler_catalogue.go:254` | `sellerForCaller` :265 | E |
| PATCH | `/products/:id` | `H/handler_products.go:184` | `S/productwrite.go:620-626` | E |
| POST, PUT, DELETE | `/products/:id/media`, `/media/order`, `/media/:mediaId` | `H/handler_catalogue.go:367`, `H/handler_storefront.go:184,209` | `galleryEditGate` (`S/reviewedcontent.go:318`); media ownership (`S/catalogue_products.go:639`) | E |
| PUT | `/products/:id/attributes`; PUT, DELETE `/try-on` | `H/handler_catalogue.go:455`, `H/handler_try_on.go:49,83` | `assertProductSeller` (`S/catalogue_products.go:603-613`) | E |
| POST | `/products/:id/variants`; PATCH, DELETE `/variants/:id`; PUT `/variants/:id/price-tiers` | `H/handler_variants.go:90,150,206,236` | `S/catalogue_products.go:444,537,588`, `S/pricing.go:71` | E |
| POST | `/products/:id/submit`; GET `/readiness`, `/seller/action-needed` | `H/handler_onboarding.go:390`, `H/handler_submissions.go:65,86` | `S/submitgate.go:413-425,500-512` | E |
| GET | `/seller/products`, `/seller/variants/:id` | `H/handler_inventory.go:135,188` | `P/inventory_adjust.go:344-368` | E |
| GET, PATCH | `/seller/variants/:id/stock` | `H/handler_inventory.go:102,39` | `P/inventory_adjust.go:286-302`; the idempotency key is scoped per seller (`:117,268`) | E |

#### Cart, coupons, addresses (buyer)

| Method | Path | Handler | Scoping | Exposure |
|---|---|---|---|---|
| GET, POST, PATCH, DELETE | `/cart`, `/cart/items`, `/cart/items/by-variant/:variantId`, `/cart/items/:variantId` | `H/handler_cart.go:18,36,94,68` | UID; cart keyed on the user (`P/cart_items.go:18,84`) | E |
| GET | `/cart/coupon-preview` (legacy `svc.Quote`), `/cart/coupons` | `H/handler_orders.go:96`, `H/handler_coupons_offers.go:83` | UID | E |
| GET, POST, PATCH | `/seller/coupons`, `/seller/coupons/:couponId` | `H/handler_coupons_offers.go:234,248,270` | SEL + approved; PATCH scoped by seller (`P/coupons.go:1322`) | E |
| GET, POST | `/addresses` | `H/handler_addresses.go:17,72` | UID | E |
| PATCH, DELETE, POST | `/addresses/:id`, `/:id/default` | `H/handler_addresses.go:117,152,168` | `WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL` (`P/addresses.go:125,164,206`) | E |
| PUT | `/seller/address` | `H/handler_p0.go:872` | SEL | E |

#### Checkout, orders, payment (buyer)

| Method | Path | Handler | Scoping and money | Exposure |
|---|---|---|---|---|
| POST | `/checkout/quote` (body: `address_id`, `coupon_code`, `payment_method`) | `H/handler_p0.go:303` | UID; address must be the caller's (`S/checkout_p0.go:527-532`); server-priced | E |
| POST | `/v2/orders/checkout` (body: `address_id`, `quote_id`, `payment_method`, `coupon_code`, `terms_version`, `expected_total_minor`) | `H/handler_p0.go:356` (fields :373-378) | UID; quote locked `WHERE id=$1 AND user_id=$2` (`P/quote.go:118`). `Idempotency-Key` is required (:364-370); same key with a different body gives 409 (:621-626; `P/checkout.go:759-788`). `expected_total_minor` > 0 is compared only, giving `PRICE_CHANGED` (:424-430). Single seller per cart (`MULTIPLE_SELLERS`, :649) | E |
| GET | `/orders` | `H/handler_orders.go:187` | `WHERE o.customer_user_id=$1` (`P/orders.go:257`) | E |
| GET | `/orders/:id`, `/orders/:id/items` | `H/handler_orders.go:216,236` | **unscoped read** `WHERE id=$1` (`P/orders.go:137`), then a Go owner check (`S/orders_read.go:134,467`) | E |
| POST | `/orders/:id/cancel` | `H/handler_orders.go:257` | `FOR UPDATE` + customer check (`P/p0.go:695-706`); writes a durable refund command | E |
| POST | `/orders/:id/payment/intent` | `H/handler_p0.go:455` | owner; amount = `OrderTotalMinor` (`S/checkout_p0.go:433,465`) | E |
| GET | `/orders/:id/payment/status`, `/orders/:id/payment` | `H/handler_p0.go:494,521` | owner; `/payment` answers **403, not 404**, to a stranger (`S/customer_payment.go:109`) | E |
| POST | `/orders/:id/payment/confirm` | `H/handler_orders.go:299` (reg `H/handler.go:180-182`) | registered only when `PAYMENTS_ALLOW_STUB`, which refuses boot outside a local ENV (`C/cmd/server/main.go:314-318,738-756`); client `amount_minor` compared only (`S/checkout.go:307`) | E-fenced |
| POST | `/orders/:id/approve`, `/reject` (B2B org approval) | `H/handler_organizations.go:305,324` (reg :29-30) | org role (`S/organizations.go:276,310`); **not covered by the `/organizations` fence** | E |

#### Shipping, tracking, invoices, seller fulfilment

| Method | Path | Handler | Scoping | Exposure |
|---|---|---|---|---|
| POST | `/orders/:id/shipment` | `H/shipments.go:78` | caller must be **a** seller on the order, then books **every** seller's shipments (`S/order_lifecycle.go:51-79`, `S/shipments.go:161-169`) | E |
| GET | `/orders/:id/shipment`, `/shipments` | `H/shipments.go:128,147` | customer or any seller on the order | E |
| POST, GET | `/orders/:id/invoice` | `H/shipments.go:183,201` | POST: any seller on the order; GET: customer or seller | E |
| GET | `/seller/orders`, `/seller/orders/:id`, `/history`, `/seller/fulfillment` | `H/handler_seller.go:169,219,195`, `H/handler_seller_fulfilment.go:167` | SEL; `WHERE oi.seller_id=$1` (`P/orders.go:325`); detail filtered to own lines (`S/orders_read.go:295-297`) | E |
| POST | `/seller/orders/:id/pack`, `/cancel`, `/ship`, `/mark-delivered` | `H/handler_seller_fulfilment.go:89,107,129,145` | SEL; refuses an order shared with other sellers | E |
| POST | `/shipments/webhooks/:courier`, `/shipments/courier-callback` | `H/shipments.go:220,251` | anonymous plus GT. Courier name must equal the configured one, then `VerifyWebhook`. Shiprocket: constant-time token or HMAC-SHA256 against `SHIPROCKET_WEBHOOK_TOKEN` / `_HMAC`, rejecting when neither is set (`courier/shiprocket.go:64-89`). **The stub accepts anything** (`courier/courier.go:161`) but parses no updates; the 401 body echoes `err.Error()` (`H/shipments.go:235`) | E |

#### Returns, payouts, B2B (fenced)

`/orders/:id/returns`, `/returns/:id[/approve|/reject|/refund-preview]`, `/seller/returns`, `/me/returns` (`H/handler_returns.go:42-209`, `H/handler_seller.go:334`); `/payout/preview` (float maths on client `gross` and percentages, `H/handler_seller.go:356`); `/seller/cod-remittances`, `/seller/earnings[.csv]`; `/seller/bulk-import/**`, `/rfqs/**`, `/organizations/**`, `/affiliate/:id`. All are E-fenced.

#### Reviews and engagement

| Method | Path | Handler | Scoping / limit | Exposure |
|---|---|---|---|---|
| POST | `/products/:id/reviews` | `H/handler_reviews.go:49` | UID; the order item must be on the caller's own delivered order (`S/reviews.go:30-46`) | E |
| POST | `/reviews/:id/seller-response` | `H/handler_reviews.go:98` | SEL + `checkReviewSeller` (`S/reviews.go:76`) | E |
| PUT, DELETE | `/products/:id/reaction`, `/reviews/:id/vote` | `H/handler_engagement.go:191,223,313,337` | UID; 60/min per user, in process | E |
| POST | `/products/:id/share` | `H/handler_engagement.go:254` | anonymous allowed; 20/min per `u:<uid>` or `ip:<ClientIP>`. gin sets no trusted proxies (`C/cmd/server/main.go:517`) | E |
| GET, POST, DELETE | `/favourites[/:productId]` | `H/handler_storefront.go:239,259,278` | UID (`P/storefront.go:378`) | E |

#### Seller onboarding, KYC, payout account, dashboard (UID, scoped by `user_id`, e.g. `P/onboarding.go:70,139,153,283`)

`POST /onboarding/start`, `GET /onboarding/status`, `PUT /onboarding/step/{basic,sell-scope,storefront,documents,fulfillment,payout}`, `POST /onboarding/submit`, `GET /onboarding/readiness`, `GET /dashboard`, `GET /seller/sell-scope`, `POST /seller/sell-scope/upgrade`, `POST /sellers/onboard`, `GET /sellers/me`. Handlers are at `H/handler_onboarding.go:118-357`, `H/handler_sellscope.go:45-84`, `H/handler_inventory.go:219` and `H/handler_seller.go:38,79`. All are E.

KYC media ids must be owned by the caller, checked through media-service; this is mandatory in managed ENVs (`C/cmd/server/main.go:207-215`).

#### Delivery partners (`/delivery/*`; UID group, `no-store`; feature off unless `COMMERCE_LOCAL_DELIVERY_ENABLED`)

| Routes | Handler | Scoping |
|---|---|---|
| profile, documents, submit, coverage, availability, location, offers (accept, decline), jobs (current, pickup, deliver, failed, support), earnings | `H/handler_delivery.go:121-170`, `H/handler_delivery_coverage.go:10`, `H/handler_delivery_operations.go:60-199` | scoped by `user_id` or `partner_user_id`. Exception: `GET /delivery/jobs/:jobId[/contact]` reads unscoped (`P/delivery_operations.go:103`), then checks in Go (`S/delivery_operations.go:129,178`) |
| `PUT /delivery/orders/:id/location`, `GET /delivery/orders/:id/jobs`, `PUT/GET /delivery/sellers/:id/location`, `POST /delivery/seller/jobs/:id/ready`, `GET /delivery/customer/jobs/:id/code` | `H/handler_delivery_operations.go:223-330` | `customer_user_id=$2` or `sellers WHERE id=$1 AND user_id=$2` (`P/delivery_operations.go:28,182,191,323,349,880`) |

#### Internal (IC only)

- **IK onboarding group** (`H/handler_onboarding.go:37-103`): sellers queue, get, approve, reject, request-changes, suspend, unsuspend and **kyc-verify**; `payouts/pending`; `jobs/dead-letter`; products queue, approve, reject and request-changes; search docs and facets (`H/handler_searchdoc.go:41,69,117`); **`cod-remittances/:id/settle`** (`H/handler_seller.go:106`); and attribute, category and schema authoring (`H/handler_attributes.go:74-414`).
- **IK storefront group:** `POST /internal/media-access[/batch]`, where the viewer comes from the body (`H/handler_storefront.go:317,344`), and banners CRUD (`:370-425`).
- **IK submissions group:** submissions and compliance gaps, including sweep (`H/handler_submissions.go:50-55`).
- **AT group** (`/internal/admin/*`, `H/admin_token.go:197-250`): mirrors the IK group, plus `/stats`, seller documents list and image (`H/handler_seller_documents.go:37,63`), coupons (`commerce:coupons.manage`, `H/handler_coupons_offers.go:56-58`), and delivery profiles, areas and jobs. Jobs and areas are gated by `commerce:catalogue.edit` (`H/handler_delivery_operations.go:33-39`, `H/handler_delivery.go:31-42`).

#### Health

`/livez`, `/healthz`, `/readyz`, `/metrics` (`C/cmd/server/main.go:577-578`); not proxied.

### 2.2 payments-service (never exposed by the gateway; `PAY/internal/http/handler.go`)

| Method | Path | Handler | Caller | Auth |
|---|---|---|---|---|
| POST | `/v1/payments/webhook` | `:189` → `HandleWebhook :1150` (1 MiB cap :77) | Razorpay, via ALB `webhookIngress` (prod `api.cleestudio.com`, qa `api-qa.cleestudio.com`; not in staging or Azure) | HMAC-SHA256 over the raw body, hex, `hmac.Equal`, header `X-Razorpay-Signature`, secret `RAZORPAY_WEBHOOK_SECRET`; **fails closed** with no secret (`PAY/internal/gateway/razorpay_provider.go:167-190`) |
| POST, GET | `/v1/payments/internal/intents`, `/:id`, list | `:236-238` → `:674,805,1101` | commerce (token `payments:intent.create` / `intent.read`; application allowlist :753; owner domain `ownsIntent :908`) | Ed25519 service token |
| POST | `/v1/payments/internal/intents/:id/verify` | `:239` → `:1070` | commerce | `intent.read`; advisory, except **in stub mode, where it settles** (`PAY/internal/service/service.go:785-816`) |
| POST | `/v1/payments/internal/intents/:id/refund` | `:240` → `:965` | commerce | `payments:refund.create`; durable command, 202. A token caller must send an amount and an idempotency key; a legacy key defaults to the remaining balance (:997-1023) |
| GET, POST | `/v1/payments/internal/refunds/needs-attention`, `/:commandId/resolve` | `refund_admin.go:71,119` | operator | `payments:refund.admin`; operator from `X-User-Id` |
| GET, PUT | `/v1/payments/internal/applications[/:id[/transactions]]` | `applications.go:213-346` | service/operator | `intent.read` / `OpApplicationAdmin` |
| GET | `/v1/payments/internal/offers` | `offers.go:58` | commerce | `payments:offers.read`. **Prod `SERVICE_CALLER_COMMERCE_SERVICE_OPS` lacks it** (`values-prod.yaml:107`) |
| POST, GET | `/v1/payments/intents[/:id]`, `/holds/:id/release` (user family) | `:216-219` | system with internal key + `X-User-Id` | intents refused in prod (:286); hold creation unreachable |
| PATCH, POST | `…/intents/:id/status`, `…/intents/:id/refund` (legacy) | `:221,225,266` | — | 410 Gone |
| all | `/v1/payments/internal/admin/*` (stats, refunds, intents, reconciliation, applications, audit, offers) | `admin_token.go:204-219` (HEAD), `offers.go:92-94` | admin-service | admin token only; **401 everywhere today** (admin-service not in `SERVICE_CALLERS`) |

There is no rate limiter in the service (middleware `PAY/cmd/server/main.go:302-307`). The dedicated `psp-webhook` WAF ACL is not wired (`infra/terraform/envs/prod/README.md:137`). COD is refused at intent creation with `COD_NOT_SUPPORTED` (`handler.go:790-802`).

### 2.3 admin-service (HEAD; `/v1/admin/commerce` → commerce `/internal/admin`, `/v1/admin/payments` → payments `/internal/admin`)

The actor, MFA and step-up claims come from gateway-stamped headers (`admin-service/internal/http/gate.go:344,359,427-429`).

| Route group | Permission | Step-up | Two-person |
|---|---|---|---|
| sellers queue and get | `commerce:sellers.read` | no | no |
| sellers approve, reject, request-changes | `commerce:seller.approve` | **no** | no |
| sellers suspend, unsuspend | `commerce:seller.suspend` | yes | no |
| sellers `kyc/verify`, document view | `commerce:kyc.verify` | yes | no |
| products queue, approve, reject | `commerce:products.moderate` | **no** | no |
| `payouts/pending` | `commerce:payouts.read` | yes | no |
| `cod-remittances/:id/settle` | `commerce:cod.settle` | yes | **yes** (`handler_commerce.go:151-169`) |
| coupons GET, POST, PATCH | `commerce:coupons.manage` | writes | no |
| delivery, banners, compliance, stats, catalogue | various | partly | no |
| payments refunds resolve | `payments:refund.issue` | yes | yes for `refunded_manually` and `written_off`; **not for `test_data`** (`handler_payments.go:347-357`) |
| payments applications PATCH, offers POST and PATCH | `payments:applications.manage` / `offers.manage` | yes | no |

**No admin route exists** for disputes, order refunds, returns, order cancel on behalf of a buyer, or executing seller payouts.

## 3. Tables and migrations

**commerce-service** (`C/database/`):
- Base schema is `setup.sql` (51 tables).
- Boot migrations `migrations/001-055` apply at server start (BootstrapSchema).
- Gated migrations `gated/998-1002` run only through `commerce-migrate -gated` (`C/cmd/migrate/main.go:13,43,95-117`). **That binary is not built into the image** (`C/Dockerfile:20-25`), and the chart's migration Job is disabled (`values-prod.yaml:176-179`).

**Money columns.** Legend: **N** = legacy `NUMERIC`, still present and never dropped; **M** = `BIGINT` paise (`*_minor`).

| Table | Created / altered | Money (type) | Notes |
|---|---|---|---|
| `orders` | setup.sql:430; 007, 008, 010, 038, 054, 055 | N `subtotal, discount_amount, shipping_charges, tax_amount, coupon_discount, final_amount` (setup:434-440); M `subtotal_minor, discount_amount_minor, shipping_charges_minor, tax_amount_minor, coupon_discount_minor, final_amount_minor` (007:19-49); M `captured_minor, offer_discount_minor, platform_discount_minor` (038:126) | `place_of_supply_state` (010:36). NOT NULL on `*_minor` only after gated 999:78-80. Status CHECK `orders_status_check_v2` (010:143-150); `payment_status` CHECK (setup:444); `payment_method` prepaid-only CHECK **only in gated 998** |
| `order_items` | setup:468; 007, 010 | N `unit_mrp, unit_price, discount_amount, tax_amount, final_price`; M `unit_mrp_minor, unit_price_minor, discount_amount_minor, tax_amount_minor, final_price_minor`; GST M `taxable_minor, cgst_minor, sgst_minor, igst_minor, allocated_discount_minor, allocated_shipping_minor, net_inclusive_minor` (010:16-33) | `hsn_code`, `tax_class_id` (010:18); `chk_item_tax_reconciles` NOT VALID until 999 |
| `product_variants` | setup:208; 007 | N `mrp, selling_price, cost_price`; M `mrp_minor, selling_price_minor, cost_price_minor` | |
| `cart_items` | setup:362; 007, 008 | N `price_snapshot`; M `price_snapshot_minor` | `carts.version` trigger (008:72) |
| `coupons`, `coupon_usages` | setup:320,343; 007, 038 | N `discount_value, max_discount_amount, min_order_amount`; M `discount_value_minor, max_discount_amount_minor, min_order_amount_minor`; `funded_by` (seller or platform) (038:62-111) | `chk_coupon_uses_within_max` NOT VALID until 999 |
| `shipping_quotes` | 008:112; 016 | M `shipping_minor`, `subtotal_minor, discount_minor, tax_minor, total_minor` (016) | the quote the buyer accepts |
| `payment_event_inbox` | 008:167 | M `amount_minor` | commerce dedupe of payment events |
| `order_refund_commands` | 008:187 | M `amount_minor > 0`; status `pending, submitted, succeeded, failed, needs_attention` (008:197) | durable refund outbox to payments |
| `order_refund_settlements` | 048:19 | M `amount_minor` | |
| `order_owed_captures`, `order_owed_capture_refunds` | 055:72,118 | M `amount_minor, refund_due_minor, refunded_minor` | late, duplicate and mismatched captures |
| `refunds`, `payments` (legacy) | setup:510,533; 007 | N `amount`; refunds also gets M `amount_minor` | pre-P0, superseded by payments-service |
| `invoices`, `invoice_sequences` | 002:4-11; 039 (reissue audit) | **N only:** `grand_total, cgst_total, sgst_total, igst_total` (002:19-24) | no `_minor` mirror |
| `shipments`, `shipment_events`, `cod_collections` | 002:35-77 | N `cod_collections.amount` | |
| `cod_remittances` | 004:13 | **N only:** `gross_amount, commission_amount, platform_fee`; status `pending, settled, on_hold` | created on COD delivery, though COD cannot be ordered |
| `payout_batches`, `payout_transactions`, `commission_rules` | setup:690-771 | N everywhere | `CreatePayoutBatch` has no caller (`P/seller_payouts.go:183`) |
| `seller_payout_accounts` | setup:71; 021, 035 | — | **bank account:** `account_number_enc BYTEA`, `_last4`, `_hash` (salted HMAC), `pii_key_version` (035:39-49). Plaintext `account_number` is cleared by gated 1002. IFSC, holder and bank name stay plaintext |
| `sellers` | setup:9; 019, 022, 035, 045 | — | `pan_enc`, `pan_masked`, `pan_hash` (035); GSTIN plaintext by design (035 header); `verification_status` `pending, verified, rejected, suspended` (setup:26-27) |
| `seller_documents` | 001:26; 014, 035 | — | trigger `trg_seller_documents_no_raw_aadhaar` (035:313) |
| `organizations` | setup:927; 035 | N `approval_threshold, credit_limit` | `pan_enc` (035) |
| `inventory_items`, `inventory_reservations`, `inventory_ledger`, `inventory_adjustments` | setup:256-286; 009, 051, 052, 053 | — | `chk_inv_reserved_le_total` NOT VALID (009:92; enforced on new writes, validated in 999:61) |
| `product_price_tiers`, `rfq_quotes` | setup:858,904 | N `price`, `quoted_total` | fenced |
| `product_offers` | 027:50 | — | per-seller offer on a catalogue product |
| `order_status_transitions` | 010:81; +033, +054 | — | the transition matrix (section 10) |
| `pii_key_ring`, `pii_backfill_progress`, `pii_cutover_state`, `pii_kyc_*` | 011, 015, 017, 035 | — | PII cutover state |
| `commerce_admin_audit_log` | 036:32 | — | append-only trigger (036:62) |
| `commerce_delivery_*` (12 tables) | 040-044 | `commerce_delivery_earnings` (type not read) | local delivery |
| others | `product_categories` (+023 seed), `attribute_*` (025-026), `product_variation_axes` (028), `product_submissions`, `product_compliance_gaps` (029), `identity_role_intents` (032), `product_try_on` (034), `product_reactions` (037), `commerce_banners`, `commerce_favourites` (024), `outbox_events` (005, 050), `fenced_legacy_orders` (012) | | |

**Gated set** (`C/database/gated/`):

| File | What it does |
|---|---|
| `998_contract_triggers_and_fences.sql` | attaches `trg_order_transition` (:74), the `trg_fence_*` triggers (:135-162) and the prepaid-only `payment_method` CHECK |
| `999_validate_and_tighten.sql` | validates the NOT VALID constraints (:61-65) and sets `*_minor` NOT NULL (:78-84); refuses while `money_minor_drift` is non-empty |
| `1000_pii_plaintext_scrub.sql` | address plaintext scrub |
| `1001_approval_status_tighten.sql` | approval status tighten |
| `1002_kyc_pii_plaintext_scrub.sql` | KYC plaintext scrub; refuses until the cutover state is `ciphertext` everywhere and a drain is recorded |

**payments-service** (`PAY/database/migrations/001-019`, gated `998_application_id_not_null`, `999_provider_reference_uniqueness`):
- **Gated set:** no migrate binary exists; the runbook says to run it by hand with psql (`docs/runbooks/payments-applications.md:153`).
- **`payment_intents`:** N `amount NUMERIC(12,2)` is a deprecated mirror (001:16). M `amount_minor` (006) is the truth. Other M columns: `refunded_amount_minor`, `refund_reserved_minor` (CHECK `refunded + reserved <= amount`, 007:191), `captured_minor`, `offer_discount_minor`, `refunded_captured_minor` (014; CHECK `captured + discount = amount`). `currency TEXT DEFAULT 'INR'`.
- **Other M tables:** `refund_commands` (007), `refunds_applied`, `provider_refunds_applied`, `refund_required`, `refund_required_refunds` (018), `payment_holds.hold_amount` (002), `payment_offers` (`discount_value`, `max_discount_minor`, `min_amount_minor`; 014).
- **Webhook dedupe:** `provider_events` PK `(provider, event_id)` (007:74-89). The legacy `webhook_events` (003) is unused.
- **`applications`** (010:37-56): keys `mstore, feast, dating, mopedu, doorstep, memberships, premium`.

## 4. Kafka topics

All commerce and payments traffic uses **`social.events.v1`** and `social.events.v1.dlq`. Both are in MSK (`infra/terraform/modules/msk/topics-job/variables.tf:32-71`), auto-create is off (`modules/msk/main.tf:147`), and `scripts/ci/kafka-topics-check.sh` runs in `go-ci.yml:131-132`.

| Event types | Producer | Consumers (group) |
|---|---|---|
| `commerce.order.{created,paid,payment_failed,cancelled,shipped,delivered,refunded,partially_refunded,capture_refused,late_capture_refunded,owed_payment_refunded}`, `commerce.inventory.released` | commerce outbox (`C/cmd/server/main.go:433-438`; e.g. `P/p0.go:303`) | notification-service: buyer order_confirmed / shipped / delivered / cancelled / refunded / payment_failed, and seller_new_order on paid (`notification-service/internal/events/commerce_consumer.go:9-47`); `created` is deliberately silent |
| `commerce.order.duplicate_capture` | commerce | payments `payments-commerce` (`PAY/internal/consumers/commerce.go:60-62`); acts only when `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` is on (off in every deployed ENV) |
| `commerce.product.{created,updated,submitted,published,unpublished}` | commerce | search-service (`search-service/internal/events/consumer.go:133,229,1006`) |
| `commerce.seller.{registered,kyc_verified,kyc_persist_failed,sell_scope_upgraded}`, `commerce.review.created`, `commerce.return.*`, `commerce.rfq.*`, `commerce.organization.*`, `commerce.org.*` | commerce | consumers not traced (returns, RFQ and orgs are fenced) |
| `commerce.delivery.*` | commerce (`P/delivery_operations.go:377`) | notification-service (`commerce_delivery.go:14`) |
| `payment.{initiated,succeeded,failed,refund_pending,refunded,refund_failed,owed_capture}` | payments outbox (`PAY/cmd/server/main.go:324-330`; partition key `reference_id`) | commerce `commerce-payments` (`C/internal/consumers/payments_p0.go:91-121`; handles succeeded, failed, refunded, refund_failed, owed_capture; ignores any reference type other than `order` :351); food `food-payments`; dating, rider, doorstep, monetization groups |

The legacy commerce `NewPaymentsConsumer` (`C/internal/consumers/payments.go:43`) is defined but **not started**.

**Trust note:** `social.events.v1` is shared by many producers. Commerce trusts `payment.succeeded` by type, then binds it to the stored intent, amount, currency and payer (section 10).

## 5. Caches and Redis keys

| Key | TTL | Owner | Evidence |
|---|---|---|---|
| `counter:product_view_count:<productId>:<shard>` (32 shards) + dirty set; flushed to `products.view_count` about every 10 s | none (flushed) | commerce | `S/service.go:203`, `shared/counters/counters.go:77`, `C/cmd/server/main.go:484-494` |
| gateway `rl:gw:ip:*`, `rl:gw:user:*` | bucket | gateway | `api-gateway/cmd/server/ratelimit_redis.go:63-97` |
| engagement limits (reaction, vote, share) | 1 min / 10 min | **in process, per pod** | `H/handler_engagement.go:45-114` |

**Nothing else in commerce uses Redis.**
- The cart, quote, idempotency, inventory and payment dedupe all live in Postgres.
- The old Redis SETNX payment dedupe was replaced by `payment_event_inbox` (`C/cmd/server/main.go:380-383`).
- `REDIS_ADDR` is still a boot dependency (`main.go:97`).
- payments-service uses no Redis.

## 6. Storage buckets

| Item | Fact | Evidence |
|---|---|---|
| Invoices | bucket module `bucket_commerce_invoices`: versioned, noncurrent versions kept 365 d, IA at 90 d, SSE-S3 (prod `infra/terraform/envs/prod/buckets.tf:29-37`, qa `:22-27`). Env `COMMERCE_BLOB_BACKEND=s3`, `COMMERCE_BLOB_BUCKET=__TF_COMMERCE_INVOICES_BUCKET_NAME__` (prod and qa only). Keys `invoices/<FY>/<number>.html`, plus `.pdf` best-effort via wkhtmltopdf. Download is a presigned URL with a **7-day TTL** | `S/shipments.go:654-689` |
| Bulk-import error files | same bucket, presigned 15 min; the feature is fenced | `S/bulk_import.go:168`, `S/shipments.go:863` |
| Product images, KYC documents, delivery-partner documents, review media | **no commerce bucket.** These are media-service objects (single media bucket, `infra/terraform/modules/media`). commerce verifies ownership and authorises viewing via `/internal/media-access`; KYC document bytes come through `C/internal/media/documentbytes.go` to the admin AT route | `C/internal/media/*`, `H/handler_seller_documents.go:37,63` |
| KYC | no separate bucket and no separate Terraform module. `kyc` is a **scope** on the commerce PII CMK (`modules/commerce-pii-kms/main.tf:84,150-170`), wired in prod and qa (`envs/prod/main.tf:233-241`) | — |
| Staging | no invoice bucket, no PII CMK, IRSA is MSK-only (`envs/staging/services-irsa.tf:29-30`) | — |

## 7. Third parties

| Party | Config keys (names only) | Mode per environment | Evidence |
|---|---|---|---|
| Razorpay (payments) | `RAZORPAY_KEY_ID`, `RAZORPAY_KEY_SECRET`, `RAZORPAY_WEBHOOK_SECRET`; `PAYMENTS_ALLOW_STUB` | **prod is seeded with the TEST key pair** until live approval and tax sign-off; live is a secret rotation, and code cannot tell test from live (base URL `https://api.razorpay.com/v1`). qa is permanently test, staging test, Azure none. The stub (`order_stub_`, signature always true) is refused when `ENV=prod` | `deploy/services/payments-service/values-prod.yaml:6-12` (HEAD), `PAY/internal/gateway/razorpay.go:17`, `stub.go:31`, `PAY/cmd/server/main.go:139-142` |
| Razorpay on clients | Android: `key_id` and `order_id` come from the server `client_session` (`core/payments/…/PaymentSession.kt:97-143`, `RazorpayPaymentLauncher.kt:117-145`). Web: build args `NEXT_PUBLIC_RAZORPAY_KEY_ID`, `NEXT_PUBLIC_ENABLE_STUB_PAYMENTS` | neither client marks an order paid; both poll server status | `src/features/shop/checkout/startPayment.ts:5-48` |
| Cashfree | adapter present, **not wired** | — | `PAY/internal/gateway/cashfree_provider.go` |
| Razorpay Offers (bank offers) | `PAYMENTS_OFFERS_ENABLED` | off everywhere ("dev only") | `values-prod.yaml:135` |
| Shiprocket (courier) | `COURIER_PROVIDER`, `SHIPROCKET_EMAIL`, `SHIPROCKET_PASSWORD`, `SHIPROCKET_WEBHOOK_TOKEN`, `SHIPROCKET_WEBHOOK_HMAC`, `SHIPROCKET_PICKUP_LOCATION`; dev only `COURIER_STUB_AUTO_DELIVER_AFTER` | prod: provider **from the secret**, and the code does not refuse the stub in prod. qa: `stub`. staging: `shiprocket`. Azure: unset, so stub. The stub returns fake AWBs (`https://example.com/labels/<awb>.pdf`) | `C/internal/courier/courier.go:125-161`; values (HEAD) prod :80-89, qa :142, staging :85 |
| KYC verification vendor | none | **stub (format and regex) everywhere**; reports carry `Source: "stub"` | `C/cmd/server/main.go:358`, `C/internal/kyc/validator.go` |
| GST / e-invoice (IRP), TDS/TCS filing | none; GST computed in-house; `COMMERCE_TDS_PCT`, `COMMERCE_COMMISSION_PCT`, `COMMERCE_PLATFORM_FEE_PCT` unset | no e-invoice IRN or QR integration found | `C/internal/tax/gst.go` |
| AWS KMS | `COMMERCE_KMS_KEY_ID`, `COMMERCE_PII_LOOKUP_SALT`; dev `COMMERCE_PII_DEV_KEY_{PROFILE,SNAPSHOT,KYC}` | prod and qa CMK; staging none; Azure none | `C/cmd/server/main.go:806-864` |
| identity-auth (seller role grant) | `IDENTITY_AUTH_URL` / `AUTH_SERVICE_URL`, internal key | — | `C/cmd/server/main.go:440-462` |
| Seller payout rail | **none for commerce** (monetization's RazorpayX rail serves creators only) | payouts are never executed | `monetization-service/internal/service/payout_rail.go` |

## 8. Feature flags and env config per environment

All values were read at HEAD. P = prod, Q = qa, S = staging, AP = azure-prod, AS = azure-staging. No `values-dev.yaml` exists; dev runs from `Architecture/docker/docker-compose.yml`.

| Setting | P | Q | S | AP | AS |
|---|---|---|---|---|---|
| commerce `ENV` | prod | **prod** | staging | prod | staging |
| `COMMERCE_PLATFORM_COUPONS_ENABLED` | false | false | unset (off) | unset | unset |
| `COMMERCE_PRODUCT_AUTO_APPROVE` | false | false | unset | unset | unset |
| `COMMERCE_KYC_REQUIRE_DOCUMENT_KIND` | false | false | false | false | false |
| `COMMERCE_PII_CUTOVER` / `COMMERCE_KYC_PII_CUTOVER` | dual / dual | dual / dual | dual / dual | unset | unset |
| `COMMERCE_LOCAL_DELIVERY_ENABLED` | unset (off) | off | off | off | off |
| `COURIER_PROVIDER` | secret | `stub` | `shiprocket` | unset = stub | unset |
| `COMMERCE_PAYMENT_METHODS` (doc only) | `upi,card` | same | same | — | — |
| `PAYMENTS_ALLOW_STUB` (commerce, payments) | absent | absent | absent | absent | absent |
| commerce `SERVICE_CALLERS` (admin console) | **commented out** | commented | commented | commented | commented |
| commerce blob, KMS, `MEDIA_SERVICE_URL`, `POSTGRES_DSN` | set | set | partly (no blob or KMS) | **absent: cannot boot** | absent |
| payments `SERVICE_CALLERS` | `commerce-service,food-service` | same | same | **absent: refuses boot** | absent |
| `SERVICE_CALLER_COMMERCE_SERVICE_OPS` | `intent.create, intent.read, refund.create, payment.fetch` (no `offers.read`) | same | same | — | — |
| `PAYMENTS_OFFERS_ENABLED` | off | off | off | off | off |
| `PAYMENTS_REFUND_REQUIRED_WORKER`, `PAYMENTS_DUPLICATE_CAPTURE_CONSUMER` | **unset = OFF** (on only for dev, local or blank ENV; `PAY/internal/config/config.go:56-71,172-190`) | OFF | OFF | OFF | OFF |
| `PAYMENTS_RECONCILE_INTERVAL_SEC` / `PENDING_AGE_SEC` | 60 / 600 | 60 / 600 | 60 / **300** | default | default |
| payments `webhookIngress` + NetworkPolicy | on | on | **off** | off | off |
| gateway `GATEWAY_DISABLED_PREFIXES` | wallet, billpay, food and others; **not commerce** | same | no wallet or billpay | **unset** | unset |
| web `NEXT_PUBLIC_ENABLE_STUB_PAYMENTS` | "false" (build arg) | — | — | — | — |

**Launch switches holding payments or products back:**
- Razorpay test keys in prod.
- Platform coupons off.
- Product auto-approve off.
- Bank offers off.
- Local delivery off.
- Returns, payouts, earnings, COD, RFQ and B2B fenced in code.
- COD removed from `shared/paymentmethod/paymentmethod.go` (allowed: `upi`, `card` only).
- Admin console callers off.

**Not switchable:**
- There is no gateway or client flag that switches MStore off.
- Android `ProductGate` leaves `MSTORE` and `MSELLER` ungated on purpose (`app/…/features/ProductGate.kt:26-28`).
- The web has no shop flag. `/shop`, `/shop/browse` and `/shop/products/` are public; the rest require sign-in (`src/middleware.ts:65-69,130`).

## 9. Background workers, sweepers, reconcilers

All run as goroutines in the server pod. There are no worker Deployments and no CronJobs.

| Service | Worker | Interval | Evidence |
|---|---|---|---|
| commerce | payments consumer (durable inbox) | stream | `C/cmd/server/main.go:385-387` |
| commerce | refund worker (drains `order_refund_commands` → payments `/refund`) | 20 s | `:393`, `S/checkout_p0.go:633` |
| commerce | delivery dispatcher (local delivery) | — | `:394` |
| commerce | reservation expiry (terminal: order → `expired`, stock released; reservation TTL 20 min, `P/checkout.go:680`; backoff 053) | 1 min | `:399`, `S/checkout_p0.go:696` |
| commerce | stub auto-delivery (dev only; ignored in managed ENVs) | configurable | `:409-421` |
| commerce | fulfillment worker (`fulfillment_jobs`: invoice, shipment booking) | — | `:425-426` |
| commerce | outbox publisher → `social.events.v1` | 500 ms | `:433-438` |
| commerce | identity role worker (`identity_role_intents` → identity-auth) | — | `:452-457` |
| commerce | integrity gauges, view-count flush | 30 s, ~10 s | `:479,493`, `:607` |
| payments | refund command worker (`X-Refund-Idempotency`) | 15 s | `PAY/cmd/server/main.go:335`, `service/p0.go:267` |
| payments | reconciler (pending older than `PENDING_AGE_SEC` is fetched from Razorpay; only path to `failed`) | 60 s | `service/p0.go:709` |
| payments | refund-required worker (late, duplicate and mismatched captures) | 30 s, **flag off in deployed ENVs** | `main.go:341-343`, `service/refund_required.go:71` |
| payments | commerce duplicate-capture consumer | stream, **flag off** | `consumers/commerce.go:60-62` |
| payments | outbox relay | — | `main.go:324-330` |
| — | **no** seller payout executor, COD remittance reconciler, invoice reissue job (`C/cmd/reissue-invoices` is manual), GST return export, or review moderation sweep | — | — |

Manual tools:
- `C/cmd/piibackfill` (`-set=kyc`)
- `C/cmd/reissue-invoices`
- `C/cmd/seed-demo` (seeds live products; `docs/COMMERCE-TESTING.md`)
- `C/cmd/migrate -gated`

## 10. State machines as declared

**Order status:**
- Vocabulary: `orders_status_check_v2` (`C/database/migrations/010_gst_and_state_machine.sql:145-150`): `created, payment_pending, payment_failed, expired, paid, confirmed, packed, shipped, out_for_delivery, delivered, cancelled, return_requested, return_approved, return_rejected, return_picked_up, returned, refund_pending, refunded, awaiting_approval`.
- Allowed `(from, to, actor)` rows in `order_status_transitions`:

| From | To | Actors | Source |
|---|---|---|---|
| `payment_pending` | `confirmed` | system | 010:93-118 |
| `payment_pending` | `payment_failed` | system | 010:93-118 |
| `payment_pending` | `cancelled` | customer, system, admin | 010:93-118 |
| `payment_pending` | `expired` | system | 010:93-118 |
| `payment_failed` | `cancelled` | customer, system | 010:93-118 |
| `payment_failed` | `payment_pending` | customer (retry) | 010:93-118 |
| `confirmed` | `packed` | seller | 010:93-118 |
| `confirmed` | `cancelled` | customer, seller, admin | 010:93-118 |
| `confirmed`, `packed` | `shipped` | system | **033** |
| `packed` | `shipped` | seller | 010:93-118 |
| `packed` | `cancelled` | customer, seller, admin | 010:93-118 |
| `shipped` | `out_for_delivery` | system | 010:93-118 |
| `shipped`, `out_for_delivery` | `delivered` | system | 010:93-118 |
| `shipped`, `out_for_delivery` | `delivered` | seller | **054** |
| `shipped`, `out_for_delivery` | `cancelled` | admin | 010:93-118 |
| `delivered` | `refund_pending` | admin | 010:93-118 |
| `cancelled` | `refund_pending` | system, admin | 010:93-118 |
| `refund_pending` | `refunded` | system | 010:93-118 |
| `expired` | `cancelled` | system | 010:93-118 |

- Note that `paid` appears in no row: the order goes `payment_pending → confirmed` on capture.
- **Enforcement:**
  - The DB trigger `trg_order_transition` is attached **only by gated 998** (`gated/998_contract_triggers_and_fences.sql:74`). The code itself says that on a boot-migrated database "nothing guards `UPDATE orders SET status`" (`P/order_fulfilment.go:3-7`).
  - Go checks the matrix on the seller pack and ship paths (`P/order_fulfilment.go:43-48`) and on cancel (`S/order_lifecycle.go:238-242`). Not every writer was traced.

**Order `payment_status`** (setup:444): `pending, processing, paid, failed, refund_pending, refunded, partially_refunded`.

**How an order becomes paid** (deployed environments):
1. The buyer opens an intent at `POST /orders/:id/payment/intent`. The amount is the server's order total.
2. commerce calls payments `POST /internal/intents` with a service token (`C/internal/payments/p0client.go:225`). The idempotency key is `order:<id>:attempt:<n>` (`:217`).
3. The client opens Razorpay Checkout with the server-issued `order_id` and `key_id`.
4. Razorpay sends the signed webhook (section 2.2). payments-service then:
   - dedupes on `(provider, event_id)`;
   - resolves the intent by `provider_order_id`, refusing an ambiguous match;
   - runs `VerifyProviderMoney`: amount equals `amount_minor`, currencies equal and non-blank, all in one transaction (`PAY/internal/store/postgres/p0.go:194-240,332-390`, `PAY/internal/gateway/moneytruth.go:75-110`). A mismatch rolls back, the PSP retries, and the capture is recorded as owed.
5. The outbox sends `payment.succeeded` on `social.events.v1`.
6. commerce `ApplyPaymentSucceeded` (`P/p0.go:110-350`):
   - claims `payment_event_inbox`;
   - takes `SELECT … FOR UPDATE` on the order;
   - compares the tuple (amount = `final_amount_minor`, currency, payer, bound `payment_intent_id`) via `paymentevents.Expected/Observed` (:215-229). A **blank currency or nil payer in the event is not treated as a mismatch** (:215-216).
   - a capture on an expired, cancelled or refunded order becomes a late-capture refund (`late_capture_refund_id`, `payment_status='refund_pending'`, :260-304);
   - otherwise it commits the reservations and sets `confirmed` / `paid`.
7. Stub path: commerce `/payment/confirm` → payments `/verify`. Only in a local ENV.

**Payment intent** (payments): states `pending, processing, succeeded, failed, refunded, partially_refunded, disputed` (004:43). Transitions:
- `pending`/`processing` → `succeeded`, `failed` or `processing`
- `succeeded` → `refunded`, `partially_refunded` or `disputed`
- `partially_refunded` → `refunded` or `disputed`
- evidence: `PAY/internal/store/postgres/p0.go:550-564`

Webhook `payment.failed` is recorded only; `failed` is set only by the reconciler after `PAYMENTS_FAILED_ATTEMPT_WINDOW` (15 min). **Nothing sets `disputed`**: no chargeback or dispute event mapping was found (`service/p0.go:65-161`).

**Inventory:**
- Checkout locks the cart (`SELECT … FROM carts WHERE user_id=$1 FOR UPDATE`, `P/checkout.go:293`) and the variant and product (`FOR UPDATE OF v, p`, :863).
- Reservation is `UPDATE inventory_items SET reserved_qty = reserved_qty + $2` (:694). The **oversell guard is the CHECK `reserved_qty <= total_qty`** (NOT VALID, which still applies to new writes; 009:92). A violation becomes `OutOfStockError` (:685-704).
- Each reservation row carries `expires_at = now + 20 min` and writes a ledger row.
- On capture the reservation is committed; on expiry, failure or cancel it is released (`P/p0.go:551-610`).

**Refund:** order cancel or late capture → `order_refund_commands` (durable) → commerce refund worker → payments `/internal/intents/:id/refund` → `refund_commands` → Razorpay → refund webhook → `payment.refunded` → commerce `refund_pending → refunded` (`order_refund_settlements`, 048). Returns are fenced, so **the only buyer refund path is cancellation before shipment**; after delivery only `delivered → refund_pending` by admin, and no admin route performs it.

**Coupons:**
- `previewCoupon` in the quote does not claim capacity; `claimCoupon` runs inside the checkout transaction (`P/coupons.go:424,448`).
- The discount comes from `CouponDiscountMinor` (:144). Release on cancel or expiry is `reclaimCouponTx` (`P/coupon_release.go:71`).
- Platform-funded coupons need `COMMERCE_PLATFORM_COUPONS_ENABLED`.

**GST:** `tax.Compute` per line with exactly one of CGST+SGST or IGST (`C/internal/tax/gst.go:92-96,263`). Interstate when the seller's place-of-supply state differs from the destination state; an unknown state is refused (`ErrPlaceOfSupplyUnknown`, `P/checkout.go:497-505`). Discount and shipping are allocated by `Allocate`. Refunds are split by `RefundComponents`.

**Other machines:**

| Machine | States | Evidence |
|---|---|---|
| Seller verification | `pending, verified, rejected, suspended`; store status `active, inactive, suspended, banned` | setup:26-29 |
| Product approval | `pending, approved, rejected, flagged`, later unified (022, 046, gated 1001); status `draft, active, paused, archived` | setup:170-174 |
| COD remittance | `pending, settled, on_hold` | 004:25 |
| Refund command | `pending, submitted, succeeded, failed, needs_attention` | 008:197 |
| Return | `requested … completed` (fenced) | setup:665 |
| Shipment | statuses not read in detail | `S/shipments.go` |

## 11. Clients

### 11.1 Android

| Module | Main / unit-test files | Role |
|---|---|---|
| `:core:commerce` | 12 / 15 | `CommerceApi.kt`, `CommerceRepository.kt`, `Paise.kt`, `PaymentAttempt.kt`, seller fulfilment DTOs |
| `:core:commerce-ui` | 11 / 4 | `ProductCard`, chrome, `LocalDelivery.kt`, `AddressSummary.kt` |
| `:core:payments` | 10 / 8 | `RazorpayPaymentLauncher.kt`, `PaymentCoordinator`, `ActivityPaymentHost` (does not decide "paid"), `PaymentStatusSource` |
| `:feature:commerce` | 29 / 16 | buyer screens |
| `:feature:seller` | 35 / 17 | seller screens |

There are **no `androidTest` sources** in any of these modules.

**Buyer routes** (`feature/commerce/…/navigation/CommerceNavigation.kt`):

| Route | Screen |
|---|---|
| `MStoreRoute` :71 | `home/StoreHomeScreen.kt` |
| `ProductRoute` :74 | `product/ProductScreen.kt` |
| `TryOnRoute` :91 | `tryon/TryOnScreen.kt` |
| `BagRoute` :101 | `bag/BagScreen.kt` |
| `StoreBrowseRoute` :111 | `browse/StoreBrowseScreen.kt` |
| `FavouritesRoute` :118 | — |
| `AddressBookRoute` :122, `AddressRoute` :126, `AddAddressRoute` :129 | `address/AddressScreen.kt` |
| `CheckoutRoute` :142 | `checkout/CheckoutScreen.kt`, `CheckoutViewModel.kt`, `MStorePayments.kt` |
| `OrdersRoute` :149, `OrderDetailRoute` :152 | `orders/OrdersScreen.kt` |
| `PaymentsRoute` :156 | `payments/PaymentsScreen.kt` (payment history, "NOT a wallet") |

**Seller routes** (`feature/seller/…/navigation/SellerNavigation.kt`):

| Route | Screen |
|---|---|
| `MSellerRoute` :49 | `seller/SellerScreen.kt` |
| `SellerStockRoute` :58 | — |
| `SellerImagesRoute` :62 | — |
| `SellerPickupAddressRoute` :66 | — |
| `StartSellingRoute` :70 | — |
| `SellerPayoutRoute` :74 | `PayoutScreen.kt`, with `KeepOutOfScreenshots.kt` |
| `SellerOrdersRoute` :78, `SellerOrderDetailRoute` :82 | — |
| `SellerReturnsRoute` :86 | — |
| `SellerEarningsRoute` :90 | — |
| `SubmitShopRoute` :94 | — |
| `SellerDocumentRoute` :98 | — |
| `SellScopeRoute` :102 | — |
| `SellerBusinessAddressRoute` :106, `SellerAddressProofRoute` :110 | — |
| `NewProductRoute` :114 | — |
| `SellerEditPriceRoute` :124 | — |

**API** (`core/commerce/…/network/CommerceApi.kt:49-530`) covers catalogue, favourites, media, cart, addresses (GET and POST only), quote, `v2/orders/checkout` :207, payment intent, status and payment, orders, cancel, `sellers/me`, seller products and stock, onboarding steps (documents, payout, submit, sell-scope), seller address, seller orders (pack, ship, cancel, history), **`seller/returns` :500, `returns/{id}/approve|reject` :514-519, `seller/earnings` :530**.

Gaps against the server:
- The last three call **fenced** routes and always get 404.
- **No Android calls** for address edit or delete, invoices, shipment tracking, reviews, coupons, bank offers, seller coupons or KYC document view.

**Money and state on Android:**
- Totals come from the server quote; `expected_total_minor` is echoed back.
- CI guards against `Double` money in Android (`integration-commerce.yml:159`).
- No Room tables or DataStore for commerce were found.

**Other Android facts:**
- `UsNavHost.kt` has uncommitted edits (section 14).
- Product tags on posts: post-service `/v1/posts/:id/product-tags` tag **affiliate links** validated by monetization-service, not commerce products (`post-service/internal/http/handler.go:438-443`, `product_tags_handler.go:29-86`). live-service-v2 has no commerce integration.

### 11.2 Web (`src/app/shop`, `src/features/shop`)

| Route | File | Sign-in |
|---|---|---|
| `/shop`, `/shop/browse`, `/shop/products/[id]` | `src/app/shop/page.tsx`, `browse/page.tsx`, `products/[id]/page.tsx` | no (`src/middleware.ts:65-69,130`) |
| `/shop/bag`, `/checkout`, `/addresses`, `/favourites`, `/orders`, `/orders/[id]`, `/orders/[id]/review` | `src/app/shop/*` → `features/shop/{bag,checkout,addresses,favourites,orders}` | yes |
| `/shop/sell`, `/sell/start`, `/sell/onboarding`, `/sell/products[/new,/[id]]`, `/sell/stock`, `/sell/orders[/[id]]`, `/sell/coupons` | `src/app/shop/sell/*`, `components/sell/*` | yes |

**API clients** are in `features/shop/api/*.ts` with base `/v1/commerce` (`addresses.ts:12`). Notable calls:
- quote `checkout.ts:23`, `v2/orders/checkout` :33, payment intent `payments.ts:24`, payment and status :30-36
- invoice `orders.ts:37`, shipments :31, cancel :43, reviews `reviews.ts:48`
- coupons, offers (`BankOffers.tsx`), seller coupons `sellerCoupons.ts:13`
- onboarding steps `sell.ts:56-127`, `seller/fulfillment`, `/delivery/orders/:id/jobs`
- `confirmStubPayment` → `/payment/confirm` (`payments.ts:60-63`), shown only on a stub intent

**Payment:** `startPayment.ts:5-48` opens Razorpay from the server intent. It never marks anything paid.

**State:** sessionStorage for bag and checkout handoff (`BagScreen.tsx:20`, `CheckoutScreen.tsx:79`); localStorage for the session user and theme.

**Tests:** 160 files under `__tests__` in `features/shop` and `app/shop`.

**CI:** `.github/workflows/web-ci.yml` runs `bun install`, `tsc --noEmit`, `bun test` and `bun run build`. This corrects the long-video map, which said the web tree had no CI.

The web has **no returns, earnings, payout or KYC-document screens.**

## 12. Tests and CI

| Layer | Count (HEAD) | CI job |
|---|---|---|
| commerce Go unit | 119 files | `go-ci.yml` workspace job (`scripts/verify/go.sh --full`, sharded :70-107) |
| commerce Go integration (`//go:build integration`) | 86 files, for example `H/checkout_journey_*`, `seam_journey_*`, `coupons_switch_*`, `admin_token_*`, `stock_idempotency_*`; `P/proof_*`, `kycpii_*`, `duplicate_capture_*`, `payment_mismatch_*`, `refund_settlement_*`, `cancel_refund_*`, `expiry_sweep_*`, `order_transitions_db_test.go`; `S/invoice_issue_*`, `invoice_reissue_*`, `kycdocument_*`; `C/internal/kmsclient/manifest_contract_test.go` (values vs code) | `integration-all.yml` from `scripts/ci/integration-services.yaml:151-163` (`db: commerce_it_test`, extra `commerce_delivery_it_test`, no gated set, four gated-998 tests skipped). `integration-commerce.yml`: `money-type-gate` :36, `unit` :229, `commerce-proofs` :303 (creates `commerce_p0_test` and `payments_p0`, applies gated 998, runs TestProof/TestC3/journeys :747-781), `migration-safety` :856 |
| payments Go | 34 unit, 33 integration (`moneytruth_c3_`, `reconcile_n2_`, `refundworker_rw_`, `refundrequired_rr_`, `duplicate_capture_m1_`, `owedcapture_oc_`, `offers_*`, `providerref_a2_`) | `integration-services.yaml:168-177` (`payments_it_test`) |
| admin-service commerce/payments | `handler_commerce*_test.go` (kyc, pdf, delivery), `handler_payments_test.go`, `handler_coupons_offers_test.go`, `twoperson_integration_test.go`; several have uncommitted edits | `admin_it_test` |
| End to end | `Architecture/tools/integration/commerce_e2e_test.go:19` (browse, cart), `commerce_journey_test.go:39` (full journey), `commerce_prepaid_test.go:41` (prepaid **stub**) | `e2e.yml:89-99` (domain `commerce`) |
| Dev smoke | `health.shop`, then `shop.home/pick/add/quote`; **stops at the quote**, with no checkout, intent or webhook | `scripts/smoke/dev-smoke.mjs:476,884-952`; run by hand |
| Android unit | 60 files across the five modules | `android-native.yml` (`android.sh --full`) |
| Android instrumented | none | — |
| Web | 160 test files | `web-ci.yml` (pb-echo) |
| Chart render assertions | `charts/atpost-service/render_test.sh` (no stub flags in QA, webhook ingress, NetworkPolicy) | **not run by any workflow** |
| Load | none for commerce or payments | — |

**Scratch-database rule:** commerce integration tests use `commerce_it_test` (plus `commerce_delivery_it_test`, `commerce_p0_test`). No test or CI file uses `commerce_db`, and the DB guard refuses it (`docs/COMMERCE-TESTING.md:16-23`). Two exceptions:
- `PAY/internal/store/postgres/store_db_test.go:13-22` is an **untagged** DB test with no `_test`-name guard, and its usage comment suggests pointing `PAYMENTS_TEST_POSTGRES_DSN` at `…/commerce_db`.
- `integration-commerce.yml:330` creates `payments_p0`, which does not end in `_test`.

## 13. Deployment manifests

| Item | Fact | Evidence |
|---|---|---|
| Chart | `charts/atpost-service`; ArgoCD ApplicationSets: prod on `release/prod`, qa on `qa`, staging on `main`, Azure overlays | `deploy/argocd/applicationset*.yaml`, `deploy/azure-applicationset.yaml:27,83` |
| commerce replicas / HPA / PDB | P 2, HPA 2-6, PDB 1. Q 1, HPA 1-2. S 2, no HPA, **PDB default minAvailable 2 (blocks drains)**. AP 3, HPA 3-12. AS 2, PDB 2 | `deploy/services/commerce-service/values-*.yaml` (HEAD) |
| payments replicas | P 2, HPA 2-6; same shape as commerce | `deploy/services/payments-service/values-*.yaml` |
| Workers | `worker.enabled` false; every loop is in the server pod | `charts/atpost-service/values.yaml:216` |
| Migrations | commerce boot-migrates 001-055. The Job is off and `/commerce-migrate` is not in the image, so the gated 998-1002 can only be run by hand (`go run ./cmd/migrate -gated`). payments gated 998-999 are run by hand with psql | `C/Dockerfile:20-25`, `values-prod.yaml:176-179` |
| Webhook ingress | internet-facing ALB in the API group (order 10), HTTPS, WAF = API ACL; prod and qa only | `templates/webhook-ingress.yaml:1-59`, payments `values-prod.yaml:145-160` |
| NetworkPolicy | payments, prod and qa: admits commerce, food, dating, rider, doorstep, monetization, admin, **api-gateway** and the **whole VPC CIDR** `10.30.0.0/16`. **commerce-service and wallet have none** | payments `values-prod.yaml:164-178` |
| Secrets (names) | commerce: `POSTGRES_DSN`, `REDIS_*`, `KAFKA_*`, `INTERNAL_SERVICE_KEY`, `JWT_*`, `COMMERCE_SERVICE_TOKEN_KEY/KID`, `COMMERCE_KMS_KEY_ID`, `COMMERCE_PII_LOOKUP_SALT`, `COURIER_PROVIDER`, `SHIPROCKET_*` (prod). payments: `RAZORPAY_KEY_ID/SECRET/WEBHOOK_SECRET`, `SERVICE_CALLER_{COMMERCE,FOOD}_SERVICE_PUBKEY/KID` | values `externalSecret` blocks |
| Azure overlays | commerce and payments lack the DSN, Kafka, KMS, blob, Razorpay and `SERVICE_CALLERS`, so **neither can boot**; the gateway there has no disabled prefixes | `values-azure-prod.yaml` |
| Image tags | staging and azure-prod use `latest`; azure-staging is pinned to an old sha | — |

## 14. Uncommitted changes seen (not audited)

From `git status --porcelain` at audit time. These files were read only at HEAD.

| Area | Path |
|---|---|
| deploy | `deploy/services/commerce-service/values-{prod,qa,staging,azure-prod,azure-staging}.yaml`. `git diff --stat`: 8 or 12 lines each, 44+/44- across the commerce and payments values together |
| deploy | `deploy/services/payments-service/values-{prod,qa,staging,azure-prod,azure-staging}.yaml` (8 lines each) |
| payments | `internal/http/admin_routes.go`, `admin_token.go`, `admin_token_test.go`, `internal/service/refund_required.go` |
| payments | `internal/store/postgres/admin.go`, `refund_attention.go`, `refund_required.go`, `refund_required_test.go` |
| payments | untracked `internal/store/postgres/refund_attention_test.go` |
| admin-service | `internal/http/handler_commerce.go`, `handler_commerce_test.go`, `handler_coupons_offers_test.go`, `handler_payments.go`, `handler_payments_test.go`, `internal/adminauth/{catalogue,permissions}.go`, `internal/approvals/approvals.go`, `gate.go` and about 30 more |
| api-gateway | `cmd/server/main.go`, `disabled_prefixes_gate_test.go`, untracked `admin_flags_route_test.go` |
| Android | `mobile/android/app/src/main/kotlin/com/us/android/navigation/UsNavHost.kt` |
| commerce-service | **none** |

## 15. Open questions for the review tracks

Focus tags: [money], [paid], [webhook], [inventory], [tenant], [refund], [coupon], [payout], [gst], [kyc], [ops].

1. **[paid] Gated contract migrations are probably not applied anywhere deployed.** Gated 998 attaches the order-transition trigger and the prepaid-only `payment_method` CHECK. Gated 999 validates the oversell and coupon constraints and makes `*_minor` NOT NULL. The migrator is not in the image and the Job is off. Which databases (dev, qa, prod) have 998/999 applied? Without 998, the status matrix is enforced only on the Go paths that ask for it (`P/order_fulfilment.go:3-7`). Which writers update `orders.status` without that check?
2. **[money] Legacy NUMERIC money columns are still written or read?** `orders`, `order_items`, `variants`, `coupons` and `cart_items` carry NUMERIC alongside `*_minor`. `invoices` and `cod_remittances` are **NUMERIC only**. Which column does each read path (invoice render, seller earnings, admin stats, search docs) use, and is `money_minor_drift` zero on dev?
3. **[payout] No seller is ever paid.** There is no payout executor; `CreatePayoutBatch` has no caller; payout preview and earnings are fenced; the payout bank account is stored, but not penny-drop verified (KYC is a stub). How does a seller receive prepaid sale proceeds at launch, and who is the merchant of record? Is Razorpay Route or RazorpayX planned? TDS/TCS (`COMMERCE_TDS_PCT` unset) and commission (`COMMERCE_COMMISSION_PCT` unset) are undefined.
4. **[kyc] KYC verification is format-only in every environment** (`kyc.StubValidator`). Admin "KYC verify" stamps a seller verified on regex checks. Is a vendor required for launch, and does the admin UI show `Source: stub`?
5. **[refund] There is no post-delivery refund or return path.** Returns are fenced; `delivered → refund_pending` is admin-only, and no admin route exists for it, for disputes, or for cancelling an order on a buyer's behalf. Nothing maps Razorpay disputes or chargebacks (`disputed` is never set). Is "cancel before ship" the whole launch refund policy?
6. **[refund] payments' refund-required worker and duplicate-capture consumer are OFF in every deployed ENV.** Late, duplicate or mismatched captures are recorded but not automatically returned. Commerce's own late-capture path does write a refund command; confirm which of the two owns each case in prod, and whether money can sit in `refund_required` with no worker.
7. **[webhook] Replay:** the HMAC covers the body only, and dedupe keys on the unsigned `X-Razorpay-Event-Id` header with no timestamp window. A replayed body with a fresh id passes. The state machine absorbs captures; confirm that refund events and `payment.failed` re-processing are idempotent too.
8. **[webhook] Ingress exposure:** there is no in-app rate limit, the dedicated `psp-webhook` WAF ACL is unwired, and the NetworkPolicy admits the whole VPC CIDR plus api-gateway. Are those acceptable, and should the webhook path have its own ACL and source-IP allowlist?
9. **[paid] Event trust:** `payment.succeeded` arrives on the shared `social.events.v1`, which any producer with Kafka credentials can write. commerce treats a blank currency or nil payer in the event as "not a mismatch" (`P/p0.go:215-216`). Could a forged event with the right `intent_id` and amount confirm an order? Is per-topic ACL or event signing required?
10. **[tenant] Unscoped reads with Go ownership checks:** `GET /orders/:id`, `/items`, `/payment*`, `/delivery/jobs/:id`. `GET /orders/:id/payment` answers 403, not 404, which confirms the id exists. Any seller on an order can book shipments and issue invoices for **all** sellers on it (safe only while carts are single-seller). B2B `POST /orders/:id/approve|reject` escapes the `/organizations` fence.
11. **[tenant] Internal-key admin surface:** `/v1/commerce/internal/*` takes an unverified `X-User-Id` actor and covers seller approve, KYC verify, COD settle and product moderation. Any in-cluster holder of the shared key bypasses admin-service step-up, two-person approval and audit. commerce has **no NetworkPolicy**. The token route (AT) that fixes this is off, because `SERVICE_CALLERS` is commented out everywhere, so the admin console's MStore and Payments pages return 401. What is the launch plan?
12. **[ops] Razorpay live vs test is invisible:** prod carries test keys by design, and nothing asserts the mode at boot or in monitoring. The same applies to `COURIER_PROVIDER` in prod, which comes from a secret, and the code does not refuse the stub courier in a managed ENV (fake AWBs, a webhook accepting anything). Add boot assertions?
13. **[coupon] Coupons:** confirm that the `claimCoupon` capacity check is atomic under concurrency (`chk_coupon_uses_within_max` is NOT VALID until 999), that reclaim on cancel or expiry is exactly once, and how a coupon discount is split between seller-funded and platform-funded coupons in GST (`Allocate`) and refunds (`RefundComponents`).
14. **[coupon] Bank offers:** prod commerce lacks the `payments:offers.read` op, so enabling `PAYMENTS_OFFERS_ENABLED` would break the offer list. When an offer reduces the capture, commerce compares `captured + discount = amount` (`P/p0.go:83-92`). Who funds the discount and how is it invoiced?
15. **[gst] GST and invoices:**
    - Invoices are NUMERIC; there is no e-invoice IRN or QR.
    - The invoice URL is a 7-day presigned link to a versioned bucket.
    - The PDF is best-effort (wkhtmltopdf), with HTML as the fallback.
    - Place of supply uses the seller's state vs the delivery state.
    - How are B2B GSTIN buyers handled?
    - Who files GSTR-1 and TCS under section 52 for a marketplace?
    - Is the tax adviser's sign-off (named in the payments prod values) tracked?
16. **[inventory] Oversell:** the CHECK `reserved_qty <= total_qty` is the only guard (it applies to new writes even while NOT VALID). Confirm with a concurrency test. Also: the 20-minute reservation TTL against Razorpay's capture window (a late capture becomes a refund); a seller's stock decrease below `reserved_qty`; and `inventory_ledger` reconciliation (999 builds a drift view).
17. **[money] What the client can send:**
    - Checkout takes ids, a coupon code, a method and `expected_total_minor` (compare-only). Confirm no price, discount or shipping value is read from any body.
    - `POST /payment-offers?amount_minor` is display-only.
    - Seller-side price writes (`PATCH /variants`, price tiers) set MRP and selling price. Are there bounds (price > 0, selling ≤ MRP) and audit?
18. **[ops] Environments that cannot boot:**
    - Staging commerce has no blob backend, no KMS and no invoice bucket.
    - Azure prod and staging lack DSNs, Razorpay and callers.
    - Staging and azure-staging PDB `minAvailable 2` with 2 replicas blocks drains.
    - Payments' webhook ingress exists only in prod and qa.
    - Is staging meant to run a checkout end to end?
19. **[ops] Coverage gaps:** no load test; the dev smoke stops at the quote; e2e covers only the **stub** prepaid path, never a signed Razorpay webhook; `render_test.sh` is not in CI; there are no Android instrumented tests. An untagged payments DB test suggests `commerce_db`.
20. **[parity] Android calls fenced routes** (`seller/returns`, `returns/{id}/approve|reject`, `seller/earnings`) and lacks address edit/delete, invoices, tracking, reviews, coupons and offers that the web has. Under the "Android is the super app" rule these are Android gaps. Which are launch-blocking?
21. **[kyc] PII cutovers sit at `dual`** in prod, qa and staging; gated 1000 and 1002 (plaintext scrub) have not run. With prod starting empty, should prod launch directly at `ciphertext` so full bank account numbers and PANs are never stored in plaintext there?
22. **[ops] Notifications:** the buyer gets paid, shipped, delivered, cancelled, refunded and payment-failed notices; the seller gets only "new order". Are seller notifications for cancellations, refunds, low stock, KYC decisions and product moderation needed for launch (the `commerce.seller.*` and `commerce.product.*` consumers were not traced beyond search)?
