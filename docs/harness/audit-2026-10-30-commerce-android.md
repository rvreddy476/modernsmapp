# Audit: Android commerce, checkout and payments

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits except this file, no git writes, no Gradle builds, no device.
Android paths are relative to `mobile/android/`. Server paths are relative to the repo root.

Modules covered: `core/commerce` (Retrofit API, DTOs, `Paise`, repository, error mapping), `core/commerce-ui`, `core/payments` (Razorpay launcher, coordinator, handoff bus, in-flight store), `feature/commerce` (bag, product, browse, checkout, orders, payment opener and status source), `feature/seller` (listing, images, stock, price, documents, payout, orders, returns, earnings), plus `app/MainActivity` and the manifest where they host the payment sheet. `feature/payments` and `feature/commerce-ui` do not exist; the UI kit is `core/commerce-ui`. Server side: `Architecture/services/commerce-service/internal/http` and `internal/service/checkout_p0.go`, the `/v1/commerce` and removed `/v1/payments` entries in `Architecture/services/api-gateway/cmd/server/main.go`, and the media-service init validation that seller uploads go through.

Every Retrofit path in `core/commerce/.../network/CommerceApi.kt` (57 routes) was matched against the gin routes in commerce-service (`handler.go`, `handler_p0.go`, `handler_storefront.go`, `handler_onboarding.go`, `handler_sellscope.go`). None is retired or missing. Android calls no `/v1/payments` route; the gateway no longer proxies it. The findings below were each traced end to end. Anything I could not confirm was dropped.

---

## HIGH

### H1. "Try again" on a failed checkout does nothing, and a timed-out checkout is treated as "no order created"
- **Files:**
  - `feature/commerce/src/main/kotlin/com/us/android/feature/commerce/checkout/CheckoutScreen.kt:224-229`: `CheckoutUiState.Failed` with `retryable = true` wires "Try again" to `viewModel::placeOrder`.
  - `feature/commerce/.../checkout/CheckoutViewModel.kt:406-407`: `placeOrder()` returns at once unless the state is `Ready`. On `Failed` the button is a no-op. The retryable failures are `Network`, `TryAgain`, `Unexpected` (any 5xx or gateway 504) and `CouponUnavailable` (`:607-620`). They come from both `requote()` (`:380`) and `placeOrder()` (`:435`).
  - `CheckoutViewModel.kt:430-436`: every `placeOrder` failure sets `saved.phase = Quoted`, with the comment "No order was created". That is false for `Network` and 5xx. `core/commerce/.../repository/CommerceRepository.kt:1086-1087` maps every `Throwable`, including a read timeout after the server committed, to `CommerceError.Network`.
  - Server: `commerce-service/internal/service/checkout_p0.go:260-345`. The order commits and the cart is cleared inside the transaction. A replay under the same `Idempotency-Key` returns the stored order (`res.Reused`). So resending the same key is the only safe retry, and the client never does it.
- **Scenario:**
  - A buyer on a weak connection taps Place order. The server creates the order and holds stock, but the 30 s read timeout fires before the response arrives.
  - The screen says "Check your connection and try again". Tapping Try again does nothing.
  - The buyer backs out, which creates a new ViewModel with a new key, and opens checkout again. The re-quote now fails with `CART_EMPTY` because the first checkout cleared the cart, so the screen shows "Your bag is empty."
  - An order the buyer never saw sits in `payment_pending` holding stock until its hold lapses. If process death happens instead, `phase == Quoted` makes recovery re-quote rather than resubmit the same key (`:281`).
  - The same dead button appears when the first quote fails on a flaky network: the buyer cannot retry without leaving the screen.
- **Fix:**
  - On a `placeOrder` failure that is ambiguous (`Network`, `Unexpected` with status ≥ 500 or 0, `TryAgain`), keep `phase = Submitting`. Make "Try again" call `resubmitUnderTheSameKey()`.
  - On a quote failure, route "Try again" to `requoteAfterStaleQuote()`. Add a `retryAction` (or an origin) to `Failed` so the screen calls the right one.
  - Set `phase = Quoted` only on a definite 4xx refusal.

