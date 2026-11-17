# Commerce (MStore / MSeller): Track F, tests against the user journeys

- **Audit date:** 2026-11-17. Read-only.
- **Trees:**
  - Backend and Android: `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `be57a46bf`. The map (`00-map.md`) was built at `d83616ad6`; the four commits since change e2e, Android manifests, Android lint and media-worker, not commerce or payments (`git log d83616ad6..HEAD`). `git status --porcelain` showed 683 dirty paths before and after this track.
  - Web: `C:\workspace\wt\pb-echo` @ `1b596b4`, clean before and after.
- **What was run (all VERIFIED-RUN):**
  - `go test -count=1 -json ./...` in commerce-service, payments-service, wallet-service and admin-service, with no DSN set, so `//go:build integration` files do not build and untagged DB tests skip.
  - `bash scripts/verify/android.sh --fast :core:commerce :feature:commerce :feature:seller :core:commerce-ui :core:payments` (PASS, 42 s, mostly Gradle up-to-date), then `./gradlew --offline :<module>:testDebugUnitTest --rerun` for all five, so every result below was executed today (XML timestamps 2026-11-17T15:17-15:19Z).
  - `bun test src/features/shop src/app/shop` in pb-echo.
  - Read-only `gh run list`, `gh run view`, `gh run view --log`.
  - No Docker, database, device or deployed environment was used. No integration test was run locally.
- **Caveat on the working tree:** Go tests ran on the working tree. commerce-service has no uncommitted files. payments-service has 8 modified files and 1 untracked test (`internal/store/postgres/refund_attention_test.go`), and admin-service has about 30 modified files including `handler_commerce_test.go`, `handler_payments_test.go` and `handler_coupons_offers_test.go`. Nothing failed, so no failure needs attributing; the payments and admin counts include those uncommitted edits.
- **Evidence tags:** VERIFIED-RUN (a command and its output), VERIFIED-READ (a file:line read), SUSPECTED, UNVERIFIED.
- Paths are relative to `Architecture/services/` unless they start with another top-level folder. `C/` = `commerce-service/internal/`, `PAY/` = `payments-service/internal/`. Web paths are relative to `pb-echo`. Android paths are relative to `mobile/android/`.

## 1. Inventory per layer

### 1.1 Go unit (local run, VERIFIED-RUN)

| Service | Pass | Skip | Fail | Notes on the skips |
|---|---|---|---|---|
| commerce-service | 989 | 13 | 0 | All 13 are untagged local-delivery DB tests that need `COMMERCE_DELIVERY_TEST_DSN` (`store/postgres` `TestDeliveryOperationsDB*`, `TestDeliveryDB*`, `TestDeliveryCoverageDB*`). Local delivery is off in every environment (map 8); they run in CI through `extra_dbs` (`scripts/ci/integration-services.yaml:159-161`). Do not matter for launch. |
| payments-service | 487 | 1 | 0 | `TestDB_UpdateStatusByProviderRef_ReturnsFullIntent`, the untagged `store_db_test.go` whose usage comment points at `commerce_db` (map 12). Minor. |
| wallet-service | 31 | 64 | 0 | All DB tests. Wallet is not used by commerce or payments (map 1.1); irrelevant here. |
| admin-service | 828 | 0 | 0 | Working tree. Commerce/payments test functions at HEAD: `handler_commerce_kyc_test` 12, `handler_payments_test` 11, `handler_coupons_offers_test` 8, `handler_commerce_kyc_pdf_test` 4, `handler_commerce_kyc_back_test` 3, `handler_commerce_test` 3, `handler_commerce_delivery_test` 1, plus `twoperson_integration_test` 4 (integration). |

Counts include subtests (`go test -json` pass events that carry a Test name). Package build failures: none.

Unit files with the most weight for money: `C/tax/gst_test.go` (15: golden inclusive extraction, CGST+SGST vs IGST, property `ComponentsAlwaysSumToCharged`, `RefundNeverExceedsAndAlwaysReconciles`), `C/store/postgres/capture_verdict_test.go` (redelivery vs late vs duplicate capture table), `order_transitions_test.go` (`TestPaymentStatusTransitions`, `TestOrderPayable`), `wirecontract_test.go:147` (`TestCustomerCanCancelMatchesTheCancellationMatrix`), `refund_settlement_test.go`, `cancel_refund_test.go`, `C/service/invoice_money_test.go`, `C/http/fence_p0_test.go`, `shared/paymentevents/check_test.go` (amount, currency, payer, intent tuple under both policies), `PAY/http/handler_test.go:308,393` (webhook fails closed, duplicate is a no-op), `PAY/gateway/provider_contract_test.go:51-168` (Razorpay HMAC verify, empty secret rejects everything, missing signature, no timestamp declared).

### 1.2 Go integration (`//go:build integration`; VERIFIED-READ, CI execution see section 4)

- **commerce-service: 86 files** of 205 test files. Key ones and what they prove:
  - `store/postgres/proof_integration_test.go`: C1 fifty concurrent checkouts on one unit `:262` with a negative control `:407`; C2 same idempotency key twenty times `:467` and same key with a different body `:572`; C3 reserve, cancel, re-reserve `:602`; C6 amount mismatch does not mark paid `:653`; C7 late capture after expiry refunds rather than fulfils, and stock is released `:769`; C9 cart mutation racing checkout `:866`; GST stored components sum to charged `:899`; cross-user cancellation refused `:1004`; fenced surfaces refuse writes `:1036` + negative control `:1119`; illegal order transition rejected by the DB trigger `:1148`.
  - `proof_n_integration_test.go`: N6 omitted, negative or stale `expected_total_minor` creates nothing (`:62-121`) + negative control `:148`; N1 edited address invalidates the quote `:204`.
  - `quotebreakdown_c3_integration_test.go` (10: quote equals checkout across tax rates and coupons, price change after quote commits nothing, quote for one method refused for another, quoting a coupon does not claim it).
  - `paymentmethod_c3_integration_test.go` (UPI and card only; COD refused with no durable effect).
  - `duplicate_capture_integration_test.go` (7), `payment_mismatch_integration_test.go` (9), `payment_failed_intent_integration_test.go` (4: failure on the bound intent fails the order, redelivery is a no-op, a failure after the capture changes nothing `:100`).
  - `cancel_refund_integration_test.go` (3: cancel of a paid order refunds the total and restocks; of a partly refunded order, the remainder), `refund_settlement_integration_test.go:17` (partial then remainder), `expiry_sweep_integration_test.go:17` (backoff), `coupon_release_integration_test.go` (5), `coupons_integration_test.go` (14, including `TestCouponLastUseRace :482`, `TestCouponPerBuyerLimit :454`).
  - `order_fulfilment_db_test.go` (4) and `order_transitions_db_test.go` (2: concurrent `MarkOrderPaid` converges, guards).
  - `http/checkout_journey_integration_test.go` (quote then checkout over HTTP; a total the server never stated is refused and writes nothing `:297`), `seam_journey_integration_test.go` (4), `ownorder_integration_test.go` (6), `seller_fulfilment_integration_test.go` (10), `reprice_contract_integration_test.go` (10, including `TestAnotherSellerCannotRepriceYourProduct :214`, `TestAPriceCannotBeSetToZero :174`), `c1_behaviour_integration_test.go` (6), `contracts_fixtures_integration_test.go` (golden fixtures, including `order_payment_get_403_not_customer :692`, `order_cancel_post_404_not_owner :849`, `seller_coupon_patch_404_not_owner :972`).
  - `service/invoice_issue_integration_test.go` (3), `invoice_reissue_integration_test.go` (3), `kycdocument_*`, `store/postgres/kycpii_integration_test.go`.
