# Commerce (MStore / MSeller): Track B, security

- **Audit date:** 2026-11-17. Read-only.
- **Tree:** `C:\workspace\modernsmapp` @ HEAD of `codex/module-01-02-launch-safety` (`be57a46bf`). commerce-service has no uncommitted edits; payments-service, admin-service, api-gateway and the deploy values were read at HEAD with `git show HEAD:<path>` where the working copy is dirty. Web: `C:\workspace\wt\pb-echo` @ `1b596b4` (clean).
- **Scope:** commerce-service, payments-service, the PSP webhook ingress, admin-service's `/v1/admin/commerce` and `/v1/admin/payments` routes, the media-service authority commerce answers for, Android `:core:commerce`, `:core:payments`, `:feature:commerce`, `:feature:seller`, and web `src/features/shop`. wallet-service is not used by commerce or payments (00-map §1.1), so it is not covered.
- **Path shorthand** (as in 00-map): `C/` = `Architecture/services/commerce-service/`, `H/` = `C/internal/http/`, `S/` = `C/internal/service/`, `P/` = `C/internal/store/postgres/`, `PAY/` = `Architecture/services/payments-service/`, `GW` = `Architecture/services/api-gateway/cmd/server/main.go` at HEAD, `MS/` = `Architecture/services/media-service/`.
- **Evidence tags:** VERIFIED-RUN (a test or command was run), VERIFIED-READ (code read at the cited lines), SUSPECTED (what would confirm it is stated), UNVERIFIED (not checked; what is missing is stated).
- **Commands run** (all passed, on the working tree; none of these packages has uncommitted edits):
  - `go test ./internal/http/ -run 'Fence|InternalKey|GatewayTrust|RequireActor'` in commerce-service
  - `go test ./paymentevents/` in `Architecture/shared`
  - `go test ./internal/gateway/ -run 'Webhook|Razorpay' -v` in payments-service, which includes `TestRazorpay_TamperedBodyRejected`, `TestRazorpay_EmptyWebhookSecretRejectsEverything`, `TestRazorpay_MissingEventIDHeaderIsRejected` and `TestRazorpay_CapabilitiesDeclareNoWebhookTimestamp`
- No secret file was opened. No secret value appears below.

## Counts

| Severity | Count | Evidence levels |
|---|---|---|
| P0 | 0 | — |
| P1 | 5 | 5 VERIFIED-READ (COM-B-1 and COM-B-4 carry a SUSPECTED part, stated in the finding) |
| P2 | 6 | 4 VERIFIED-READ, 2 VERIFIED-READ with a SUSPECTED part (COM-B-10, COM-B-11) |
| P3 | 6 | 3 VERIFIED-READ, 1 VERIFIED-RUN + READ, 2 SUSPECTED |

**Answers to 00-map open questions 4 and 5:**
- **Q4 (KYC).** The stub never stamps `verified`. It writes `format_ok` (`S/onboarding.go:443-460`). But seller approval does not read the verification status at all (`P/onboarding.go:337-361`). The admin console's MStore pages, including the KYC document view, answer 401 today. A seller can therefore go live on unverified identity. See COM-B-4.
- **Q5 (refunds).** Confirmed: the only buyer refund is a cancel before shipment. Under the stub courier a seller can record their own tracking number and mark the order delivered, and after that the buyer has no route to a refund. See COM-B-4.

## 1. Route and auth table

**Legend:**
- **GT** = the gateway-trust key. This is the same `INTERNAL_SERVICE_KEY` that every service holds (`H/gatewaytrust.go:77-114`).
- **UID** = `X-User-Id`, re-derived from the JWT by the gateway (`GW:854,1076`).
- **SEL** = the caller's `sellers` row.
- **IK** = internal key plus an unverified `X-User-Id` actor.
- **AT** = admin-service Ed25519 token (401 everywhere today).

"Object check" names where the caller is matched to the object. "Result" is my verdict for authorization on that row.

