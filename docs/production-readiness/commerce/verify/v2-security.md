# Commerce verify v2: security findings (adversarial re-check)

Date: 2026-11-17. Verifier read only each finding's Title and Evidence, then
tried to disprove it from code at HEAD (be57a46bf). Commerce-service and
media-service have no uncommitted changes; admin-service, payments admin
files and commerce values files were read with `git show HEAD:`.
Paths: C = Architecture/services/commerce-service, MS = .../media-service.

---

## COM-B-1: variant image_media_id written unchecked, then vouched for to media-service

Verdict: CONFIRMED. Evidence level: VERIFIED-READ (whole chain).

Checked:
- C/internal/http/handler.go:111 `PATCH /variants/:variantId`; handler_variants.go:150-176 passes the raw map on.
- C/internal/service/catalogue_products.go:525-556: product ownership only; `checkVariantPatch` (reviewedcontent.go:447-480) checks status, sku and price, not media. No `verifyMedia` call. Contrast: CreateProduct calls `verifyMedia` at :131-136.
- C/internal/store/postgres/variants_stock.go:161-172: `image_media_id` is in the allowlist.
- C/internal/store/postgres/storefront.go:767-793 (`VisibleProductMediaIDs`): allows a media id referenced by `pv.image_media_id` when the listing is live OR when `seller_user_id = viewer`. Served at C/internal/http/handler_storefront.go:67-68.
- MS/cmd/server/main.go:537-543: commerce is one of five authorities. MS/internal/delivery/authz.go:408-435 and :437+ (batch): the first authority that says yes wins. HEAD still has this rule.
- Things that could have made it false, and do not: MS/internal/service/document.go:140 `ViewerReadDenies` refuses only `document` kind and dating-scoped assets (so KYC documents are safe, which settles the track's open question). Anonymous-scope assets are streamed through MS's own routes but still go through the same gate. Public-prefix keys need no gate anyway. No media-service rule ties an asset's authority to its own surface.
- Revalidation: reviewedcontent.go:376-383 counts an image change as substantive, so on an APPROVED listing the swap needs `revalidate:true` and sends the listing back to review (gateApprovedEdit :120-135). The public arm therefore needs a moderator to approve a listing that shows the foreign image. The seller-only arm needs no review.

What an attacker needs: an account with a seller row (onboarding is self-serve; `sellerForCaller`, sellerresolve.go:34-51, needs no approval), one draft product (POST /products needs no approval), and the victim's media UUID. UUIDs are not guessable, so the attacker must have seen the id once: a former follower, a former chat or group member, someone blocked later, or anyone the post's JSON reached before it was made private or deleted. No cluster access needed.

Not checked: whether a deleted post's asset row still exists in media-service (it decides whether "deleted post" is in scope).

Corrected severity: P0 (was P1). It lets a self-registered seller read another user's private media, and publish it if a moderator misses it. That matches the P0 definition. The id-knowledge precondition limits scale but does not change the class.

---

## COM-B-2: shared Kafka login, and payment.succeeded accepted with blank currency, payer and intent

Verdict: CONFIRMED. Evidence level: VERIFIED-READ.

Checked:
- infra/terraform/modules/msk/main.tf:11-17 says "The one application user"; :231-233 associates one SCRAM secret; :240-241 says the SCRAM user has full access on a cluster without ACLs. `git grep` for kafka ACL resources under infra/ and deploy/ finds none. Every service's values file reads `kafka_sasl_username/password`.
- C/internal/consumers/payments_p0.go:346-371: filters only on `reference_type == "order"`. No producer or source check, and no signature on the envelope (shared/paymentevents has none).
- C/internal/store/postgres/p0.go:226-230: `CheckCapture(..., AllowUnstated)`. shared/paymentevents/check.go:79-95: under AllowUnstated, a blank currency, nil payer and blank intent are skipped; only the amount must match.
- p0.go:918-925 `captureIntentToBind`: an empty intent binds nothing and does not refuse. :877-888 `decideCapture`: with no capture landed and the order payable, the verdict is `captureApplies`.
- Cross-check with payments-service: commerce has `GetIntent` (C/internal/payments/p0client.go:247), but it is used only on the checkout and confirm paths (checkout.go:402, checkout_p0.go:454/503), never in the consumer.
- What payments-service always sets: payments-service/internal/service/service.go:566-572 publishes the whole intent (id, currency and payer are always present). A strict policy would therefore cost nothing. payments-service's own consumer of the same topic re-reads its ledger and does not trust the event (payments-service/internal/consumers/commerce.go:22-25), so the safer pattern already exists in this codebase.

What an attacker needs: an in-cluster foothold in any pod that holds the Kafka secret, plus an order id and its exact total (a buyer knows both for their own order).

Corrected severity: P1 (unchanged). It is a money-integrity break, but it needs an in-cluster foothold.

---

## COM-B-3: internal-key admin routes with an unverified X-User-Id actor

Verdict: CONFIRMED. Evidence level: VERIFIED-READ.

Checked:
- C/internal/http/handler_onboarding.go:36-67: approve, reject, request-changes, suspend, unsuspend, kyc/verify, payouts/pending, products approve/reject and COD settle all sit behind `requireInternalKey` only.
- C/internal/http/internal_guard.go:42-74: `requireActor` falls back to `X-User-Id` when no admin token is present. The key compare is the only gate.
- The key is shared by every service: infra/terraform/modules/auth-keys/main.tf:25,65 is one `random_password`, and 158 HEAD values files reference `remoteRef: internal_service_key`. The gateway injects the same key on user traffic (C/internal/http/gatewaytrust.go:63-114).
- Edge exposure: api-gateway refuses every `internal` path with 404 (api-gateway/pkg/internalroutes/internalroutes.go:21,192-201). So this needs an in-cluster foothold, not just internet access.
- NetworkPolicy: `git grep NetworkPolicy` at HEAD finds it only in payments-service values (prod and qa). Commerce has none.
- admin-service at HEAD (handler_commerce.go): step-up on suspend, unsuspend and kyc/verify, step-up plus two-person on COD settle. Seller approve and product approve have NO step-up even there.
- The admin-token path is not live: SERVICE_CALLERS is commented out in commerce qa, staging, prod, azure-prod and azure-staging, and admin-service's ADMIN_SERVICE_TOKEN_KEY is commented out (admin-service values-prod.yaml:47-61). Correction: the console sees 503 PRODUCT_UNAVAILABLE from admin-service (admin-service cmd/server/main.go:130). It does not see commerce's 401, because admin-service never sends the call.

Corrected severity: P1 (unchanged). It needs an in-cluster foothold in any service, and the result is a forged, falsely attributed seller, KYC and remittance decision.

---

## COM-B-4: seller live without verified identity, and no refund route after a seller-marked delivery

Verdict: CONFIRMED for the code path. DOWNGRADE-TO-SUSPECTED only for "prod runs the stub courier". Evidence level: VERIFIED-READ.

Checked:
- C/internal/store/postgres/onboarding.go:337-361 and service/onboarding.go:341-361: approve never reads verification_status.
- C/cmd/server/main.go:358 `svc.WithKYC(kyc.StubValidator{})` is unconditional. onboarding.go:455-465: the stub can reach only `format_ok`, never `verified`.
- Document view is AT-only through admin-service (handler_commerce.go, `documents/:documentId/view`), and AT is not enabled (see COM-B-3).
- C/internal/courier/courier.go:125-135: unknown or empty COURIER_PROVIDER means stub. No managed-ENV refusal; values-prod.yaml:83-85 says so in a comment. The value comes from a secret, so the prod mode is unknown.
- service/fulfilment.go:62-81: under the stub, the seller's courier and AWB are stored. service/seller_delivery.go:98-150: the seller can mark shipped orders delivered.
- Returns: `POST /orders/:orderId/returns` is fenced (http/handler_p0.go:110-118) along with the /returns prefixes. Buyer cancel after shipment returns 409 CANCEL_NOT_PERMITTED (handler_p0.go:763).
- payments-service admin (HEAD admin_routes.go:8) can only RESOLVE existing refund commands. It cannot create one for a delivered order. admin-service at HEAD has no order cancel or refund route.

What an attacker needs: a seller account that an admin approves (an admin who cannot see documents), and prod running the stub courier.

Corrected severity: P1 (unchanged). Buyer money is stuck until an out-of-band PSP refund. It needs admin approval, and the prod courier mode is unconfirmed.

---

## COM-B-5: commerce and payments take no part in deletion or export (DPDP)

Verdict: CONFIRMED. Evidence level: VERIFIED-READ.

Checked:
- identity-platform/services/auth-service/internal/purge/worker.go:46 (HEAD): DefaultRequiredServices does not list commerce or payments. No REQUIRED_PURGE_SERVICES override in deploy/ or docker compose.
- C/internal/consumers/ holds only payments.go and payments_p0.go. `git grep` for purge_requested, deletion_requested, user.purged, user.deleted and anonymise finds nothing in commerce-service or payments-service.
- Export: the only commerce export is the seller earnings CSV (fenced, under /seller/earnings). admin-service exports.go only records a request and pulls nothing from commerce.
- Retention: orders, invoices and GST records must be kept. CGST Act s.36 with Rule 56 requires roughly 72 months from the annual-return due date, income-tax books are kept for a similar period, and DPDP s.8(7) permits retention that law requires. So the correct design is a partial purge (addresses, cart, favourites, reviews and KYC erased or anonymised; tax records kept with a minimal link), as the finding's fix describes. Full deletion would be wrong.

What an attacker needs: nothing. This is a compliance gap, not an exploit.

Corrected severity: P1 (unchanged). It is a statutory obligation and auth-service reports "purged" while commerce still holds the data. It could drop to P2 if launch is before the DPDP Rules' operative date, which I did not verify.

---

## COM-A-3: no working tool for seller approval or product moderation

Verdict: CONFIRMED, with one wording fix. Evidence level: VERIFIED-READ.

Checked:
- SERVICE_CALLERS is commented out in all five Helm values files at HEAD (qa:163, staging:83, prod:159, azure-prod:58, azure-staging:49). C/internal/http/admin_token.go:121-171 refuses with 401 when `h.verifier.Callers() == 0`.
- COMMERCE_PRODUCT_AUTO_APPROVE is "false" in prod and qa and unset (so false, cmd/server/main.go:333) in staging.
- Corrections: (1) the console actually gets 503 PRODUCT_UNAVAILABLE, because admin-service has no signing key and never calls commerce; (2) the local docker-compose stack defaults auto-approve to true (Architecture/docker/docker-compose.yml:2028), so "every environment" holds for the Helm environments only.

Corrected severity: P1 (unchanged). It is a launch blocker for seller onboarding. The only workaround is the IK path from COM-B-3.

---

## COM-F-2: courier webhook verification and routes untested

Verdict: CONFIRMED. Evidence level: VERIFIED-READ.

Checked:
- C/internal/courier contains only shiprocket_etd_test.go and shiprocket_rate_test.go. A repo-wide `git grep` for VerifyWebhook, shipments/webhooks, courier-callback and ShipmentWebhook in tests finds only two test doubles that return nil (checkout_journey_integration_test.go:67, contracts_integration_test.go:245).
- Code exists and looks right: shiprocket.go:63-89 refuses when no secret is set, and compares the token and HMAC in constant time. Routes are at http/shipments.go:21,28. The handler answers 401 on a verify error (:234-236).
- Mitigation found: under the stub, `ParseWebhook` returns no updates (courier.go:152-154), so an unsigned stub webhook changes nothing. The risk is a regression on the Shiprocket path.

Corrected severity: P1 (unchanged). A regression would let anyone mark orders delivered, and the test is cheap to add.

---

## COM-F-3: object-id isolation untested on invoice, shipments, items and seller order detail

Verdict: CONFIRMED as a test gap. The authorization checks EXIST on all four routes, so there is no P0. Evidence level: VERIFIED-READ.

Per route:
- GET /orders/:id/invoice and POST invoice: `requireOrderRole` (shipments.go:183-215). OrderActor (service/order_lifecycle.go:51-79) requires the customer or a seller with an item on the order. POST requires the seller. CHECK EXISTS.
- GET /orders/:id/shipment and /shipments: `requireOrderRole(..., false)` (shipments.go:132-160). CHECK EXISTS.
- GET /orders/:id/items: service/orders_read.go:129-143 checks `order.CustomerUserID != userID` and answers 404. Customer only, so a seller is refused too. CHECK EXISTS.
- GET /seller/orders/:id: `sellerForCaller` plus `sellerLines` (orders_read.go:289-298) answers 403 when the caller has no line. CHECK EXISTS. The answer is 403, not 404, which confirms the order exists (an oracle, already filed under track B).
- Tests: the only invoice test is the buyer's own 200 (contracts_fixtures_integration_test.go:676). Items: the buyer only (:670). Seller detail: the owning seller only (:908; ownorder_integration_test.go:342-347). Shipment GET: no stranger case. The finding stands.
- Side observation (P2, not in the finding): on a multi-seller order, any seller with one line reads the whole invoice (buyer name and address, and the other sellers' lines) and every seller's shipments. OrderActor does not scope by seller.

Corrected severity: P1 (unchanged) as a missing regression net. There is no missing check.

---

## Summary

| ID | Verdict | Corrected severity | Needs | Why |
|---|---|---|---|---|
| COM-B-1 | CONFIRMED | P0 (from P1) | Self-serve seller row + victim's media UUID; public arm also needs a moderator approval | Reads another user's private media through commerce's yes in a first-yes-wins gate |
| COM-B-2 | CONFIRMED | P1 | In-cluster foothold (shared Kafka creds) + order id/total | Forged payment.succeeded confirms the order; blank fields accepted, no intent re-read |
| COM-B-3 | CONFIRMED | P1 | In-cluster foothold (shared internal key); edge refuses /internal | Forged, falsely attributed approve/KYC/settle; commerce has no NetworkPolicy |
| COM-B-4 | CONFIRMED (prod courier SUSPECTED) | P1 | Seller account + admin approval + stub courier in prod | Seller can mark delivered under stub; no refund or return route afterwards |
| COM-B-5 | CONFIRMED | P1 | Nothing (compliance) | Not in purge required set, no consumer, no export; tax records must be kept |
| COM-A-3 | CONFIRMED (console sees 503, not 401) | P1 | n/a | AT path off in all Helm envs, auto-approve off |
| COM-F-2 | CONFIRMED | P1 | n/a | Zero tests on Shiprocket verify and webhook routes; stub webhook is a no-op |
| COM-F-3 | CONFIRMED (test gap only) | P1 | n/a | Checks exist on all four routes; stranger cases untested |