- **payments-service: 33 integration files** of 67 test files. Key: `service/moneytruth_c3_integration_test.go` (a genuinely HMAC-signed Razorpay body through the real adapter and `ApplyWebhook`: complete tuple settles `:282`; no currency, other currency, no amount, wrong amount cannot settle `:302-346`), `openorder_a1_*`, `reconcile_n2_*` (lost webhook recovered `:348`; wrong amount or currency refused), `failedretry_d1_*`, `refundworker_rw_*`, `refundrequired_rr_*`, `owedcapture_oc_*`, `orderduplicate_od_*`, `offers_integration_test.go:610` (provider over-refund refused), `store/postgres/proof_integration_test.go:71` (C4 duplicate webhook applies once), `:296` (C8 duplicate provider refund credits once), `proof_b_integration_test.go` (B2 low or currency-mismatched capture writes nothing, with a negative control `:194`; B3 refund webhook atomic and the same refund under a NEW event id credits once `:309`).

### 1.3 End to end and smoke

- `Architecture/tools/integration/commerce_*_test.go` (VERIFIED-READ): `TestE2E_Commerce_BrowseAndCart` (`commerce_e2e_test.go:19`), `TestE2E_Commerce_FullJourney` (`commerce_journey_test.go:39`), `TestE2E_Commerce_PrepaidStub` (`commerce_prepaid_test.go:41`).
- E2E commerce tier in run 44093031010 (`87a34d23d`, job 134072872120): all three `--- PASS` (0.01 s, 1.31 s, 3.22 s). The log shows every step, from onboarding through admin approval, UPI checkout, idempotent replay, stub confirm, stranger refusals, closed `/v1/payments` edge and webhook 503, to a stub-courier shipment and `SHIPMENT_NOT_SELLER_MANAGED` (VERIFIED-RUN).
- What the e2e does **not** do: a signed webhook (it asserts 503 on a stub payments-service, `commerce_prepaid_test.go:139-165`), payment failure, cancel, refund, delivered, invoice, reviews, a second seller, coupons.
- Dev smoke: `scripts/smoke/dev-smoke.mjs:182,886-952` stops at `shop.quote`. It runs by hand; no workflow calls it (VERIFIED-READ).
- Load: `Architecture/tools/loadtest` holds `README.md`, `lib`, `long-video`, `reels` only; no file under it mentions commerce or checkout, and no `func Benchmark` exists in commerce-service or payments-service (VERIFIED-RUN, `ls` + `git grep`). **No load test exists for commerce or payments.**

### 1.4 Android unit (VERIFIED-RUN, forced re-run)

| Module | Test files | Tests | Skip | Fail |
|---|---|---|---|---|
| `:core:commerce` | 15 | 144 | 0 | 0 |
| `:feature:commerce` | 16 | 117 | 0 | 0 |
| `:feature:seller` | 17 | 185 | 0 | 0 |
| `:core:commerce-ui` | 4 | 17 | 0 | 0 |
| `:core:payments` | 8 | 50 | 0 | 0 |
| **Total** | 60 | **513** | 0 | 0 |

- Journey-relevant: `CheckoutJourneyTest` (submits exactly the quoted total, never adds tax, replayed event consumed once, event for another order ignored), `CheckoutProcessDeathTest` (8: lost response then recreation resubmits the same key and yields one order; after order creation it fetches and never checks out again; server-reported paid renders paid), `OrderDetailPayNowTest` (a closed sheet polls the server), `PaymentCoordinatorTest` (never reports paid from the SDK callback alone), `PaymentLauncherOneFlightTest` (8), `SellerOrderDetailViewModelTest`, `EditPriceTest`.
- `CommerceContractFixtureTest` compares 71 fixtures byte for byte with commerce-service's `testdata/contracts`. I re-checked: 71 of 71 identical (VERIFIED-RUN, `cmp`).
- Instrumented: none in any commerce module (map 11.1).

### 1.5 Web unit (VERIFIED-RUN)

- `bun test src/features/shop src/app/shop`: **642 pass, 23 skip, 2 fail, 37 test files**, 6.23 s. The 160 files under `__tests__` include 122 JSON fixtures.
- The 2 failures are golden-fixture drift against the backend (COM-F-7):
  - `couponsOffers.contract.test.ts:68`: `admin/coupon_patch_200.json` has `expires_at` 2026-11-15 locally, 2030-11-15 in the backend (backend commit `13676d3a5`).
  - `orders.contract.test.ts:109`: `orders/order_shipments_get_200` lacks `seller_managed` (added in backend commit `b610342a1`).
- Behaviour vs text: only `payments.test.ts:102-109` reads source files (it asserts no payment source reads `process.env`). The rest test pure functions: quote mapping, `expected_total_minor` copies the quote (`checkout.test.ts:76-84`), Idempotency-Key reuse across a retry and a reload (`:91-133`), the payment poll schedule and stop rules (`paymentPoll.test.ts:30-79`). Nothing mounts the checkout page or `startPayment`.
- web-ci run 44078366557 on `feat/echo-web` @ `1b596b4`: success, `5830 pass, 100 skip, 0 fail` across 379 files (whole repo).

## 2. Journeys: which test covers which step