| Route family (prefix `/v1/commerce`) | Auth | Object check | Result |
|---|---|---|---|
| GET catalogue: `/categories*`, `/tax-classes`, `/home`, `/products`, `/products/:id[/preview,/media,/attributes,/variants,/reviews,/delivery-estimate]`, `/variants/:id/price-tiers`, `/sellers/:id/products`, `/payment-offers` | public (GT). Deliberate: a shop front must be readable signed out (web `src/middleware.ts:65-69`) | `productReadable` returns 404 for a non-live product to anyone but its owner (`H/handler.go:282-294`) | sound, VERIFIED-READ |
| POST `/products`, PATCH `/products/:id` | UID+SEL | owner (`S/productwrite.go:620-626`). Body allowlist; unknown keys are refused by name (`H/handler_products.go:66-180`) | sound, VERIFIED-READ |
| POST/PATCH/DELETE variants, PUT price tiers | UID+SEL | owner via the parent product (`S/catalogue_products.go:525-543,576-595`) | **`image_media_id` not ownership-checked: COM-B-1** |
| product media, attributes, try-on | UID+SEL | `assertProductSeller` (`S/catalogue_products.go:603`); media ownership on gallery adds | sound (media), VERIFIED-READ for media add (`S/productwrite.go:657-662`, `S/catalogue_products.go:131`) |
| seller stock, `/seller/products`, `/seller/variants/:id` | UID+SEL | `WHERE seller_id` (00-map; not re-read) | UNVERIFIED by me (00-map §2.1 read it) |
| cart, `/cart/coupon-preview`, `/cart/coupons` | UID | keyed on the user | sound; coupon guessing is unthrottled (COM-B-9) |
| `/addresses[/:id[/default]]` | UID | `WHERE id=$1 AND user_id=$2` (00-map; `P/addresses.go:125,164,206`) | sound per 00-map, not re-read |
| `/checkout/quote`, `/v2/orders/checkout` | UID | quote locked to the user; the body carries ids, a coupon, a method and a compare-only `expected_total_minor` (`H/handler_p0.go:370-378`). Method allowlist (`H/handler_p0.go:329,413`; `shared/paymentmethod`) | sound, VERIFIED-READ |
| GET `/orders`, `/orders/:id`, `/orders/:id/items` | UID | customer check after an unscoped read (`S/orders_read.go:129-136,459-468`); a stranger gets 404 | sound, VERIFIED-READ |
| POST `/orders/:id/cancel` | UID | `FOR UPDATE` + customer check + matrix (`P/p0.go:691-723`) | sound, VERIFIED-READ |
| `/orders/:id/payment/intent`, `/payment/status` | UID | owner (`S/checkout_p0.go:427-434,483-489`); amount = server total | sound, VERIFIED-READ |
| GET `/orders/:id/payment` | UID | owner. A stranger gets 403, not 404 (`S/customer_payment.go:109`, `H/handler_p0.go:519-533`) | oracle: COM-B-14 |
| POST `/orders/:id/payment/confirm` | registered only with `PAYMENTS_ALLOW_STUB`; fenced otherwise | — | sound (00-map §2.1) |
| POST `/orders/:id/approve`, `/reject` (B2B) | UID | org role (`S/organizations.go:268-320`). **Not fenced** | latent, COM-B-16 |
| POST `/orders/:id/shipment`, POST `/orders/:id/invoice` | UID | caller must be **a** seller on the order (`H/shipments.go:36-60`); both require `payment_status=paid` (`S/shipments.go:117-122,629-633`) | sound only while carts are single-seller (00-map Q10) |
| GET `/orders/:id/shipment[s]`, GET `/orders/:id/invoice` | UID | customer or any seller on the order (`H/shipments.go:54-57`); a stranger gets 403 | oracle: COM-B-14 |
| `/seller/orders*`, pack, cancel, ship, mark-delivered | UID+SEL | `requireWholeOrder` (`S/fulfilment.go:104-122`). Mark-delivered only on seller-managed shipments (`S/seller_delivery.go:98-112`) | sound for authorization; fraud angle in COM-B-4 |
| POST `/shipments/webhooks/:courier`, `/shipments/courier-callback` | public (GT); provider `VerifyWebhook` | Shiprocket: constant-time token or HMAC, closed when unset (`C/internal/courier/shiprocket.go:61-89`). **Stub accepts anything** (`courier.go:159-161`) | COM-B-10 |
| POST `/products/:id/reviews` | UID | the order item is the caller's own delivered item (`S/reviews.go:29-46`); `UNIQUE(reviewer_id, order_item_id)` (`C/database/setup.sql:624`) | sound; abuse in COM-B-12 |
| POST `/reviews/:id/seller-response` | UID+SEL | `checkReviewSeller` (`S/reviews.go:87-92`) | sound, VERIFIED-READ |
| reactions, votes, share, favourites | UID; share allows anonymous | per-user keys | share limiter: COM-B-15 |
| `/seller/coupons[/:id]` | UID+SEL | PATCH scoped by seller (`P/coupons.go:1322-1327`); targets must be the seller's own products (`P/coupons.go:1103-1112`); claim refuses another seller's goods (`P/coupons.go:296-299`) | sound, VERIFIED-READ |
| onboarding steps, `/onboarding/*`, `/sellers/me`, `/dashboard`, sell-scope | UID | `WHERE user_id` (`P/onboarding.go:132-143`); KYC media ownership (`S/onboarding.go:131-176`) | post-approval edits: COM-B-6 |
| `/delivery/*` (off: `COMMERCE_LOCAL_DELIVERY_ENABLED` unset) | UID | job reads checked in Go (`S/delivery_operations.go:124-132`) | sound, VERIFIED-READ (spot) |
| fenced: returns, payout, earnings, COD remittances, RFQ, orgs, bulk import, affiliate | 404 before routing (`H/handler_p0.go:43-59,213-250`) | — | sound, VERIFIED-RUN (fence tests) |
| `/v1/commerce/internal/*` (IK) | key + unverified `X-User-Id` actor (`H/internal_guard.go:42-74`; `H/handler_onboarding.go:36-103`). Refused at the edge (`api-gateway/pkg/internalroutes/internalroutes.go:58-110,195-208`) | none: the actor is a header | COM-B-3 |
| `/v1/commerce/internal/media-access[/batch]` (IK) | key; the viewer comes from the body | — | the authority behind COM-B-1 |
| `/v1/commerce/internal/admin/*` (AT) | admin token; the actor is the signed act | — | sound by design; 401 today (00-map §2.1) |
| payments `POST /v1/payments/webhook` | HMAC over the raw body (`PAY/internal/gateway/razorpay_provider.go:166-183`) | intent resolved by `provider_order_id` | sound for the signature, VERIFIED-RUN. Replay: COM-B-13. Ingress: COM-B-11 |
| payments `/v1/payments/internal/intents*` | service token + op + application + owner domain (`PAY/internal/http/handler.go:236-240,900-930`). The legacy key is refused in prod on money routes (`:232-252`, `:280-300`) | `ownsIntent` 404 | sound, VERIFIED-READ |
| admin-service `/v1/admin/commerce/*`, `/v1/admin/payments/*` | admin session; permission, step-up, two-person per route (HEAD `admin-service/internal/http/handler_commerce.go:75-135`) | upstream AT | sound by design; unreachable while AT is 401 |

## 2. Inherited (platform findings re-checked and still true for commerce)