---

## MEDIUM

### M1. Changing payment method while "Place order" is in flight can overwrite the placed order's screen; the sheet's result is then consumed and dropped, so the buyer is never told they paid
- **Files:**
  - `CheckoutScreen.kt:456-469`: `MethodRow` has no `enabled` and stays clickable while `placing = true`. Only the Place order button is disabled (`:282-283`).
  - `CheckoutViewModel.kt:399-403`: `selectMethod` has no in-flight guard. `requote()` (`:377-393`) writes `quoteId`, `serverBreakdown` and `_state` unconditionally, including over `OpeningPayment` or `AwaitingConfirmation`.
  - `CheckoutViewModel.kt:204-215`: the handoff observer calls `handoff.consume(mine)` before checking the state. When the state is no longer `OpeningPayment` or `AwaitingConfirmation`, it returns, so the event is consumed and never acted on.
- **Scenario:**
  - The buyer taps Place order on UPI, then taps Card within about a second. The checkout succeeds first: `adoptPlacedOrder` moves to `OpeningPayment` and the sheet opens.
  - The re-quote returns afterwards. Because the cart is now empty, it returns `CART_EMPTY`, and the screen becomes "Your bag is empty" under the open sheet. If the re-quote wins the race instead, the screen becomes `Ready` with a new `quoteId` under the same key.
  - The buyer pays and the sheet closes. `SheetClosed` matches the attempt, is consumed, and is dropped because the state is `Failed` or `Ready`. No poll runs, and the buyer never sees "Order confirmed".
  - In the `Ready` case, tapping Place order sends the old key with a new quote. The server answers `IDEMPOTENCY_CONFLICT`, which leads to "Let's start again" on an order that is already paid.
  - The server stays correct throughout: the webhook still marks the order paid. The app is what misleads the buyer.
- **Fix:**
  - Ignore `selectMethod` (and disable `MethodRow` and the address "Change" pill) unless the state is `Ready` with `placing == false`.
  - In `requote()`, apply the result only if the state is still `Loading` or `Ready`.
  - In the observer, check the state before calling `consume`. If an event matches the active attempt, poll regardless of what the screen is showing.

### M2. Checkout drops the reason a payment could not be opened and always says "Order is still held, try payment again"
- **Files:** `CheckoutViewModel.kt:220-224` maps `PaymentHandoffEvent.Unavailable` to `PaymentFailed(orderId, orderNumber)` and discards `event.reason`. `CheckoutScreen.kt:185-196` renders fixed copy. The reasons that are dropped are built specifically for display:
  - `CheckoutPaymentOpener.kt:124-130`: `OUT_OF_STOCK_ON_RETRY`, "Those items have sold out…".
  - `core/payments/.../RazorpayPaymentLauncher.kt:126-136`: wrong provider build, or no session from the server.
  - `RazorpayPaymentLauncher.kt:97-101`: "A payment is already in progress".
  - An `ORDER_NOT_PAYABLE` refusal on an order the webhook already marked paid.
- **Scenario:** The buyer's first UPI attempt fails, and they tap "Try payment again" after the stock has sold out. The intent route answers 409 `OUT_OF_STOCK`. Checkout tells them the order "is still held for you", and every retry loops back to the same screen. The order detail screen does show the reason (`OrdersViewModel.kt:368-371`), so the two screens disagree.
- **Fix:** Carry `reason` on `PaymentFailed` (or add a `PaymentUnavailable(reason)` state) and render it. For an out-of-stock refusal, offer "Back to bag" rather than retry.

### M3. Stock adjustments are deltas with no idempotency, so a timed-out save applied twice doubles the change
- **Files:**
  - `core/commerce/.../network/CommerceApi.kt:309-312, 1173-1176`: `PATCH /seller/variants/:id/stock` takes `{delta, reason}`. The doc comment says "never an absolute total". No `Idempotency-Key` header is sent.
  - `feature/seller/.../seller/StockViewModel.kt:124-153`: on any failure, `amount` and `reason` are kept and `saving = false`, so the same Save is offered again.
  - Server: `commerce-service/internal/http/handler_inventory.go:37-63` applies `*body.Delta` with no dedupe.