| Journey | Covering tests | What they assert | Gap |
|---|---|---|---|
| **J1 Full purchase, UPI, to delivery** | e2e `FullJourney`; `checkout_journey_integration_test.go:218`; `seam_journey_*`; `c1_behaviour_integration_test.go:219` (stub courier delivers, reviews become possible); Android `CheckoutJourneyTest`; web `checkout.test.ts` | behaviour: state, total equals quote, one order per key, one shipment | **payment settles only through the stub confirm**; e2e stops at `shipped`; delivered only via the stub auto-deliver in an integration test (COM-F-1, COM-F-2) |
| **J1 Full purchase, card** | `TestC3BothLaunchMethodsCompleteCheckout` (`paymentmethod_c3_integration_test.go:144`) | checkout completes for `card` | no payment for card at any layer beyond checkout; the PSP is method-agnostic, so low risk |
| **J2 Paid at the provider, app closed before return** | payments `TestReconcileAppliesACapturedPaymentWhoseWebhookWasLost` (`reconcile_n2_integration_test.go:348`); the webhook path needs no client call (`moneytruth_c3 :282`); Android `CheckoutProcessDeathTest`, `OrderDetailPayNowTest` ("a closed sheet polls the server"); web `paymentPoll.test.ts` | behaviour per side | no test joins provider capture → payments → event → commerce order `confirmed` while the client is gone (COM-F-1) |
| **J3 Payment fails** | commerce `payment_failed_intent_integration_test.go:44-129`; payments `failedretry_d1_integration_test.go:60-190` (failure is set only by the reconciler window; a capture after a failed attempt succeeds once); `TestC1RetryAfterPaymentFailureReReservesAndPays`; Android `PaymentCoordinatorTest` (polling stops at failed) | behaviour | none significant |
| **J4a Webhook twice** | `PAY/http/handler_test.go:393 TestWebhook_DuplicateIsNoop`; `PAY/store/postgres/proof_integration_test.go:71` (C4, same event id ×10); commerce `capture_verdict_test.go:33-70`, `TestSecondCaptureOnPartiallyRefundedOrder_SamePaymentIsANoOp` | behaviour | capture replayed under a **new** event id is not tested at payments (only refunds are, `proof_b :309`) (COM-F-11) |
| **J4b Webhook late** | `TestProofC7_LateCaptureAfterExpiryRefundsRatherThanFulfils`; `TestOwed_PaymentsLateCaptureParkedThenRefunded`; `TestLateCaptureRepeat_RefundedOrderStaysRefunded` | behaviour (order stays expired, one refund command, stock not consumed) | — |
| **J4c Webhook out of order** | `TestPaymentFailed_AfterTheCaptureChangesNothing` (`:100`); `TestPaymentFailed_TheEarlierAttemptsFailureNeverFailsTheRetry` (`:129`); `TestPaymentStatusTransitions` | behaviour | refund event before capture is not tested (UNVERIFIED that it is reachable) |
| **J5 Two buyers, last unit, same moment** | `TestProofC1_FiftyConcurrentCheckoutsAgainstOneUnit` + `TestProofC1_NegativeControl`; `TestCouponLastUseRace`; `TestProofC9_CartMutationRacingCheckout` | behaviour on real Postgres with an executed negative control | none at unit scale; nothing at load scale (COM-F-9) |
| **J6 Change the price in the request** | N6 proofs; `TestC3JourneyRefusesATotalTheServerNeverStated`; `TestC3PriceChangeAfterQuoteCommitsNothing`; e2e `order.TotalMinor == q.TotalMinor`; e2e "customer confirms for one paisa → 400"; Android "checkout submits exactly the server's quoted total" | behaviour | no test sends forged money fields (`total_minor`, `unit_price_minor`, `discount`) in cart or checkout bodies (COM-F-10) |
| **J7 Cancel before shipping** | `TestCancelPaidOrder_RefundsTheTotalAndRestocks`; `TestSellerFulfilment_CancelFromPackedReleasesStockAndOwesARefund`; `TestCouponRelease_OnCancelBeforePaymentOnly`; contract `order_cancel_post_204` | behaviour up to the `order_refund_commands` row | the commerce refund worker that delivers the command to payments is untested; nothing follows through to `refunded` (COM-F-5) |
| **J7 Cancel after shipping** | `TestSellerFulfilment_CancelFromShippedIsRefused`; `TestDB_SellerCancelsFromPackedAndNotFromShipped`; `TestCustomerCanCancelMatchesTheCancellationMatrix`; contract `order_cancel_post_409_not_permitted` | behaviour (refused) | no post-delivery refund path exists (returns fenced, no admin route; map 15 q5); nothing to test |
| **J8 Partial and full refund** | payments `refundworker_rw_*`, `proof_b` B3, `TestInitiateRefund_AmountExceedsIntent`, `TestOfferRefund_ProviderOverRefundIsRefused`; commerce `TestRefundSettlement_PartialThenRemainder`, `TestCancelPartiallyRefundedOrder_*`; `tax TestProperty_RefundNeverExceedsAndAlwaysReconciles` | behaviour, each side alone, by injecting events | no cross-service refund test; the only product trigger for a partial refund is a provider-side refund (COM-F-5) |
| **J9 Seller A asks for seller B's order by id** | `TestSellerFulfilment_RoutesAreOwnerChecked` (pack, cancel, ship, history → 403 for another seller, a non-seller and the buyer); `TestTheOwningSellerMayBookAShipmentAndAStrangerMayNot`; `TestAnotherSellerCannotRepriceYourProduct`; `TestAdjustingAnotherSellersStockIsForbiddenNotAnInternalError`; `TestMarkDeliveredRefusesAnotherSellersShipment` | behaviour (status + no state change) | `GET /seller/orders/:id` detail by another seller is tested only through the `sellerLines` helper (`service/sellermoney_test.go:54`); `GET/POST /orders/:id/invoice` and `GET /orders/:id/shipments` by another seller are untested (COM-F-3) |
| **J10 Buyer asks for another buyer's invoice by id** | none | — | **no test** (COM-F-3). Neighbours: `GET /orders/:id` → 404 (`ownorder :269`), `/payment` → 403 (contract), cancel → 404 (contract) |
| **Onboarding and admin review** | e2e `FullJourney` (incomplete application 409, draft shop and never-submitted listing refused, CSRF, no-role admin 403, consumer token 401, `/internal/` 404); `adminreview_*`; `submitgate_*`; admin-service `handler_commerce_kyc_*` | behaviour | the e2e admin refusals see only the status (log: "admin-service relayed the status without commerce's body") (COM-F-13) |

## 3. Specific checks: would a test fail if the rule broke?