| Source ID | What | Re-check for commerce |
|---|---|---|
| feed-27 | One shared internal key and no NetworkPolicy between pods | commerce's gateway-trust key **is** `INTERNAL_SERVICE_KEY` (`H/gatewaytrust.go:63-114`). The gateway stamps it on every `/v1/commerce` request (`api-gateway/pkg/internalroutes/internalroutes.go:490`). The commerce chart has no `networkPolicy` (HEAD `deploy/services/commerce-service/values-prod.yaml`, no match). So any pod holding the key can call any commerce user route as any `X-User-Id`. The commerce-specific consequence is COM-B-3. VERIFIED-READ |
| feed-64 | Wildcard CORS is only warned about; any origin is reflected with credentials | `GW:62` (warning only), `GW:1295` (`Access-Control-Allow-Credentials: true`). VERIFIED-READ |
| reels-21 | The web sets no CSP, frame-ancestors or HSTS | `next.config.ts` and `src/middleware.ts` set no such header (grep). The access token is now held in memory (`src/lib/accessToken.ts:4-25`), so an XSS in a shop page acts as the user but cannot exfiltrate a stored token. VERIFIED-READ |
| reels-77 | The `access_token` cookie is a credential at the gateway | The gateway accepts `access_token` as a cookie on every non-admin REST path (`GW:1008`) with no CSRF check. The web scopes its copy to `/v1/media` (`src/app/api/auth/_lib/session.ts:46-53`). auth-service sets one with `Path=/` and `SameSite=Lax` (`identity-platform/services/auth-service/internal/http/handler.go:412-417`). Lax keeps cross-site POSTs out, so commerce writes are not CSRF-able cross-site; a same-site sibling host would be (SUSPECTED; confirm the cookie `Domain` per ENV). VERIFIED-READ for the code |
| feed-25 | The web `/v1` proxy forwards no client address | `src/app/api/proxy/[...path]/route.ts` forwards only an allowlist with no `x-forwarded-for` (grep). All web shoppers share one per-IP edge bucket. VERIFIED-READ |
| reels-78 / feed-26 | Raw Go error text returned | commerce still echoes `err.Error()` on several routes: COM-B-14 |

## 3. Findings

### P1

```
ID:             COM-B-1
Title:          A seller can attach ANY media id as a variant image, and commerce then tells media-service that the seller (and, once the listing is live, everyone) may view it
Severity:       P1
Category:       Authorization / IDOR (cross-service)
Evidence level: VERIFIED-READ (both halves read). SUSPECTED only for media of kind `document`; confirm whether media-service refuses document kind before its gate
Evidence:
  - H/handler_variants.go:150-176. PATCH /variants/:variantId decodes the body into map[string]any and passes it on.
  - S/catalogue_products.go:525-556. UpdateProductVariant checks product ownership, then calls checkVariantPatch and store.UpdateVariant. No verifyMedia call.
  - S/reviewedcontent.go:376-383. image_media_id is only compared, to decide revalidation.
  - P/variants_stock.go:161-172. `image_media_id` is in the store allowlist and is written as sent.
  - Contrast: the product's own images ARE verified (S/catalogue_products.go:131, S/productwrite.go:657-662, S/service.go:391-402), as are storefront logo and banner (S/onboarding.go:113-118) and the delivery proof (S/seller_delivery.go:182-186).
  - P/storefront.go:777-793. VisibleProductMediaIDs allows a media id referenced by `pv.image_media_id` when the product is live, OR when the viewer is the product's seller (`seller_user_id = $2`).
  - MS/internal/delivery/authz.go:398-430. AnyContentAuthorizer allows on the FIRST authority that says yes; commerce is one of five authorities (MS/cmd/server/main.go:537-543).
How to reproduce:
  1. Seller S has a draft product P with variant V.
  2. S learns the media id M of someone else's protected asset (a followers-only post photo, a chat photo, an unsent message, a deleted post).
  3. S sends PATCH /v1/commerce/variants/V {"image_media_id":"M"}. The answer is 200.
  4. GET /v1/commerce/products/P/variants as S: commerce resolves M through media-service with viewer=S, and commerce's media-access answers allowed (seller arm). S receives a signed URL.
  5. If P is approved, the anonymous arm opens M to everyone.
Impact:         Any seller (onboarding is self-serve) can read, and then publish, another user's private media whose id they hold. Every privacy decision taken by post, chat, profile and group services is bypassed for that asset.
Fix:            In S.UpdateProductVariant, when `image_media_id` is in the body, call s.verifyMedia(ctx, actorID, media.KindImage, &id) before the store write (the same call PATCH /products makes for primary_image_media_id). Apply the same rule to any future variant-create path that takes an image.
Acceptance:     A test that PATCH /variants/:id with a media id owned by another user returns 403 NOT_YOUR_MEDIA and writes nothing; and that the seller's own image still succeeds.
Effort:         S (one call + one test)
```