- **Scenario:** A seller restocks +50 on a slow connection. The server applies it, and the response is lost to the read or call timeout. The screen shows "Check your connection" with "50" still filled in. The seller taps Save again, and stock becomes +100. The app then sells 50 units that do not exist, which leads to cancellations after payment. A write-down (-N) can likewise remove stock twice.
- **Fix:**
  - Client: after a `Network` or 5xx failure, re-read `GET /seller/variants/:id/stock` before re-enabling Save, and show the server's level.
  - Proper fix, a contract change: mint an `Idempotency-Key` per submission in the ViewModel (in `SavedStateHandle`) and have the server dedupe on it.

### M4. A PDF seller document can never upload, although the screen invites PDFs
- **Files:**
  - `feature/seller/.../seller/DocumentScreen.kt:61-66`: the screen says "a PAN card is often a PDF" and uses the general `GetContent` picker.
  - `DocumentViewModel.kt:305` accepts `application/pdf`.
  - `DocumentViewModel.kt:192` calls `uploader.reserve(mimeType, sizeBytes)`, which defaults `fileType = "image"` (`core/media/.../upload/MediaUploader.kt:69-86`).
  - Server: `Architecture/services/media-service/internal/service/media.go:301` → `validation.go:55-61, 82-95`. `file_type=image` allows only jpeg, png, webp, gif and heic. The handler binds `file_type` as `oneof=image video audio` (`internal/http/handler.go:270`), so no document type exists.
- **Scenario:** A seller picks their PAN PDF. Init is refused with 400 "invalid image type". The seller sees "The upload could not start." (`DocumentViewModel.kt:194`), which reads as a network fault. They retry, and each retry also counts against the media upload rate limit (M5).
- **Fix:** Until media-service accepts documents, drop `application/pdf` from `AcceptedDocumentTypes` and change the screen copy to "photo of the document". Use an image picker with JPEG/PNG. Longer term, add a `document` file type and a private access scope on the server for KYC files.

### M5. Seller uploads share the media-service limit of 10 uploads per hour, and failures surface as "could not start"
- **Files:**
  - `media-service/internal/service/validation.go:40-41` (`MaxUploadsPerHour = 10`, `MaxUploadsPerDay = 30`) and `ratelimit.go:30`, applied per user at init.
  - `feature/seller/.../seller/ProductImages.kt:19`: `MAX_PRODUCT_IMAGES = 8`.
  - `ProductImagesViewModel.kt:184` and `DocumentViewModel.kt:194`: every init failure says "The upload could not start." The error is not inspected.
- **Scenario:** A new seller onboarding uploads a PAN, a two-sided address proof (3 uploads), then lists their first product with 8 photos. The 11th upload in the hour fails. The seller sees generic failures on photos 8 and later, retries, and burns more of the daily 30. A seller listing 4 products in a day is locked out of uploads, including posting to the social feed, which uses the same counter.
- **Fix:**
  - Client: map media-service's rate-limit error (429) to "You've uploaded a lot in the last hour — try again in N minutes" and stop auto-retrying.
  - Server, the product decision: give commerce uploads (an upload purpose of `product` or `kyc`) their own budget.

### M6. Product search shows results for an earlier query when responses arrive out of order
- **Files:**
  - `feature/commerce/.../browse/StoreBrowseViewModel.kt:102-120`: `load()` launches a new coroutine per debounced query and never cancels the previous one. The last response to arrive wins.
  - `StoreBrowseViewModel.kt:123-147`: `loadMore()` writes `current.copy(...)` from a snapshot taken before a new query started, so an append that lands after a new search restores the old query's list.
- **Scenario:** The buyer types "shirt", pauses for 300 ms, then types "shirts". The "shirt" request is slow and returns after the "shirts" request, so the grid shows "shirt" results under the "shirts" query. The same happens if they scroll (triggering `loadMore`) and then type: the old list comes back.
- **Fix:** Keep a single `Job` for loading, and cancel it in `load()` before launching. In `loadMore`, drop the result unless the state is still the same `Content` (compare the query, or a load generation counter).