| Rule | Test that would fail, and layer | Verdict |
|---|---|---|
| Client cannot influence the total | N6 proofs + negative control (store, integration); `TestC3JourneyRefusesATotalTheServerNeverStated` (HTTP, integration); `TestC3QuotedTotalIsAcceptedByCheckoutExactly`; e2e total equals quote; Android `CheckoutJourneyTest` | **yes** for `expected_total_minor`; **no** for extra money fields in a body (COM-F-10) |
| Paid only from a signed webhook, amount and currency compared | payments `moneytruth_c3 :282-346` (real HMAC, real adapter), `proof_b` B2 + negative control, `openorder_a1`, `reconcile_n2`; commerce C6, `shared/paymentevents/check_test.go:10-86`; e2e: refused confirm for 1 paisa and for another intent; webhook 503 on stub | **yes, per service.** No test crosses payments → Kafka → commerce with a signed body (COM-F-1). **No e2e is driven by a real signed webhook body.** |
| Blank currency or nil payer on the commerce event | `check_test.go:62-73` pins `AllowUnstated` accepting them | pinned as accepted (map q9); a test exists either way |
| Replay rejected | same event id: payments C4, `TestWebhook_DuplicateIsNoop`; commerce inbox; refund under a new event id: `proof_b :309`, `owed_refund_replay_test.go:63` | **yes** for same id and for refunds; **partial** for a capture replayed under a fresh id (COM-F-11) |
| No oversell under concurrency | `TestProofC1_*` with negative control (integration-all and integration-commerce) | **yes** |
| Abandoned reservations released | C7 asserts `reserved=0` after the sweep (`proof_integration_test.go:821-822`); `TestReleasingACartHoldActuallyReleasesIt`; `TestExpireStaleOrders_DefersAFailingOrderAndProgresses`; `TestCouponRelease_OnExpiry` | **yes** (sweep called directly; the 1-minute ticker loop is not tested) |
| Illegal order transitions rejected | DB trigger: `TestProofIllegalOrderTransitionRejected` (**gated 998 only; skipped in integration-all, last run 31 Oct**); Go paths: `order_fulfilment_db_test.go`, `TestDB_MarkOrderPaid_Guards`, `TestSellerFulfilment_*FromShipped/Cancelled*`, `TestPaymentStatusTransitions` | **partial** (COM-F-4, COM-F-15) |
| Every transition authorised | seller routes by owner (`RoutesAreOwnerChecked`), cross-user cancel (`proof :1004`), admin review by state (`adminreview_*`) | **partial**: no test enumerates every `UPDATE orders SET status` writer (COM-F-15) |
| Seller and buyer isolation on every object-id route | orders get, payment, cancel, seller pack/cancel/ship/history, shipment POST, variants, stock, products, coupons PATCH, try-on, reviews seller-response | **no** for invoice GET/POST, shipments GET by a stranger, `/orders/:id/items`, seller order detail over HTTP (COM-F-3) |
| Refund cannot exceed captured or repeat | `TestInitiateRefund_AmountExceedsIntent` (unit), `TestOfferRefund_ProviderOverRefundIsRefused`, C8, B3, `TestOwedCaptureNewRefundPastTheOwedAmountIsStillRefused`, tax property tests, `TestCancelPartiallyRefundedOrder_RefundsTheRemainder…` | **yes** |
| Coupon reuse and stacking | `TestCouponPerBuyerLimit`, `TestCouponLastUseRace`, `TestCouponRelease_RetryRefusedWhenTheLastUseWasTaken`, `TestC3QuoteTakenWithACouponIsRefusedWithout`, `TestPlatformCoupon_RefusedWhileOff_…`, `TestPaymentSucceeded_RecordsTheBankOffer_AmountRuleUnchanged`; one `coupon_code` per checkout by shape | **yes** |
| Payout detail changes audited | none. Payout account tests cover sealing only (`kycpii_test.go:116-152`, `kycpii_integration_test.go:58-96`); no audit row exists to assert | **no** (COM-F-6) |
| GST split | `gst_test.go` golden + property; `TestProofGST_StoredComponentsSumToCharged`; `TestCouponSellerPercentage_QuoteEqualsCheckout_GSTOnDiscountedLines` | **yes** |
| Invoice fields | `invoice_money_test.go` (every line and total is stored paise; refuses money that does not reconcile), `invoice_issue_integration_test.go` (CGST/SGST/IGST, HSN) | **partial**: seller GSTIN, place of supply, invoice-number sequence and buyer details on the goods invoice are not asserted; `shared/invoice` Rule 46 tests cover the service document only (COM-F-8) |
| Shipping webhook authenticated | none. `ShiprocketCourier.VerifyWebhook` (`C/courier/shiprocket.go:64-89`) has no test; the HTTP test doubles return nil (`checkout_journey_integration_test.go:67`, `contracts_integration_test.go:245`); no test hits `/shipments/webhooks/:courier` or `/courier-callback` | **no** (COM-F-2) |
| Edge never routes `/v1/payments` | e2e `commerce_prepaid_test.go:125-137` (five calls, all 404) | **yes** |

**Mutation coverage.** The harness rule is a recorded mutation per new guard (`docs/harness/AGENT-RULES.md:58-72`, `scripts/harness/mutate.sh`). For commerce and payments the records are counts in commit messages, not per-guard lists (VERIFIED-RUN, `git log --grep mutation`):
- `cd49241d4` (8 Nov) 12/13 killed; `230a9beba` (8 Nov) 7/9 money mutations; `00685ad32` (8 Nov) 24/27, "one, an owed-back intent sharing a provider payment id, has no test yet"; `a5662facf` (8 Nov, Android commerce) 42/43; `52c5641d0` (25 Oct, Android) 46/48; `c3259f808` (1 Oct) 71 guard mutations; `d107aa1f3` (1 Oct) 21; `f458bcd11` (coupons, last-use race); `13f600f5c` (bank offers, every clause); payments `c8b39ea27`, `66d5fbeb9`, `698c0f30d`, `0745c36ba`, `5972669e3`, `93e7ad449` (3-5 each, 14 Sep).
- Executed negative controls committed as tests: `TestProofC1_NegativeControl` (asserted by `integration-commerce.yml:837-851`), `TestProofNegativeControl_FenceRemovedAllowsTheWrite`, `TestProofNegativeControl_N6_OptionalTotalSkipsTheComparison`, `TestNegativeControl_WithoutTheFenceTheLegacyRouteIsReachable`, `TestNegativeControl_FallThroughMakesBadTaxDataZeroRated`, `TestSellerCrossingNegativeControl_…`, `TestNegativeControl_PlaceholderPricesEverythingAtZero`; payments `TestProofNegativeControl_B2_CommitBeforeCompareIsUnreachable`, `…_N4_…`, `TestNegativeControl_OldRefundNormalizerLosesTheOrderID`.
- No recorded mutation was found for: the courier webhook verifier, the invoice and shipment read guard (`requireOrderRole`), the commerce refund worker, or payout-account changes.

## 4. CI state (VERIFIED-RUN)

| Workflow | Runs these tests | Last run on `codex/module-01-02-launch-safety` | Last run on `main` |
|---|---|---|---|
| Integration (all Go services) `integration-all.yml` | commerce on `commerce_it_test` + `commerce_delivery_it_test`, payments on `payments_it_test`, admin, wallet; **skips** the four gated-998 tests (`integration-services.yaml:162`) | 44093026646 @ `87a34d23d`, **success**: commerce `PASS integration (42s)`, payments `PASS integration (8s)`; 44111295029 @ `be57a46bf` queued | none listed |
| Integration (Commerce P0) `integration-commerce.yml` | money-type gate, unit, **commerce-proofs on gated 998** (illegal transition, fences, DB-level method check, C1 negative-control assertion, named-PASS checks `:822-835`), migration-safety | **41162319116 @ `545baf5f0`, 31 Oct, success.** Trigger is `pull_request` on commerce/payments paths or `push` to `main` only (`:17-30`). **19 commerce/payments commits since** (`git log 545baf5f0..HEAD`) | none listed |
| E2E `e2e.yml` | domain `commerce`: the three `TestE2E_Commerce*` | 44093031010 @ `87a34d23d`: commerce **success**, run **failure** (video tier only); 44088840973, 44083423118 also failed; 44111297089 @ `be57a46bf` queued | 26 Aug @ `adad00644`, failure |
| Go CI `go-ci.yml` | every module, unit | 44093026684 @ `87a34d23d` success; 44111294720 @ `be57a46bf` in progress | 26 Aug, failure |
| Android `android-native.yml` | the five commerce modules in "Unit tests" | 44093026827 @ `87a34d23d`: **Unit tests success** (log shows `:core:commerce`, `:core:payments`, `:feature:commerce`, `:feature:seller` `testDebugUnitTest`), **Lint failure**; 44110288912 @ `1ff95208d` in progress | 26 Aug, failure |
| web-ci (pb-echo) | all web tests | 44078366557 @ `1b596b4`, success (5830 pass, 100 skip) | — |