```
ID:             COM-B-2
Title:          Any holder of the single shared Kafka credential can confirm any order as paid by publishing payment.succeeded; commerce accepts a blank currency, payer and intent id
Severity:       P1
Category:       Payment integrity / event trust
Evidence level: VERIFIED-READ (VERIFIED-RUN for the policy behaviour: shared/paymentevents tests pass)
Evidence:
  - infra/terraform/modules/msk/main.tf:11-17 ("The one application user"), :78-82 (scram_username default "atpost"). Every service uses one SCRAM login, and there are no topic ACLs.
  - C/internal/consumers/payments_p0.go:347-360. The only filter is reference_type == "order". The source of the event is not checked.
  - P/p0.go:214-229. CheckCapture is called with paymentevents.AllowUnstated.
  - Architecture/shared/paymentevents/check.go:63-95. Under AllowUnstated, an empty currency, a nil payer and an empty intent are skipped; only the amount must match.
  - P/p0.go:918-931. captureIntentToBind with an empty incoming intent binds nothing and does not refuse.
  - The order total is shown to the buyer (S/orders_read.go:440+), so the amount is known.
How to reproduce:
  From any pod with the Kafka secret (about 30 services), publish on social.events.v1 an envelope of type payment.succeeded with payload {reference_type:"order", reference_id:<order>, amount_minor:<order total>} and no currency, payer or intent_id. ApplyPaymentSucceeded claims the inbox row and moves the order payment_pending → confirmed / paid. The seller then ships.
Impact:         Goods shipped without payment. It needs in-cluster compromise of ANY service, not just payments, so it turns a low-value service compromise into theft.
Fix (smallest): (a) In commerce, use a strict policy for payment.succeeded: a blank currency, a nil payer or a blank intent_id is a mismatch, since payments-service always sets them. (b) Before confirming, call payments GET /internal/intents/:id (op intent.read, already granted) and require status=succeeded and amount_minor equal to the order. Longer term: per-service Kafka users with write ACLs on the payment event types, or signed events.
Acceptance:     Integration test: a payment.succeeded with a blank currency, a nil payer or an unknown intent leaves the order payment_pending. A forged event naming a real but unpaid intent is refused by the server-side intent check.
Effort:         M
```

```
ID:             COM-B-3
Title:          Seller approve, KYC verify, product approve and COD settle are callable by any pod with the shared internal key, as any actor named in X-User-Id, bypassing admin-service's step-up, two-person rule and audit
Severity:       P1
Category:       Authentication / service-to-service
Evidence level: VERIFIED-READ
Evidence:
  - H/handler_onboarding.go:36-73. The IK group has sellers approve, reject, request-changes, suspend, unsuspend, kyc/verify; payouts/pending; products approve, reject; cod-remittances/:id/settle; attribute and category publishing.
  - H/internal_guard.go:42-54. requireActor takes the actor from the X-User-Id header when no admin token is present.
  - H/internal_guard.go:56-74. Only the shared key is checked.
  - H/handler_seller.go:106-113. COD settle uses requireActor.
  - The same key is the gateway-trust key on every user route (H/gatewaytrust.go:63-114) and is held by every service.
  - The commerce chart has no networkPolicy (HEAD values-prod.yaml).
  - admin-service applies step-up to KYC verify and COD settle, and the two-person rule to COD settle (HEAD admin-service/internal/http/handler_commerce.go:94-125). That path is AT and answers 401 today, because commerce SERVICE_CALLERS is commented out (HEAD values-prod.yaml:159). So the IK path is the only working one.
How to reproduce:
  From any pod, POST http://commerce-service:8109/v1/commerce/internal/sellers/<id>/approve with X-Internal-Service-Key and X-User-Id:<any uuid>. The seller goes live and is granted the seller role. The audit row names the invented actor.
Impact:         Any compromised service can put a seller live, mark KYC checked, approve listings and settle remittances. The audit trail names whoever the caller chose. The admin controls the founder approved are not where the decisions are made.
Fix:            Enable the AT path (uncomment SERVICE_CALLERS for admin-service and its key) and remove the money and identity actions (approve, reject, suspend, unsuspend, kyc/verify, products approve/reject, cod settle, payouts/pending) from the IK group, leaving only service reads (search docs, media-access). Add a NetworkPolicy to commerce-service.
Acceptance:     A key-only POST to /internal/sellers/:id/approve returns 404 or 401. The admin console's approve works through AT with step-up. A chart render test shows the commerce NetworkPolicy.
Effort:         M
```

```
ID:             COM-B-4
Title:          A seller goes live with no verified identity, and a buyer of a seller who never ships has no in-app route to a refund (00-map Q4 + Q5)
Severity:       P1
Category:       Fraud / payment
Evidence level: VERIFIED-READ for each step. SUSPECTED for the prod courier mode: the prod COURIER_PROVIDER comes from a secret (HEAD values-prod.yaml:80-85). Confirm the value set in prod.
Evidence:
  - P/onboarding.go:337-361. ApproveSellerByAdmin moves status to approved and grants the role; verification_status is not read.
  - S/onboarding.go:341-361. Same.
  - KYC is the stub in every ENV (C/cmd/server/main.go:358), which can at best reach `format_ok` (S/onboarding.go:443-460).
  - Reviewers cannot see the documents: the admin document view is AT-only, and AT answers 401 (HEAD values-prod.yaml:159).
  - C/internal/courier/courier.go:125-135. An unset or unknown COURIER_PROVIDER silently selects the stub, with no refusal in a managed ENV.
  - S/fulfilment.go:62-81. Under the stub, the seller's typed courier and tracking number are recorded.
  - S/seller_delivery.go:98-150. The seller can then mark the order delivered.
  - Buyer cancel after shipment is not in the matrix (00-map §10: shipped→cancelled is admin-only).
  - Returns are fenced (H/handler_p0.go:43-59).
  - No admin route cancels or refunds a shipped or delivered order (00-map §2.3).
How to reproduce:
  Under the stub courier: register as a seller with a well-formed fictitious PAN and bank account; an admin approves; list a product; a buyer pays; the seller POSTs /seller/orders/:id/ship with {"courier":"x","tracking_number":"y"}, then /mark-delivered. The buyer's cancel returns 409 CANCEL_NOT_PERMITTED, and there is no return or dispute route.
Impact:         Buyer money is lost in-app, and only an out-of-band Razorpay dashboard refund can return it. Payouts are not executed yet, so the platform still holds the money; the harm is to buyers and to trust, and it becomes direct loss once payouts exist.
Fix (smallest):
  (1) ApproveSellerByAdmin refuses unless verification_status is `verified`, or unless an explicit, audited override is given that the reviewer viewed the documents.
  (2) At boot in a managed ENV, refuse COURIER_PROVIDER other than `shiprocket` (or an explicit allowlist).
  (3) Add an admin "cancel and refund" route on shipped and delivered orders (the matrix already admits the admin actor) behind step-up.
Acceptance:     Tests: approve of a `format_ok` seller is refused; a prod-ENV boot with the stub courier exits; an admin refund of a delivered order writes one refund command.
Effort:         M
```