---

## LOW

### L1. A failure on one bag row undoes another row's successful update and leaves that row stuck busy
`feature/commerce/.../bag/BagViewModel.kt:112-129`: the failure branch restores the `current` snapshot taken when this mutation began. If row A was busy at that moment and has since succeeded, row B's failure (for example a 409 out of stock) puts back A's old quantity, the old subtotal and A's busy flag. Row A then stays disabled until the screen refreshes. **Fix:** on failure, edit `_state.value` (remove only this variant from `busyVariantIds` and set the message) rather than restoring the snapshot, or reload the cart.

### L2. The checkout response's breakdown is computed on the client, against the class's own rule
`CommerceRepository.kt:519-529` builds `subtotal = total − shipping` and `discount = 0` because `CheckoutResultDto` has no `subtotal_minor` or `discount_minor`. `adoptPlacedOrder` stores it as `serverBreakdown` (`CheckoutViewModel.kt:452`). No screen renders it today, so this is latent. It would be wrong as soon as a discount or coupon exists. **Fix:** keep the quoted breakdown and take only `total_minor` from the response, or have the server return the full breakdown.

### L3. `ADDRESS_NOT_FOUND` is shown as "We couldn't find that order."
`CommerceRepository.kt:1146` folds `ADDRESS_NOT_FOUND` into `OrderNotFound`, and `CheckoutViewModel.kt:604-605` renders order copy with no retry. A buyer whose address was deleted on another device gets a misleading dead end. **Fix:** add a `CommerceError.AddressNotFound` and route it to "Change address".

### L4. The payout screen has no FLAG_SECURE
`feature/seller/.../seller/PayoutScreen.kt:100-121` shows the full bank account number, IFSC and holder name in plain text. The window is not secured, so the details appear in the recents thumbnail and in screenshots. Chat, hidden chats and dating already use `core/common/.../window/SecureWindow.kt`. **Fix:** hold `SecureWindow` while the payout screen and the document screen are shown. The checkout screen shows only totals and the address summary; Razorpay's own sheet is the card surface.

### L5. The payout form's prefill can overwrite a name the seller has typed
`PayoutViewModel.kt:87-99`: when `sellerProfile()` returns, it copies `storeName` over `accountHolderName` regardless of what the seller has typed since. **Fix:** prefill only if the field is still blank.

### L6. `parseRupees` can overflow silently
`NewProductViewModel.kt:146` computes `it * PAISE_PER_RUPEE + fraction` on a Long without checking for overflow. A pasted 17–19 digit price wraps around, and some values wrap to a positive number. **Fix:** use `Math.multiplyExact` and `addExact` inside `runCatching`, or cap the rupee digits at about 9.

### L7. The delivery-pin path combined with process death shows "Confirming your payment" for 180 s when no sheet was ever opened
`adoptPlacedOrder` sets `phase = SheetRequested` before the pin confirmation (`CheckoutViewModel.kt:459`, `CheckoutScreen.kt:170-174`). After process death, recovery polls (`:327-329`) an order that was never offered to the PSP. **Fix:** add a `PinPending` phase, or set `SheetRequested` in `onOpenPaymentSheet`.

### L8. Smaller items
- `RazorpayPaymentLauncher.kt:200` logs the whole `PaymentOutcome`. For `Failed`, that includes Razorpay's raw `response` JSON (error description and metadata, including payment and order ids). It is not card data, but it is payment data in logcat on release builds. Log only the outcome type and code.
- `ProductImagesViewModel.kt:179, 221-222` and `DocumentViewModel.kt` call `MediaSourceResolver.resolve` (a `ContentResolver.getType` plus a `query`) on the main dispatcher. Move it to `Dispatchers.IO`. The MIME type is also the provider's declared type, not sniffed from the bytes; the server must keep sniffing.
- `Paise.formatWithSymbol()` always prints ₹ and ignores `PriceBreakdown.currency`. That is correct while the server is INR-only, but the `currency` field is never checked.
- Golden fixture `variant_patch_200_price_live.json` differs from the server copy by a trailing newline. That breaks the AGENT-RULES "byte for byte" rule, not behaviour.