- **Skip visibility:** `go.sh` runs integration tests without `-v`; the CI job prints only stage lines (`PASS integration commerce-service (42s)`). Which commerce and payments integration tests ran or skipped in integration-all cannot be shown. All DSN variables the tests read (`COMMERCE_TEST_DSN` ×9, `COMMERCE_DELIVERY_TEST_DSN`, `PAYMENTS_TEST_DSN` ×3, `PAYMENTS_TEST_POSTGRES_DSN`) are set by the manifest, so silent skips by a missing variable are not expected (VERIFIED-READ). Only integration-commerce asserts named `--- PASS` lines, and it last ran on 31 Oct.

## 5. Findings

### COM-F-1
- **ID:** COM-F-1
- **Title:** No test at any layer drives a real signed Razorpay webhook through payments-service, the outbox and Kafka to a commerce order becoming paid; the e2e settles only through the stub confirm.
- **Severity:** P1
- **Category:** tests
- **Evidence level:** VERIFIED-RUN (e2e log) + VERIFIED-READ
- **Evidence:** `commerce_journey_test.go:208-216` and `commerce_prepaid_test.go:104-106` pay through `/payment/confirm` with `gateway=stub`; `commerce_prepaid_test.go:139-165` asserts the webhook answers 503 on a stub stack, and its header says "The signed provider webhook → outbox → consumer path … is not exercised here" (`:34-36`). Payments signs a real body but stops at `ApplyWebhook` (`moneytruth_c3_integration_test.go:236-276`). Commerce injects `PaymentSucceeded` structs directly into the store (e.g. `cancel_refund_integration_test.go:29`). The seam is covered only by decode unit tests (`shared/paymentevents/events_test.go:70`).
- **How to reproduce:** `grep -n "Razorpay-Signature" Architecture/tools/integration/*.go` returns nothing; `gh run view 44093031010 --job 134072872120 --log | grep commerce_prepaid_test.go:1[4-6]`.
- **Impact:** The deployed paid path (map 10, steps 4-6) is never run end to end. A change to the outbox event shape, the partition key, the `application_id` stamp, the commerce consumer group or the `reference_type` filter (`C/consumers/payments_p0.go:351`) can stop every real order from being confirmed while CI stays green. J2 (app closed after paying) depends on exactly this path.
- **Fix (test to add):** E2E commerce domain variant with payments-service in `razorpay` mode pointed at an `httptest` fake of the Razorpay API (orders create, payment fetch) and a test `RAZORPAY_WEBHOOK_SECRET`. `TestE2E_Commerce_SignedWebhookPays`: checkout, open intent, build `payment.captured` for the intent's provider order id and amount, sign with HMAC-SHA256, POST to `:8102/v1/payments/webhook`; poll `/orders/:id/payment/status` until `paid` with no client confirm. Then: the same body with a new `X-Razorpay-Event-Id` (one `commerce.order.paid`, order unchanged); a body for 1 paisa less (order stays `payment_pending`, owed capture recorded); a bad signature (401).
- **Acceptance:** Green in CI; red if the commerce payments consumer is not started, or if the amount comparison is removed from `VerifyProviderMoney`.
- **Effort:** M