```
ID:             COM-B-5
Title:          Commerce and payments take no part in account deletion and offer no export (DPDP)
Severity:       P1
Category:       Privacy
Evidence level: VERIFIED-READ
Evidence:
  - identity-platform/services/auth-service/internal/purge/worker.go:46. DefaultRequiredServices does not include commerce or payments.
  - C/internal/consumers/ holds only payments consumers (payments.go, payments_p0.go). Nothing in commerce-service consumes user.purge_requested (shared/events/events.go:2141-2154). A grep for purge, erasure and deletion handlers in commerce-service found none.
  - What stays after a purge: addresses (plaintext until gated 1000), carts, favourites, reactions, reviews (with reviewer_id, title and body), seller rows with PAN and bank account ciphertext (plus plaintext under `dual`, COM-B-7), seller_documents, and order snapshots.
  - No export route exists for orders, addresses or reviews.
How to reproduce: Delete an account. After the purge reports complete, query commerce_db for the user_id: every row above remains.
Impact:         DPDP erasure and access obligations are not met for the commerce module. auth-service reports the account purged while commerce still holds the data.
Fix:            A commerce purge consumer that:
  - deletes addresses, cart, favourites and reactions;
  - anonymises reviews;
  - crypto-shreds the seller's PII and KYC (deletes their key-ring entry or ciphertext) once no open order or settlement needs them;
  - keeps orders, invoices and GST records under a documented tax-retention class (the CGST retention period) with the user link reduced to an id;
  - acks the purge.
  Add "commerce" (and "payments") to DefaultRequiredServices. Add a JSON export of orders, addresses and reviews.
Acceptance:     An integration test purges a buyer and a seller and asserts the per-table result. The auth purge waits for the commerce ack.
Effort:         L
```

### P2

```
ID:             COM-B-6
Title:          An approved seller can replace their payout bank account and business identity at any time, with no re-verification, audit row or notice
Severity:       P2
Category:       Authorization / account-takeover hardening
Evidence level: VERIFIED-READ
Evidence:
  - H/handler_onboarding.go:311-339. PUT /onboarding/step/payout.
  - S/onboarding.go:251-262. SavePayout seals and saves the account.
  - P/onboarding.go:209-248. The upsert does not read the seller's status and does not reset verification.
  - P/onboarding.go:132-143. SaveOnboardingBasic rewrites store_name, owner_name, email and phone on an approved seller.
  - P/onboarding.go:160-176. KYC documents re-upload resets only the document row to pending; the seller stays approved.
  - No audit insert on any of these paths (contrast commerce_admin_audit_log for admin actions).
How to reproduce: As an approved seller (or someone holding their session), PUT /v1/commerce/onboarding/step/payout with a new account. The answer is 204, and the seller stays approved and verified.
Impact:         Latent: no payout executor exists yet (00-map §9). The day payouts run, one stolen seller session redirects the seller's money without trace.
Fix:            When the seller is approved, a payout or identity change moves verification_status back to pending (or queues it for review), writes an audit row, and notifies the seller's email. Require a step-up (re-auth) on the route.
Acceptance:     A test shows a payout change on an approved seller leaves an audit row and verification pending.
Effort:         S-M
```

```
ID:             COM-B-7
Title:          Full bank account numbers and PANs are stored in plaintext in prod (KYC cutover at `dual`); a cancelled-cheque document_number (a bank account) and other ID numbers stay plaintext for good
Severity:       P2
Category:       Data protection at rest
Evidence level: VERIFIED-READ
Evidence:
  - HEAD deploy/services/commerce-service/values-prod.yaml:154-156. COMMERCE_PII_CUTOVER and COMMERCE_KYC_PII_CUTOVER are 'dual'.
  - S/kycpii.go:89. WritePlaintext comes from the cutover mode.
  - P/onboarding.go:221-235. account_number plaintext is written beside the ciphertext.
  - The gated scrub 1002 cannot run from the image (C/Dockerfile:20-25; 00-map §13).
  - P/onboarding.go:160-176. SaveOnboardingCompliance stores document_number as sent.
  - C/database/migrations/035_seller_kyc_pii_encryption.sql:226-234. Only Aadhaar-shaped numbers are refused, and `cancelled_cheque` is exempt.
  - C/database/gated/1002_kyc_pii_plaintext_scrub.sql:145-149. The scrub nulls only Aadhaar values; cancelled_cheque numbers (bank accounts) and passport, voter ID and DL numbers stay.
Impact:         A database or backup read exposes every seller's full bank account and PAN, which the KMS envelope was built to prevent.
Fix:            Launch prod (empty) directly at `ciphertext` for both scopes. Do not store document_number for cancelled_cheque (the sealed payout account already holds it) and seal or drop the other document numbers.
Acceptance:     manifest_contract_test (or a values test) asserts prod is `ciphertext`. A test shows a cancelled_cheque document saves with a null number.
Effort:         S (config) + S (code)
```