---

## Checked and fine
- **Money on the wire:** every checkout, quote, order, cart and payment amount is `Paise` (a Long) with `*_minor` names matching the server's json tags. `PaiseSerializer` rejects fractional values. The remaining rupee-float seller routes (earnings, return `refund_amount`) are decoded via `BigDecimal` (`RupeesAsPaiseSerializer`). Seller price entry is integer parsing, with no `toDouble`. The only `Double` is `rate_percent`, a tax rate. `avg_rating` is a Float, which is fine.
- **Checkout contract:** `QuoteRequest` and `CheckoutRequest` match `PrepareQuote` and `CheckoutP0` (`address_id`, `quote_id`, `payment_method`, `expected_total_minor` > 0, `Idempotency-Key` header). `expected_total_minor` echoes the server's quote total and is never computed on the client. `PRICE_CHANGED` and `OUT_OF_STOCK` details are parsed by type.
- **Server-side protection:** the client sends no coupon, but the quote binds one, so the two match. The idempotency fingerprint covers the quote, method and address.
- **Idempotency key:** one key per attempt, stored in `SavedStateHandle` before the first request. A new key is minted only after a price acknowledgement or a conflict. Place order is disabled while placing.
- **Payment sheet:**
  - Only the server's `client_session` (`order_id`, `key_id`) is used, and the amount is relayed from the intent, not chosen. `retry.enabled=false`.
  - Every SDK ending (success, error, cancel) leads to polling the server. A client "success" is never treated as paid.
  - Refund outranks paid. Permanent 4xx responses stop the poll, while transient errors keep polling. The poll is bounded at 180 s and its timeout copy says "don't pay again".
  - Attempts are matched by application, order and attempt id, and a one-flight launcher slot prevents a result being delivered to the wrong checkout.
- **Process death:** the phase, key, quote, accepted total, order id and attempt survive. Recovery asks the server and never auto-reopens a sheet. Provider session material is deliberately not persisted.
- **Configuration changes:** `MainActivity` declares `configChanges` for rotation, uiMode, fontScale and others, so a rotation does not dispose the screen and abandon an in-flight sheet.
- **Platform security:** no WebView in any commerce or payment path, and no JS bridge. No commerce deep links or return URLs; the payment result comes through the Razorpay SDK callback on `MainActivity` (`ActivityPaymentHost`). The exported activities are the launcher and verified https links for chat, posts and OAuth, none of which lead into commerce. `allowBackup=false`.
- **HTTP layer:** logging is `BASIC` in debug and `NONE` in release. OkHttp's `retryOnConnectionFailure(false)`, and `RetryInterceptor` retries only GET/HEAD and `@Retryable` methods, none of which are commerce writes. No card data is stored anywhere; Aadhaar numbers are never sent.
- **Seller writes:** routes resolve the seller from the caller, and the server refuses `NOT_YOUR_PRODUCT`, `NOT_YOUR_VARIANT` and `NOT_YOUR_MEDIA`. The client holds ids only from its own listings. Pack, ship, cancel, return actions, price edits and new product submissions each have a busy guard. Image MIME types are allow-listed, size is capped at 8 MB (10 MB for documents), and a zero or unknown size is rejected.
- **Paging and memory:**
  - Server paging parameters match the client: cursor for products, orders and favourites; offset for seller orders, returns, earnings and products. Pages are de-duplicated by id.
  - Lists are Lazy with stable keys, and Coil `AsyncImage` loads at layout size. Lists grow only with what the user scrolls, with no eager full load.
  - Seller earnings totals are labelled as a sum of the loaded rows.
- **Media reclamation:** `upload_purpose=composer` on product and KYC uploads is harmless today, because confirmed-media reclamation is disabled (`media-service/internal/store/postgres/reclaim_policy.go:351-358`). Revisit this before a sweeper ships.

## Not verified
- Whether media-service serves KYC document images under a private access scope. Upload goes through the general `image/general` path, but I did not trace the serve path.
- Whether Razorpay's own checkout activity sets FLAG_SECURE. It is a third-party activity.