### COM-F-2
- **ID:** COM-F-2
- **Title:** The courier webhook is unauthenticated as far as tests know: `ShiprocketCourier.VerifyWebhook`, both webhook routes and the stub-in-prod case have no test.
- **Severity:** P1
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/courier/shiprocket.go:64-89` (token or HMAC, reject when neither is set). Courier tests are `shiprocket_etd_test.go` and `shiprocket_rate_test.go` only. Every HTTP test double returns nil (`C/http/checkout_journey_integration_test.go:67`, `contracts_integration_test.go:245`). `git grep "shipments/webhooks\|courier-callback" -- '*_test.go'` finds nothing. The stub verifier accepts anything (`courier.go:161`), prod reads `COURIER_PROVIDER` from a secret, and nothing refuses the stub in a managed ENV (map 7, q12).
- **How to reproduce:** the grep above; `ls Architecture/services/commerce-service/internal/courier`.
- **Impact:** Courier updates move orders to `shipped`, `out_for_delivery` and `delivered`, and `delivered` unlocks reviews (`TestC1StubCourierDeliversAndReviewsBecomePossible`). A regression that accepts an unsigned callback (for example the empty-secret check inverted, or the case-folding of headers dropped) would let anyone mark orders delivered with CI green.
- **Fix (test to add):** `shiprocket_webhook_test.go`: no secrets → error; token only → right token in `X-Api-Key`, `Token` or `X-Token` passes, wrong or missing fails; HMAC only → right hex signature passes, wrong body fails; header names in mixed case. Route test: `POST /v1/commerce/shipments/webhooks/shiprocket` with a bad token → 401 and no `shipment_events` row; the courier name not matching the configured one → refused. Boot test: `COURIER_PROVIDER=stub` with `ENV=prod` refuses to start (once that guard exists).
- **Acceptance:** Each test fails when its branch of `VerifyWebhook` is removed (record the mutations).
- **Effort:** S

### COM-F-3
- **ID:** COM-F-3
- **Title:** Object-id isolation is untested on the invoice routes, shipment reads, order items and seller order detail.
- **Severity:** P1
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ
- **Evidence:** `GetInvoice` and `IssueInvoice` depend on `requireOrderRole` (`C/http/shipments.go:183-215`; the comment at `:199-200` says GET "was previously unauthenticated"). The only invoice route test is the buyer's own 200 (`contracts_fixtures_integration_test.go:675-676`). `git grep "/invoice" -- '*_test.go'` finds no stranger, other-buyer or other-seller case. Shipment POST by a stranger or another seller is tested (`ownorder_integration_test.go:167-190`), but GET `/shipments` only by the owner and buyer (`:191-210`). `GET /orders/:id/items` (unscoped read then Go check, map 2.1) has no test. `GET /seller/orders/:id` by another seller is covered only by the `sellerLines` helper (`service/sellermoney_test.go:54-61`).
- **How to reproduce:** `git grep -n "/invoice\|/items\"" -- 'Architecture/services/commerce-service/*_test.go'`.
- **Impact:** The invoice carries the buyer's name and delivery address and the seller's GSTIN, and comes with a 7-day presigned URL. Dropping the `requireOrderRole` call (as was once the case) or loosening `OrderActor` would leak them to any signed-in user who has an order id, and no test would fail. Track B's question 10 (unscoped reads with Go checks) has no regression net on these routes.
- **Fix (test to add):** One table-driven HTTP integration test over every `/orders/:id/*` and `/seller/orders/:id*` route with five callers: owner buyer, owning seller, another buyer, another seller, no seller row. Assert the status (404 for strangers on buyer routes, matching `TestOrderDetailDoesNotConfirmSomeoneElsesOrderExists`), that no `download_url` or address appears in the body, and that no invoice row is created by a refused POST.
- **Acceptance:** Fails when `requireOrderRole` is removed from `GetInvoice`, or when the seller filter in `S/orders_read.go:295-297` is removed.
- **Effort:** S

### COM-F-4
- **ID:** COM-F-4
- **Title:** The commerce P0 gate (`integration-commerce.yml`) has not run for 17 days and 19 commerce/payments commits; the gated-998 proofs run nowhere else.
- **Severity:** P1
- **Category:** tests / CI
- **Evidence level:** VERIFIED-RUN
- **Evidence:** Last run 41162319116 @ `545baf5f0` on 31 Oct (success). Triggers: `workflow_dispatch`, `pull_request` on commerce/payments paths, `push` to `main` (`integration-commerce.yml:17-30`); this branch is pushed without a PR. `git log --oneline 545baf5f0..HEAD -- Architecture/services/commerce-service Architecture/services/payments-service` lists 19 commits, including `2f5679693` (mismatched payments refunded), `00685ad32`, `2166d3141` and `0a57631fd` (refund and duplicate-capture logic). integration-all skips `TestProofFencedSurfacesRefuseWrites`, `TestProofNegativeControl_FenceRemovedAllowsTheWrite`, `TestProofIllegalOrderTransitionRejected` and `TestC3DatabaseRefusesAnUnsupportedMethodDirectly` (`scripts/ci/integration-services.yaml:162`). Only this workflow asserts named `--- PASS` lines and that the C1 negative control reproduced the oversell (`:822-851`).
- **How to reproduce:** `gh run list --workflow integration-commerce.yml --limit 5`.
- **Impact:** The DB-level state-machine proof, the fence triggers and the money-type gate could be broken today without anyone seeing it. Since integration-all prints no test names, whether C1 itself ran there is also unverifiable.
- **Fix (test to add):** Add `push` on `codex/**` and `feat/**` with the same paths filter (as `integration-all.yml:21-26` does), or fold the commerce-proofs job into integration-all as a gated-998 extra database. Make `go.sh --db` print `-v` names or a per-test summary for commerce and payments.
- **Acceptance:** A run exists for the current HEAD; a commit that breaks `TestProofIllegalOrderTransitionRejected` turns the branch red.
- **Effort:** XS

### COM-F-5
- **ID:** COM-F-5
- **Title:** The refund hop from commerce to payments is untested: the commerce refund worker has no test, and no test follows a cancel or partial refund through to `refunded`.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** `C/service/checkout_p0.go:633-689` (`RunRefundWorker`, `deliverRefund`: missing intent parked as terminal, malformed id terminal, `payments.ErrRefused` terminal, otherwise retry; the same idempotency key on every attempt). `git grep "deliverRefund\|ClaimDueRefundCommands\|MarkRefundSubmitted" -- '*_test.go'` returns nothing. Cancel tests stop at the `order_refund_commands` row (`cancel_refund_integration_test.go:63-122`). Settlement tests inject refund events (`refund_settlement_integration_test.go:17`). The payments refund worker is tested on its own (`refundworker_rw_integration_test.go:542-637`).
- **How to reproduce:** the grep above.
- **Impact:** Cancellation is the only buyer refund path at launch (map 10). A regression in the terminal-vs-retry mapping, in the key reuse (double refund at the PSP, or a retry that never settles), or a nil payments client in prod would leave buyers unrefunded with every test green.
- **Fix (test to add):** Service unit test with a fake store and a fake payments client: (a) success → submitted once; (b) 5xx → failed, retryable, and the next tick sends the same idempotency key; (c) `ErrRefused` → terminal; (d) no intent → terminal. Then in the COM-F-1 e2e: cancel a paid order and poll until `payment_status=refunded` with exactly one provider refund call recorded by the fake Razorpay.
- **Acceptance:** Fails when the idempotency key is regenerated per attempt, or when `ErrRefused` is treated as retryable.
- **Effort:** S (unit), M (e2e)

### COM-F-6
- **ID:** COM-F-6
- **Title:** Changes to a seller's payout bank account are neither audited nor tested.
- **Severity:** P2
- **Category:** tests / security
- **Evidence level:** VERIFIED-READ
- **Evidence:** Payout-account tests cover sealing and readiness only: `service/kycpii_test.go:116-152`, `store/postgres/kycpii_integration_test.go:58-96`, `http/onboarding_contract_integration_test.go:140 TestSavingAPayoutAccountWorks`. No test file that mentions payout also asserts an audit row for a seller-side change (the files that mention both are admin-token, admin-audit and delivery tests). `PUT /onboarding/step/payout` is reachable after approval (map 2.1).
- **How to reproduce:** `git grep -il payout -- '*_test.go' | xargs grep -il audit`.
- **Impact:** Account takeover followed by a payout-account swap is the classic marketplace fraud. Payouts are not executed yet (map q3), so the exposure starts when they are, but there is no trail and no test to keep one.
- **Fix (test to add):** After adding an append-only audit row (actor, old and new last4, hash, time) and a post-approval re-verification state: an integration test that changes the account of an approved seller and asserts one audit row, no plaintext number in it, and the seller back in a review state for payouts.
- **Acceptance:** Fails when the audit write is removed.
- **Effort:** S (test) after the product decision

### COM-F-7
- **ID:** COM-F-7
- **Title:** The web's golden-fixture contract tests skip in CI and have already drifted from the backend.
- **Severity:** P2
- **Category:** tests / contract
- **Evidence level:** VERIFIED-RUN
- **Evidence:** Local `bun test src/features/shop src/app/shop`: 2 failures, `admin/coupon_patch_200.json` (`expires_at`) and `orders/order_shipments_get_200` (missing `seller_managed`). The backend path is hard-coded to `C:/workspace/modernsmapp/...` (`src/features/shop/__tests__/contractFixtures.ts:15`, `couponsOffers.contract.test.ts:26`), and the tests skip when it is absent (`orders.contract.test.ts:107-108`). `sell.contract.test.ts:31` and `storefront.contract.test.ts:25` resolve `../../../../../modernsmapp`, which from this worktree is `C:\workspace\wt\modernsmapp` and does not exist, so `sell.contract` returns early (`:60`). web-ci runs on ubuntu without the backend checkout, so all of these skip there (run 44078366557 is green with 100 skips). Android's equivalent check runs and passes (71 of 71 identical).
- **How to reproduce:** the `bun test` command above in pb-echo.
- **Impact:** A backend field rename or removal in an order, shipment or coupon response reaches the web with CI green. `seller_managed` drives whether the seller may mark delivered, so the web cannot show that state from its fixture-based tests.
- **Fix (test to add):** Check out commerce-service's `testdata/contracts` in web-ci (sparse checkout of the backend repo at a pinned ref, or publish the fixtures as an artifact) and make the comparison fail, not skip, when the directory is missing. Use one path helper for every contract test. Refresh the two drifted fixtures.
- **Acceptance:** web-ci fails when a backend fixture changes and the web copy does not.
- **Effort:** S

### COM-F-8
- **ID:** COM-F-8
- **Title:** The goods invoice's statutory fields (seller GSTIN, place of supply, invoice number and sequence, buyer details) are not asserted.
- **Severity:** P2
- **Category:** tests / compliance
- **Evidence level:** VERIFIED-READ
- **Evidence:** `invoice_money_test.go` and `invoice_issue_integration_test.go` assert paise, CGST/SGST/IGST and HSN only (grep finds no `GSTIN` there). `shared/invoice/generator_test.go` (3 tests, the generator commerce uses, GSTIN at `generator.go:231`) asserts no GSTIN or place of supply. The Rule 46 tests (`service_test.go:21`, `service_pdf_test.go:126`) cover the service document, not the goods invoice. The sequence is read in a test (`invoice_issue_integration_test.go:269`) but its uniqueness under concurrency is not asserted.
- **How to reproduce:** `grep -n -i gstin Architecture/shared/invoice/generator_test.go Architecture/services/commerce-service/internal/service/invoice_*test.go`.
- **Impact:** An invoice missing the supplier GSTIN or place of supply, or two invoices sharing a number, is a GST compliance failure that no test would catch.
- **Fix (test to add):** `TestGoodsInvoiceCarriesRule46Fields` (supplier name, address, GSTIN; invoice number format `…/FY/nnnnnn`; date; buyer name and address; place of supply with state code; HSN; taxable value; rate and amount per component; total in words). Integration: issue invoices for 20 orders concurrently and assert 20 distinct consecutive numbers per financial year.
- **Acceptance:** Fails when GSTIN is dropped from the template or the sequence is read outside the transaction.
- **Effort:** S

### COM-F-9
- **ID:** COM-F-9
- **Title:** No load test exists for checkout, the payment webhook or the order reads, and the dev smoke stops at the quote.
- **Severity:** P2
- **Category:** tests / performance
- **Evidence level:** VERIFIED-RUN
- **Evidence:** `ls Architecture/tools/loadtest` → `README.md lib long-video reels`; no commerce or checkout file; no `func Benchmark` in commerce or payments. `scripts/smoke/dev-smoke.mjs:182` ends at `shop.quote`.
- **How to reproduce:** as above.
- **Impact:** Checkout takes row locks on the cart, variant and product (`P/checkout.go:293,863`); hot-product contention, the 500 ms outbox poll and the 20 s refund worker under a sale-day burst have never been measured. C1 proves correctness at 50 goroutines, not throughput or latency.
- **Fix (test to add):** k6 script under `tools/loadtest/commerce`: N buyers checking out against one hot variant and against many variants, with a stub-settled payment; record p95 for quote, checkout and intent, lock waits, and reservation-to-confirmed time. Extend the dev smoke to checkout and intent (stopping before payment).
- **Acceptance:** A baseline report checked in, with thresholds that fail the run.
- **Effort:** M

### COM-F-10
- **ID:** COM-F-10
- **Title:** No test sends forged money fields in cart, quote or checkout bodies.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** The money guards are tested only through `expected_total_minor` (section 3). No test posts `total_minor`, `unit_price_minor`, `discount_minor` or `shipping_minor` to `/cart/items`, `/checkout/quote` or `/v2/orders/checkout` and asserts they are ignored. The handlers bind only ids today (`H/handler_p0.go:373-378`), so the risk is a future regression, not a current hole.
- **How to reproduce:** `grep -rn "unit_price_minor\|total_minor\":" --include=*_test.go Architecture/services/commerce-service/internal/http` shows only response decoding.
- **Impact:** A later DTO change that binds a client price would not be caught.
- **Fix (test to add):** Extend `TestC3JourneyQuoteThenCheckoutOverHTTP` with a body that also carries forged money fields at one paisa; assert the order total equals the quote and the stored line prices equal the variant's.
- **Acceptance:** Fails if any of those fields is bound.
- **Effort:** XS

### COM-F-11
- **ID:** COM-F-11
- **Title:** A capture webhook replayed under a fresh event id is not tested at payments-service; only refunds are.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** Capture dedupe tests reuse one event id (`PAY/store/postgres/proof_integration_test.go:71-110`, `PAY/http/handler_test.go:393-410`). The new-id case is tested for refunds (`proof_b_integration_test.go:309-325`, `owed_refund_replay_test.go:63`). Commerce absorbs a redelivered capture (`capture_verdict_test.go:33-70`, `duplicate_capture_integration_test.go:63`). The HMAC covers the body only and the event id is an unsigned header with no time window (map q7).
- **How to reproduce:** `git grep -n "EventID" -- 'Architecture/services/payments-service/internal/store/postgres/proof_integration_test.go'`.
- **Impact:** Whether payments publishes a second `payment.succeeded` (or records an owed capture) for a replayed body is unpinned; today commerce's verdict table is the only backstop.
- **Fix (test to add):** In `moneytruth_c3_integration_test.go`, apply the same signed capture body twice with different `X-Razorpay-Event-Id` values. Assert one `succeeded` transition, at most one outbox `payment.succeeded`, and no owed-capture row.
- **Acceptance:** Fails if payments' dedupe relies on the event id alone.
- **Effort:** XS

### COM-F-12
- **ID:** COM-F-12
- **Title:** CI cannot show which commerce and payments integration tests ran; locally 13 delivery DB tests and one payments DB test skip.
- **Severity:** P3
- **Category:** tests / CI
- **Evidence level:** VERIFIED-RUN
- **Evidence:** integration-all job 134073094517 prints `PASS integration commerce-service (42s)`, payments `(8s)` and nothing per test. Local skips in section 1.1. The untagged payments `store_db_test.go` has no `_test` database guard (map 12).
- **How to reproduce:** `gh run view 44093026646 --job 134073094517 --log | grep -E "^(PASS|FAIL)"`.
- **Impact:** A fixture or env change that makes a proof `t.Skip` would go unnoticed.
- **Fix (test to add):** Have `go.sh` write `go test -json` and fail on any skip in a list of must-run tests (C1, C2, C6, C7, N6, B2, B3, C4, C8), as integration-commerce already does by name.
- **Acceptance:** Renaming or skipping one of those tests turns integration-all red.
- **Effort:** S

### COM-F-13
- **ID:** COM-F-13
- **Title:** The e2e's admin refusals check only the status, because admin-service relays commerce's 409 without its body.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-RUN
- **Evidence:** Log lines for `commerce_journey_test.go:123,151,160,171`: "409 (admin-service relayed the status without commerce's body; SELLER_TRANSITION_NOT_ALLOWED not visible here)".
- **How to reproduce:** `gh run view 44093031010 --job 134072872120 --log | grep relayed`.
- **Impact:** Any 409 (for example a lock timeout) passes as the intended state refusal. The admin console also cannot tell the operator why an approval failed.
- **Fix (test to add):** Once admin-service relays the error code, change `commerceRefusedByConsole` to require it.
- **Acceptance:** The e2e fails on a 409 with another code.
- **Effort:** XS

### COM-F-14
- **ID:** COM-F-14
- **Title:** Android has no instrumented commerce tests; the Razorpay launcher and process death are proven only with fakes.
- **Severity:** P3
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** No `src/androidTest` in the five modules (map 11.1). `PaymentLauncherOneFlightTest` and `CheckoutProcessDeathTest` run on the JVM with fakes.
- **How to reproduce:** `git ls-files mobile/android | grep -c "commerce.*/src/androidTest"` = 0.
- **Impact:** Razorpay SDK result delivery after a real activity recreation is unproven on a device.
- **Fix (test to add):** One instrumented test with Razorpay's test mode and `ActivityScenario.recreate()` during the sheet; assert one order and a polled `paid`.
- **Acceptance:** Runs in a device job.
- **Effort:** M