```
ID:             COM-B-8
Title:          Order and invoice events put the buyer's email and a 7-day bearer link to the invoice (name and address) on the shared topic
Severity:       P2
Category:       PII in events
Evidence level: VERIFIED-READ
Evidence:
  - S/shipments.go:686-702. commerce.invoice.issued carries invoice_url, a 7-day presigned URL from PresignedGetURL(…, 7*24h), and buyer_email.
  - S/checkout.go:207,225; S/order_lifecycle.go:157; S/shipments.go:329,604-608. buyer_email on other order events.
  - Topic social.events.v1 is readable with the one shared Kafka user (infra/terraform/modules/msk/main.tf:11-17).
Impact:         Any service, or any log or DLQ sink of the topic, gets buyer emails and working links to invoices showing the buyer's name and delivery address.
Fix:            Drop invoice_url and buyer_email from event payloads. Carry ids only, and let notification-service resolve the email and a short-lived URL at send time.
Acceptance:     An event contract test asserts the payload has no buyer_email or invoice_url.
Effort:         S
```

```
ID:             COM-B-9
Title:          No abuse limits on stock holds, checkout, coupon guessing or review posting beyond the edge's fail-open per-user limiter
Severity:       P2
Category:       Abuse / rate limiting
Evidence level: VERIFIED-READ (absence)
Evidence:
  - Each /v2/orders/checkout reserves stock for 20 min (P/checkout.go:680-704; 00-map §10).
  - Cart quantity has min=1 and no max (H/handler_cart.go:33).
  - No cap on open payment_pending orders per user: grep for a pending-order or quantity cap in S/checkout_p0.go, P/checkout.go and P/cart_items.go found none.
  - GET /cart/coupon-preview tells a valid code from an invalid one (P/coupons.go:249-285, classifyCouponMiss), which makes non-public seller codes guessable.
  - commerce has in-process limits only on reaction, vote and share (H/handler_engagement.go:45-114).
  - The edge allows 60 rps per user and fails open on Redis (00-map §0).
How to reproduce: Script N accounts that each check out a rival seller's whole stock and never pay. The listing shows out of stock, renewed every 20 minutes.
Impact:         A competitor can deny a seller's sales. Private coupon codes can be enumerated.
Fix:            Cap open payment_pending orders per user (for example 3) and quantity per line. Add per-user token buckets on checkout, intent, coupon-preview and review POST.
Acceptance:     A test shows the 4th open checkout is refused 429/409, and the coupon-preview limiter trips.
Effort:         S-M
```

```
ID:             COM-B-10
Title:          The stub courier is not refused in a managed ENV; its webhook accepts any unsigned call and its bookings are seller-typed
Severity:       P2
Category:       Authentication (third-party webhook) / config
Evidence level: VERIFIED-READ. SUSPECTED that prod runs the stub (the value is in a secret)
Evidence:
  - C/internal/courier/courier.go:125-135. The default is StubCourier.
  - C/internal/courier/courier.go:159-161. VerifyWebhook returns nil.
  - HEAD values-prod.yaml:80-85. COURIER_PROVIDER is from a secret.
  - qa is set to `stub` (00-map §8).
  - H/shipments.go:220-237. The webhook is public, and its 401 body echoes err.Error().
Impact:         Today the stub's ParseWebhook returns no updates, so an unsigned call changes nothing. The real issue is that in prod the stub enables COM-B-4's self-recorded delivery and fake AWBs with no alarm.
Fix:            A boot assertion: ENV in {prod, qa?} requires COURIER_PROVIDER=shiprocket and at least one of SHIPROCKET_WEBHOOK_TOKEN or _HMAC. Log the provider at start.
Acceptance:     A unit test on the config validator.
Effort:         S
```

```
ID:             COM-B-11
Title:          The payments NetworkPolicy admits the whole VPC CIDR, and the PSP webhook has no dedicated WAF rule or in-app rate limit
Severity:       P2
Category:       Network / ingress hardening
Evidence level: VERIFIED-READ for config. SUSPECTED that pods get VPC IPs (EKS VPC CNI default), which makes the pod allowlist moot
Evidence:
  - HEAD deploy/services/payments-service/values-prod.yaml:162-178. The pod allowlist includes api-gateway, and `ingressControllerCidrs: 10.30.0.0/16` admits the entire VPC.
  - :153-160 and infra/terraform/envs/prod/README.md:137. The psp-webhook ACL is not wired.
  - 00-map §2.2. No rate-limit middleware in payments.
  - The webhook body cap is 1 MiB (PAY/internal/http/handler.go:1157-1168).
Impact:         Any pod can reach payments on every route (still behind service tokens). The public webhook is limited only by the API ACL.
Fix:            Restrict the ingress-controller rule to the ALB subnets' CIDRs (or use the security-groups-for-pods source), and drop api-gateway from the allowlist (it never proxies /v1/payments). Add the 1000/5-min WAF rule scoped to /v1/payments/webhook inside the web ACL.
Acceptance:     render_test.sh asserts the CIDR is not the VPC CIDR, and is wired into CI.
Effort:         S
```

### P3

```
ID:             COM-B-12
Title:          Unapproved, unreviewed review text has no length limit and is published immediately
Severity:       P3
Category:       Input validation / abuse
Evidence level: VERIFIED-READ
Evidence:
  - H/handler_reviews.go:41-47. Title and body are *string with no max.
  - H/handler_reviews.go:69-71. IsPublished: true.
  - H/handler_reviews.go:90-92. sellerResponseReq has no max.
  - Clients escape: web shop has no dangerouslySetInnerHTML (grep over src/features/shop, src/app/shop); Compose Text on Android.
Fix:            Max 150 and 5000 runes, a 2000-rune seller response, and a per-user rate limit.
Acceptance:     An over-length review returns 400.
Effort:         S
```

```
ID:             COM-B-13
Title:          Webhook replay: the dedupe key is the unsigned X-Razorpay-Event-Id header, and there is no timestamp window
Severity:       P3
Category:       Webhook integrity
Evidence level: VERIFIED-RUN (TestRazorpay_CapabilitiesDeclareNoWebhookTimestamp passes) + VERIFIED-READ
Evidence:
  - PAY/internal/gateway/razorpay_provider.go:166-190. The HMAC covers the body only, and the event id is a header.
  - PAY/internal/service/p0.go:65-80. Refunds dedupe on the provider refund id, which is inside the signed body.
  - Captures are absorbed by the intent state machine (00-map §10).
Impact:         A captured signed body re-sent under a new id is re-applied. The effects found are idempotent (refund by refund id; capture on a succeeded intent). An attacker needs a real signed body.
Fix:            Also dedupe on (provider, payment_id, event type) from the signed body. Optionally reject created_at older than 24 h.
Acceptance:     A test where a replayed body with a fresh event id writes no second inbox effect.
Effort:         S
```

```
ID:             COM-B-14
Title:          Existence oracles and raw error text on order routes
Severity:       P3
Category:       Information disclosure
Evidence level: VERIFIED-READ
Evidence:
  - H/shipments.go:48-57. A stranger gets 403 on an existing order and 404 on a missing one (shipment, shipments, invoice).
  - H/handler_p0.go:519-533 + S/customer_payment.go:109. GET /orders/:id/payment answers 403.
  - Raw err.Error(): H/shipments.go:105 (SHIPMENT_FAILED), :197 (INVOICE_FAILED, includes payment_status), :235 (webhook 401); H/handler_orders.go:195 (LIST_ORDERS_FAILED), :285 (CANCEL_FAILED).
Fix:            Map "not associated" to 404 ORDER_NOT_FOUND, and route these errors through writeCommerceError.
Acceptance:     A test where a stranger's GET /orders/:id/invoice returns 404.
Effort:         S
```

```
ID:             COM-B-15
Title:          The anonymous share limiter keys on a client-spoofable IP
Severity:       P3
Category:       Rate limiting
Evidence level: SUSPECTED (confirm gin's ClientIP with default trusted proxies and the gateway's X-Forwarded-For handling)
Evidence:
  - C/cmd/server/main.go:517. gin.New() with no SetTrustedProxies, so gin trusts every proxy and reads X-Forwarded-For.
  - H/handler_engagement.go:45-114. Share is limited at 20/min per `ip:<ClientIP>`.
Fix:            SetTrustedProxies(nil) and use the gateway-stamped address.
Effort:         S
```

```
ID:             COM-B-16
Title:          B2B approve and reject are outside the /organizations fence, and a credit-terms approval confirms an order without payment
Severity:       P3 (latent: no organization order can be created while /organizations and /rfqs are fenced)
Category:       Authorization / payment (latent)
Evidence level: VERIFIED-READ
Evidence:
  - H/handler_organizations.go:29-30 (registration); S/organizations.go:268-296. CreditTermsDays > 0 moves the order to "confirmed" with no capture.
  - P/checkout.go has no organization_id or credit_terms write (grep).
Fix:            Add `/v1/commerce/orders/:id/approve|reject` to fencedRouteReason until B2B launches.
Effort:         S
```

```
ID:             COM-B-17
Title:          KYC uploads may be ordinary image-kind media in every ENV
Severity:       P3
Category:       Data protection
Evidence level: SUSPECTED (confirm what media-service does with image kind: thumbnails, moderation copies, CDN class)
Evidence:
  - HEAD values-prod.yaml:136. COMMERCE_KYC_REQUIRE_DOCUMENT_KIND is "false".
  - S/onboarding.go:188-193. kycMediaKinds then allows image.
Fix:            Turn the flag on once the Android document upload ships, and re-upload legacy image KYC as document kind.
Effort:         S (flag) + client release
```

## 4. Checked and sound

All of these are VERIFIED-READ unless marked otherwise.

**Payments and money**
- **Payable amount.** Checkout reads no price, discount, shipping or total from the body; `expected_total_minor` is compared only (`H/handler_p0.go:370-378`). The intent amount is the server's order total (`S/checkout_p0.go:461-469`). The order currency is hard-coded INR (`P/checkout.go:536-547`), so a seller's variant `currency_code` cannot change it.
- **Marking paid.** Only the payments event marks an order paid. The stub confirm route is unregistered and fenced outside a local ENV (`H/handler.go:180-182`; VERIFIED-RUN fence tests). Neither client marks paid (00-map §11).
- **Webhook signature.** HMAC-SHA256 over the exact raw body, hex, `hmac.Equal`, fails closed with no secret, and requires an event id. The body cap of 1 MiB applies before verification (`PAY/internal/gateway/razorpay_provider.go:166-190`, `PAY/internal/http/handler.go:1150-1225`). VERIFIED-RUN.
- **Provider-side money check.** payments-service compares amount and currency inside the terminal transaction, and a mismatch writes nothing and returns 500 (`PAY/internal/service/p0.go:96-160`).
- **Refunds.** A refund cannot exceed the intent: CHECK `refunded + reserved <= amount` (`PAY/database/migrations/007…:185-193`). Cancel refunds only what is still owed (`P/p0.go:784-800`). A refund webhook dedupes on the provider refund id (`PAY/internal/service/p0.go:165-175`). In prod, a refund requires a service token with `refund.create`, not the legacy key (`PAY/internal/http/handler.go:240`).
- **COD.** COD cannot be ordered: the method allowlist is `upi` and `card` (`shared/paymentmethod/paymentmethod.go:6-50`, `H/handler_p0.go:329,413`, `S/checkout_p0.go:275`).
- **Card data.** No card data touches commerce or payments servers: the client opens Razorpay Checkout with the server `order_id` and `key_id` (`PAY/internal/gateway/razorpay_provider.go:152-162`). The webhook handler never logs the body or the signature (`PAY/internal/http/handler.go:1175-1179`).