### COM-F-15
- **ID:** COM-F-15
- **Title:** No test proves that every writer of `orders.status` goes through the transition matrix on a database without gated 998.
- **Severity:** P2
- **Category:** tests
- **Evidence level:** VERIFIED-READ
- **Evidence:** The code itself says nothing guards `UPDATE orders SET status` on a boot-migrated database (`C/store/postgres/order_fulfilment.go:3-7`). Go-level matrix tests cover seller pack, ship and cancel (`order_fulfilment_db_test.go`), payment (`TestDB_MarkOrderPaid_Guards`) and the customer cancel matrix (`wirecontract_test.go:147`). The DB trigger test needs gated 998 (COM-F-4). No test lists every `UPDATE orders SET status` site (courier updates, expiry, refund settlement, admin, delivery) and checks it against `order_status_transitions`.
- **How to reproduce:** `git grep -n "UPDATE orders SET status" -- Architecture/services/commerce-service/internal/store` and compare with the tests above.
- **Impact:** A new writer, or a courier update that jumps `payment_pending → delivered`, would not be refused on a database where 998 was never applied (probably all deployed ones, map q1).
- **Fix (test to add):** A source-level guard test that fails when an `UPDATE orders SET status` appears outside an allowlisted helper that checks the matrix, plus a behaviour test per writer that tries one illegal source state.
- **Acceptance:** Adding a raw status update anywhere else fails the build.
- **Effort:** S