**Coupons**
- A seller coupon applies only to that seller's goods (`P/coupons.go:296-299`).
- Targets are checked to be the seller's own products (`P/coupons.go:1103-1112`).
- Capacity is claimed atomically by a conditional UPDATE (`P/coupons.go:424-445`).
- The per-buyer cap is counted in the checkout transaction (`P/coupons.go:301-311`).
- Codes are globally unique (`C/database/setup.sql:323`).

**Authorization**
- Buyer object routes: orders, items, cancel, intent, status and addresses are scoped to the customer (section 1). Seller object routes: products, variants (except COM-B-1), coupons, fulfilment and review responses are scoped to the seller.
- Mass assignment: PATCH `/products` is decoded against an explicit allowlist and refuses unknown keys (`H/handler_products.go:66-180`). PATCH `/variants` writes only the store allowlist; status is limited to a vocabulary; selling price must stay ≤ MRP; price pairs are normalised (`P/variants_stock.go:161-205`, `S/reviewedcontent.go:423-480`).

**Injection and output**
- **SQL injection.** Every dynamic SQL fragment found builds placeholders only (`P/products.go:395-456,505-516`, `P/shipments.go:285-296`, `P/variants_stock.go:186-205`, keys from a fixed allowlist). Review sort goes through `reviewOrderBy` (`P/reviews.go:59`).
- **Invoice.** Rendered with `html/template` (`Architecture/shared/invoice/generator.go:11,168`), with no `template.HTML` or URL contexts. `wkhtmltopdf --enable-local-file-access` (`shared/invoice/pdf.go:39`) is not in the commerce image, so the PDF path is not exercised.
- **XSS on clients.** Web shop: no `dangerouslySetInnerHTML`. `tracking_url` comes from the courier adapter only (`S/shipments.go:282-283`; `applyManualBooking` clears it, `S/fulfilment.go:62-81`).

**Edge and service authentication**
- **Internal routes.** `/internal/` is refused at the edge under every decoding (`api-gateway/pkg/internalroutes/internalroutes.go:58-110`). The commerce internal guard fails closed with no key, constant-time (`H/internal_guard.go:56-74`). VERIFIED-RUN.
- **Courier webhook (Shiprocket).** A constant-time token or HMAC, rejected when unconfigured (`C/internal/courier/shiprocket.go:61-89`).
- **commerce → payments.** Ed25519 service token with op, application allowlist and owner-domain checks; 404 for "not yours" (`PAY/internal/http/handler.go:236-240,900-930`). User intent creation is refused in prod (`:280-300`).

**KYC and logging**
- **KYC media ownership** is checked through media-service and is mandatory in managed ENVs (`S/onboarding.go:131-176`; 00-map §2.1).
- **Aadhaar** numbers are refused in code and by trigger (`S/onboarding.go:146-148`, migration 035:297-314).
- **Payout account reads** return only the last four digits to the seller (`P/onboarding.go:103`). The stub KYC never writes `verified` (`S/onboarding.go:443-460`).
- **Android logging.** OkHttp logging is BASIC at most and NONE in release (`mobile/android/core/network/…/NetworkModule.kt:105-118`). No `Log.*` of account, IFSC, PAN, phone or address was found in the commerce, seller or payments modules. The payout screen is kept out of screenshots (00-map §11.1).

## 5. Not verified

- **Prod runtime values.** The values of the prod secrets `COURIER_PROVIDER`, `RAZORPAY_*` (test vs live) and the cookie `Domain` per ENV were not checked: no deployed environment or secret store was accessed. This affects COM-B-4, COM-B-10 and the reels-77 row.
- **Whether gated 998-1002 ran anywhere** (00-map Q1). No database was accessed.
- **Seller stock and seller product reads, addresses, delivery partner writes.** These ownership checks rely on the 00-map pass; I did not re-read `P/inventory_adjust.go`, `P/addresses.go` or `P/delivery_operations.go` line by line.
- **Try-on effect bundle validation, bulk import, RFQ and affiliate.** Fenced or out of scope; not read.
- **Price-tier bounds.** `PUT /variants/:id/price-tiers` is live (`H/handler_variants.go:236`) and takes rupee floats; its bounds and its readers were not read. UNVERIFIED.
- **media-service handling of `document` kind under the gate** (COM-B-1 SUSPECTED part, COM-B-17).
- **Android:** certificate pinning, Razorpay SDK version and WebView use were not reviewed. **Web:** the `/shop/sell` forms were not reviewed for open redirects.
- **Kafka topic ACLs** at the broker were not inspected beyond Terraform (no MSK access).
- **No Android or web tests were run.**

## 6. Notes for other tracks

- **Track A (money).** With a bank offer (`captured < amount`), a cancel refunds `final_amount_minor − settled`, which can exceed the money actually captured (`P/p0.go:784-800` vs `PAY` CHECK on `amount_minor`, not `captured_minor`). Offers are off everywhere. Also COM-B-4's missing admin refund route.
- **Track A / GST.** A seller's pickup or business address drives place of supply (`P/checkout.go:497-505,1069-1082`) and is editable after approval without review; GSTIN's state is not cross-checked.
- **Track C (ops).** Enabling AT (`SERVICE_CALLERS`) is a prerequisite for COM-B-3 and for admins to see KYC documents at all. Add boot assertions for the courier mode (COM-B-10) and the KYC cutover (COM-B-7). `render_test.sh` is not in CI.
- **Track D (tests).** No test covers a cross-seller variant image (COM-B-1), a forged or blank-field `payment.succeeded` (COM-B-2), or a key-only call to `/internal/sellers/:id/approve` (COM-B-3).
- **Platform.** The single shared Kafka SCRAM user (COM-B-2, COM-B-8) and the shared internal key (COM-B-3) are platform-wide. Fixing them once covers food, dating, rider and doorstep payment consumers too.