## 6. Already well tested

- **Oversell:** 50 concurrent checkouts on one unit, with an executed negative control asserted by name in CI (`proof_integration_test.go:262,407`; `integration-commerce.yml:837-851`).
- **Idempotent checkout:** same key ×20 concurrently, same key with a different body refused (C2), the e2e replay, Android process death resubmitting one key (`CheckoutProcessDeathTest`).
- **Server-side total:** N6 with negative control, C3 quote-equals-checkout across tax rates and coupons, price change commits nothing.
- **Payment truth at payments-service:** real HMAC-signed bodies through the real adapter; missing, wrong or other-currency amounts never settle; reconciler recovers a lost webhook once; failed attempts do not end the payment.
- **Commerce capture handling:** amount mismatch, late capture after expiry, duplicate and owed captures, failure after capture, the redelivery verdict table.
- **Refund bounds:** cannot exceed the intent or the order; duplicate provider refunds credit once, also under a new event id; cancel refunds the remainder after a partial refund.
- **Coupons:** per-buyer limit, last-use race, release on expiry, cancel and failure, platform switch, GST on discounted lines.
- **GST maths:** golden and property tests that components always sum to the charge and refunds always reconcile.
- **Seller-side ownership on writes:** pack, cancel, ship, history, reprice, stock, product patch, gallery, try-on, coupon patch, mark-delivered.
- **Edge:** `/v1/payments/*` never routed; `/internal/` refused; admin approval needs an admin console session with CSRF and a role.
- **Clients:** Android 513 unit tests (never "paid" from the SDK callback, one payment sheet in flight, the quoted total submitted exactly) with 71 fixtures byte-identical to the backend; web 642 passing shop tests on pure logic (poll schedule, idempotency key, quote mapping).

## 7. Not verified

- Which commerce and payments integration tests actually ran (not skipped) in integration-all: no per-test output (COM-F-12).
- Go CI 44111294720, Integration 44111295029 and E2E 44111297089 for `be57a46bf` were queued or in progress at audit time.
- The 23 local web skips were not listed one by one (bun prints no skip names without a reporter flag).
- admin-service and payments-service results include uncommitted work; a clean-HEAD run (for example from `git archive HEAD`) was not done.
- Notification coverage was only counted: `notification-service/internal/events/commerce_consumer_test.go` (4 tests) and `service/commerce_delivery_test.go` (2); what they assert was not read.
- Review, favourites, engagement and search-document tests were not mapped to journeys.
- The 13 local-delivery DB tests were not run (feature off).
- Whether a refund webhook arriving before the capture can occur at all, and how it is handled.

## 8. Notes for other tracks

- **Track B (security):** COM-F-2 (courier webhook untested, stub accepts anything, no prod refusal) and COM-F-3 (invoice and shipment reads by id untested) are the two isolation gaps with real data behind them. COM-F-11 (capture replay under a new id) goes with map q7. COM-F-6: payout-account changes have no audit.
- **Track C (data):** COM-F-15 and COM-F-4: the order transition trigger exists only in gated 998, its test runs only in a workflow that has not run since 31 Oct, and probably no deployed database has 998 (map q1). COM-F-8: invoices are NUMERIC-only and their statutory fields are untested.
- **Track D (performance):** COM-F-9, no commerce load test at all.
- **Track E (reliability):** COM-F-5 (commerce refund worker untested), COM-F-1 (paid path never run end to end). `00685ad32` records an untested survivor: "an owed-back intent sharing a provider payment id, has no test yet". Disputes and chargebacks: nothing sets `disputed` and no test exists (map q5).
- **Track A (functional):** e2e stops at `shipped`; `delivered`, cancel, refund, invoice and reviews have no e2e. The web contract fixtures have drifted (`seller_managed` missing on the web copy of shipments, COM-F-7).
- **CI hygiene:** E2E has failed on its last three branch runs because of the video tier only; Android CI's unit tests pass and only Lint fails (44093026827, step 10); the weekly `main` Security workflow still fails.
