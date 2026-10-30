# Audit: Android food (Feast customer, Kitchen, Rider)

Branch `codex/module-01-02-launch-safety`. This was a read-only audit: I made no edits except this file, ran no git writes, no Gradle builds and used no device.
Android paths are relative to `mobile/android/`. Server paths are relative to the repo root.

## Scope

**Modules covered:**
- `core/food`: the Retrofit APIs (`FeastApi`, `KitchenApi`, `RiderApi`, `FoodApi`), DTOs, `Paise`, repositories, `FoodError` mapping and the realtime token source.
- `feature/feast`, which is the customer food app. There is no `feature/food`; Feast is the customer module.
- `feature/kitchen`, plus its host `app-kitchen` (manifest, `KitchenNewOrderAlert`, push registration).
- `feature/rider`, plus its host `app-rider` (manifest, `RiderLocationService`).

`feature/mopedu-rider` is out of scope. It is the Mopedu ride-hailing passenger app on rider-service `/v1/rider`. It delivers no food orders and calls no food-service route. Its only food mention is a comment in `MopeduPayments.kt:24`.

**Server side checked:**
- `Architecture/services/food-service/internal/http` (route registration in `handler.go:42-182`, `handler_b8.go`, `handler_delivery_offers.go`, `handler_delivery_proof.go`, `handler_errors.go`).
- The store paths behind each finding.
- The `/v1/food`, `/v1/partner` and `/v1/realtime` entries in `api-gateway/cmd/server/main.go:652,734,741,1387`.
- `notification-service/internal/service/food_push.go`.

**Contract checks:**
- Every Retrofit path in the four food APIs (77 routes) was matched against the gin routes. None is retired or missing. `/v1/partner/work-session` is served by rider-service (`rider-service/internal/http/handler.go:63`), and the gateway routes `/v1/partner` there.
- All 110 golden fixtures in `core/food/src/test/resources/contracts/` are byte-identical to food-service's `testdata/contracts/` (`cmp`, 0 diffs).
- Each finding below was traced end to end. Anything I could not confirm was dropped or moved to "Not verified".

---

## HIGH

### H1. Feast Rider never alerts a rider to a new job offer while the app is in the background, and offers live 20 s
- **Files:**
  - `app-rider/src/main/AndroidManifest.xml:96-98` removes `UsMessagingService` with `tools:node="remove"`. The comment says "none of which a rider receives". This means `food_delivery_offer` pushes can never be received, even once a `google-services.json` exists.
  - Yet notification-service sends exactly that push to the `feast_rider` app on channel `rider_job_offer` (`notification-service/internal/service/food_push.go:20,38`, `internal/push/fcm.go:111,192`).
  - The in-app path does not cover this:
    - Offers come only from `RiderLiveFeed` (SSE plus a 15 s poll), owned by `HomeViewModel` (`feature/rider/.../home/HomeViewModel.kt:108-114`).
    - `onMessage` only updates `HomeUiState.offers`. Nothing in `feature/rider` or `app-rider` posts a notification or plays a sound: the `RIDER_JOB_OFFER` channel is created (`RiderApplication.kt:22`) and never used.
  - `HomeScreen.kt:149` asks the rider to "Allow notifications so an offer can reach you while the app is in the background". Nothing delivers on that promise.
  - Server: `offerTTL = 20 * time.Second` (`food-service/internal/service/delivery_dispatch.go:50`).
- **Scenario:**
  - A rider goes online. The location foreground service keeps the process alive, and the rider pockets the phone, or has Google Maps in front from the previous job (`ActiveJobScreen.kt:111,138` hands navigation off to Maps).
  - An offer arrives. The SSE frame lands and the Home list updates off-screen, with no sound, vibration or heads-up.
  - Twenty seconds later the offer expires and dispatch moves on. This repeats for every offer: an online rider effectively never gets work unless they stare at Home.
- **Fix:**
  - When a `Message` frame or a refresh yields an offer id not seen before, post a high-priority notification on `rider_job_offer` with a full-screen or heads-up intent to `/rider/offers/{id}`. Do this from a process-scoped collector (or from `RiderLocationService`, which is already running while online), not from `HomeViewModel`. Cancel it when the offer leaves the pending list.
  - Separately, keep `UsMessagingService` in `app-rider`, or a rider-specific subclass, so the server push works once Firebase is configured. `RiderActivity` already parses `fromPush` (`RiderActivity.kt:58`).

### H2. The kitchen's order detail never shows what was ordered
- **Files:**
  - `feature/kitchen/.../queue/OrderDetailViewModel.kt:41-42` says "ROUTE GAP: there is no partner `GET /orders/:id`". `load()` (`:63-79`) finds the order inside `GET /partner/restaurants/:id/orders`.
  - That list never carries items:
    - `ListPartnerOrders` → `listOrdersByWhere` → `listOrdersByWherePage` (`food-service/internal/store/postgres/ops.go:363-368, 1597-1636`) scans only the order columns (`scanOrder`, `customer.go:1276-1301`) and loads no items.
    - `OrderDetailScreen.kt:131-136` then renders "Item details aren't available for this order." for every order opened from the queue.
  - The kitchen queue row (`KitchenOrderDto`, `core/food/.../network/KitchenDtos.kt:140-153`) has only `item_count` and `customer_instruction`.
  - The route does exist: `partner.GET("/orders/:orderId", h.GetPartnerOrder)` (`handler_b8.go:32, 179-186`) returns the order with items via `getOrder(..., true)` (`partner_b8.go:454-466`). It is golden-pinned as `partner_order_get_200.json`, which Android already holds and decodes, but `KitchenApi` has no method for it.
- **Scenario:** A new order rings. The cook taps it to see what to make before accepting. The screen shows the total, the status and "Item details aren't available". The only time items show up is right after Mark ready, when the transition response (which does load items) replaces the order. That is after the food was cooked. The kitchen accepts blind or phones the customer.
- **Fix:**
  - Add `@GET("v1/food/partner/orders/{orderId}") suspend fun getOrder(...): Response<ApiEnvelope<PartnerOrderDto>>` to `KitchenApi`, and have `OrderDetailViewModel.load()` call it.
  - Delete the stale ROUTE GAP comment.
  - Add a fixture test that the decoded `partner_order_get_200` has non-empty `items`.

---

## MEDIUM

### M1. An ambiguous "Place order" failure orphans an order, and the retry then says "cart is empty"
- **Files:**
  - `feature/feast/.../checkout/FeastCheckoutViewModel.kt:207-227`. On every failure, `saved.phase = FRESH` (`:213`). The key is kept only for `FoodError.Network` (`:217`). Any non-IO failure clears it, and that includes a gateway 502 or 504, a 503, or `FOOD_IDEMPOTENCY_IN_PROGRESS`, because these map to `Unexpected` (`FoodError.kt:126-135`).
  - `:121-123` and `:126-138` let the customer change the method or the address while the kept key is outstanding. The server hashes the whole `PlaceOrderInput` and answers 409 `FOOD_IDEMPOTENCY_PAYLOAD_MISMATCH` (`customer.go:1075`, `checkout_validation.go:22-26`). The client then clears the key too.
  - Server: `PlaceOrder` deletes the cart inside the order transaction (`customer.go:684`). A new key then fails with `ErrCartEmpty` (`customer.go:447`), surfaced as 400 `FOOD_ORDER_PLACE_FAILED` "cart is empty" (`handler.go:615-620`).
  - `start()` (`:174-176`) treats `FRESH` as "load the cart". After process death or a back-out following a network failure, the saved key is never resent.
  - Neither `OrdersScreens.kt` nor `OrderTrackingScreen.kt` offers a way to pay a `PAYMENT_PENDING` order. The only action is Cancel.
- **Scenario:**
  - The customer taps Place order on a weak connection. The server commits the order and clears the cart, and the gateway answers 504.
  - The checkout returns to `Ready` with a fresh key. The second tap gets "cart is empty" under a bill that still lists the food.
  - Backing out shows an empty cart. The real order sits unpaid in Orders until its hold lapses, and the customer has to re-add everything.
  - The same thing happens when, after a network failure, they switch UPI to Card and retry.
  - No money is taken twice: the server is safe. The app is what loses the order.
- **Fix:**
  - On `Network`, `Unexpected(status >= 500 or null)` and `FOOD_IDEMPOTENCY_IN_PROGRESS`, keep `phase = SUBMITTING` and the key. Lock the method and address rows until the key is resolved, and have "Try again" resend the same body under the same key.
  - Clear the key only on a definite 4xx refusal.
  - Add "Pay now" for a `PAYMENT_PENDING` order on tracking and orders, routed through the existing `retryPayment` path (a new attempt on the same order).

### M2. A rider can go offline in the middle of a delivery, which stops tracking and can block entering the delivery code
- **Files:**
  - `feature/rider/.../home/HomeScreen.kt:246` shows "Go offline" with no job check. `GoOnlineFlow.onGoOfflineTapped` (`GoOnlineFlow.kt:113-117`) only checks `state == Online`.
  - `RiderLocationService.kt:131-136` / `LocationCadence.kt:363-366`: two minutes without any fix (`NO_FIX_TIMEOUT_MILLIS = 120_000`) triggers `GoOffline(NO_FIX)`, even with `onJob == true`.
  - `RiderRootViewModel.signOut()` does the same.
  - `goOffline` (`RiderLocationService.kt:170-182`) stops location updates and calls `setAvailability(false)`, ignoring the answer.
  - Server, when `partnerwork` is not enabled (opt-in, `shared/partnerwork/rollout.go:12`): `SetDeliveryAvailability` sets `status='OFFLINE'` with no active-job check (`ops.go:533-561`). `DeliveryUpdateAssignment` and `RiderVerifyDeliveryCode` then refuse with 403 `FOOD_DELIVERY_PARTNER_NOT_ACTIVE` (`ops.go:600-601`, `delivery_proof.go:199-200`).
  - When `partnerwork` is enabled, `domain_duty` refuses with `BUSY`, but the client has already stopped the service and shows "offline". The server thinks the rider is online, and no pings arrive.
- **Scenario:** A rider carrying an order waits two minutes in a basement lift lobby with no fix. The service stops itself, and the customer's map freezes. At the door, the rider enters the customer's code and gets "Your rider account isn't active. Go online again". They must go through Go online with the food in hand. A rider who taps Go offline by mistake hits the same wall.
- **Fix:**
  - While `duty.onJob` is true:
    - Disable Go offline, or confirm with "you have an active delivery".
    - Make the no-fix watchdog warn and keep running rather than `GoOffline`.
    - Defer the sign-out offline step until the job ends.
  - In `goOffline`, keep the service running if the server refuses. Server side, consider refusing `is_online=false` while an assignment is active, matching `partnerwork`'s `BUSY`.

### M3. A lost "Accept offer" response tells the rider the offer was taken while they actually hold the job
- **Files:**
  - `feature/rider/.../offers/OfferViewModel.kt:374-394`. On any failure it calls `load()`, which reads `GET /delivery/offers/me` and sets `gone = true` when the offer is not listed. `OfferScreen.kt:50-53` then says "Another rider took it, or its time ran out".
  - The list holds only `status='pending'` offers for riders with no active assignment (`delivery_offers.go:82-92`). So after a committed accept, the offer is never listed.
  - The accept route takes no `Idempotency-Key` (`RiderApi.kt:201-202`). A replay answers 400 `DELIVERY_ACCEPT_FAILED "offer not pending: accepted"` (`handler_delivery_offers.go:38-41`, `delivery_offers.go:181`).
  - `duty.setOnJob(true)` runs only on success (`:385`), so the location cadence stays at the idle 30 s / 50 m rate.
- **Scenario:** A rider accepts on a flaky connection. The server assigns the order, and the read times out. The screen says the offer is gone. The rider waits on Home, and the job card appears only at the next feed refresh (up to 15 s later, or 60 s while LIVE) if they notice it. Meanwhile the restaurant's food waits, the customer's ETA is computed from idle-rate pings, and the assignment's own confirm step is pending.
- **Fix:** On any accept failure, read `GET /delivery/assignments/current` first. If it holds this offer's `order_id`, treat it as accepted (`setOnJob(true)`, navigate to the job). Server side, make accepting an offer that is already accepted by the same partner return 200.

### M4. The kitchen's background new-order alert depends on a process Android will freeze, and FCM is not wired
- **Files:**
  - `feature/kitchen/.../queue/OrderQueueViewModel.kt:50-53` claims the queue "runs ... also while the app is in the background". It is `viewModelScope` work in an app with no foreground service: the `app-kitchen` manifest declares none.
  - `KitchenNewOrderAlert.kt:46-54` posts the insistent notification only when that in-process loop ticks.
  - FCM is disabled without `google-services.json` (`KitchenApplication.kt:51`). The comment says new-order alerts come from the in-app queue.
- **Scenario:** A kitchen tablet sits with the screen off, or a staff member switches to another app. The activity stops, and the process becomes cached. On current Android, the cached-app freezer suspends it and network access is deferred. An order arrives, nothing polls, nothing rings, and the 15 s server sweep auto-rejects it after the accept deadline. The customer gets a refund instead of food.
- **Fix:** Ship the kitchen with FCM configured: `food_order_new` already targets `feast_kitchen` on `kitchen_new_order` (`food_push.go:19`). Or, while the restaurant is accepting orders, run a `dataSync` / `specialUse` foreground service that owns `LiveOrderQueue`. Treat the in-app loop as the foreground path only.

### M5. Kitchen "Preparing" and "Awaiting pickup" are drawn from the latest 100 orders of any status
- **Files:**
  - `OrderQueueViewModel.kt:168-178` filters `kitchen.orders(restaurantId)` client-side by status.
  - Server `ListPartnerOrders` → `listOrdersByWhere` has a fixed `Limit: 100`, ordered by `created_at DESC`, with no status filter and no paging (`ops.go:1597-1599, 1621-1622`). Abandoned `PAYMENT_PENDING`, delivered and cancelled orders all count.
  - `OrderDetailViewModel.load()` also searches the same 100.
- **Scenario:** A cloud kitchen at dinner peak takes more than 100 orders, including abandoned checkouts, after an order that is still waiting for a rider. That order drops off "Awaiting pickup", and its detail says "This order is no longer in your list", so the kitchen cannot find it to take the rider's pickup code.
- **Fix:** Client: use `GET /partner/orders/:id` for detail (H2). For the boards, ask the server for active statuses only: add a `status` filter or a dedicated active-orders route, paged. Until then, show a "showing the latest 100" note.

---

## LOW

### L1. The offer countdown trusts the device clock against a 20 s absolute deadline
`OfferCountdown.at` (`offers/OfferCountdown.kt:41-49`) compares `Instant.now()` with the server's `expires_at`. `accept()` refuses locally when it reads expired (`OfferViewModel.kt:377-379`). A phone whose clock runs 20 s fast shows every offer as expired and will not let the rider accept. A clock running behind shows offers that the server will refuse. The kitchen solved the same problem with `seconds_to_breach` plus the receipt time (`AcceptCountdown.kt:124-126`). **Fix:** have `GET /delivery/offers/me` return `seconds_to_expiry`, and anchor it to the response's arrival time. Never refuse locally, only grey the button.

### L2. The checkout bill can differ from what the customer is charged, and nothing compares them
`FeastCheckoutViewModel.loadCart` prices the screen once (`:183-205`). `refreshAddress` (`:126-138`) changes the address without re-reading the cart. `PlaceOrderRequest` carries no expected total (`FeastDtos.kt:421-425`; the server has no such field, `handler.go:594-599`). `adopt()` (`:231-235`) opens payment without comparing `order.money.totals_paise.final_amount_paise` to `bill.total`. A menu price edit or a fee change between screen open and Place order is charged as the server re-prices it. The customer sees the new figure only on Razorpay's sheet. Client totals are never computed (see below), so this is a stale display, not client arithmetic. **Fix:** compare after placing, and on a mismatch show "the total changed to X" before opening the sheet. Re-read the cart after an address change. Ideally, add `expected_total_paise` server-side, as commerce has.

### L3. A stale looping kitchen alarm survives a process restart
`KitchenNewOrderAlert.postedCount` is in memory (`:44`), and `cancelNotification()` returns early when it is `NOT_POSTED` (`:127-131`). If the process dies after posting the `FLAG_INSISTENT` notification (`:122`), the next process can never cancel it. Opening the app from the launcher, rather than the notification, leaves the alarm looping until someone swipes it away, even though every order has been answered. **Fix:** call `NotificationManagerCompat.cancel(NOTIFICATION_ID)` unconditionally in `silence()`, which is cheap and idempotent.

### L4. No FLAG_SECURE on the partner apps' bank, PAN/GSTIN, licence and selfie screens
No file in `feature/kitchen`, `feature/rider`, `app-kitchen` or `app-rider` uses `SecureWindow` or `FLAG_SECURE`. The affected screens are:
- `kycui/BankAccountForm.kt:87-93` in both modules. "Re-enter account number" is a plain `KeyboardType.Number` field, so the full number shows in clear text.
- Kitchen compliance (PAN, GSTIN).
- Rider documents and the selfie preview.

The recents thumbnail and screenshots capture them. The saved-account read-back is masked (`PayoutScreen.kt:47-53`). **Fix:** hold `core/common`'s `SecureWindow` on the payout, compliance, documents and selfie destinations, as chat and dating already do.

### L5. Live screens keep polling while the app is backgrounded
These loops run on `viewModelScope` with no lifecycle gating, so they continue for as long as the destination is on the back stack:
- `OrderTrackingViewModel.poll` (10 s, or 30 s while live, plus SSE; `:190-199`).
- `ActiveJobViewModel` (every 10 s: `currentAssignment` plus `batch` plus `assignments`; `:80-87, 238-250`).
- `OfferViewModel` (1 s tick).

A customer who leaves tracking open in the background keeps the radio awake for the whole delivery. The rider process is kept alive by the foreground service, so the rider loops never stop while on duty. **Fix:** gate them with `repeatOnLifecycle(STARTED)` (collect `state` through `stateIn(WhileSubscribed)` and start the loops from there). For the rider, keep only what the foreground service needs.

### L6. A lost delivery-code response makes the job vanish without "Delivered"
`ActiveJobViewModel.verifyCode` (`:123-159`) maps an IO failure to `Failed` and keeps the code. The next 10 s poll gets 404 `FOOD_DELIVERY_CURRENT_NOT_FOUND` and clears the assignment (`:256-259`), without sending `finished` or a success message. The rider cannot tell whether the delivery counted. The server is replay-safe for the same code (`delivery_proof.go:219-221`). **Fix:** on `Network`, retry `verify-delivery` with the same code once or twice before giving up. If the poll later shows no current job, check `history` for this assignment and show "Delivered".

### L7. The rider app-link filter also claims `http` and cannot verify
`app-rider/src/main/AndroidManifest.xml` adds `<data android:scheme="http" />` next to https for `/rider/digilocker/return` and `/rider/offers/`, and the hosts are `.invalid` placeholders (`app-rider/build.gradle.kts:57-65`), so `autoVerify` cannot succeed. Another installed app can claim the same links and receive the DigiLocker `code` and `state`. The damage is limited: `state` is bound to the rider (`kyc_digilocker_callback_403_state_not_yours`), and the code exchange is server-side. It can still burn the rider's attempt. **Fix:** https only, and real hosts with `assetlinks.json` before release.

### L8. Smaller items
- `SelfieScreen.kt:147, 162-171` decodes the camera JPEG (with `inSampleSize = 4`) and reads EXIF inside `remember` during composition, on the main thread. Move it to `Dispatchers.IO` with `produceState`.
- `FeastPayments.kt:123` logs the order id and the `FoodError`, including the server message, at `Log.w` in release builds. This is not PII, but it is order data in logcat. Log only the error code.
- A kitchen pickup lockout (429 `FOOD_PICKUP_CODE_ATTEMPTS_EXCEEDED`, `handler_errors.go:31`) falls through `pickupMessage` (`OrderDetailViewModel.kt:143-148`) to the generic mapping. The cook gets no "too many wrong codes, contact support".
- `OrderQueueViewModel.accept` (`:106-116`) refuses locally once the countdown has expired. This applies even when the tap is a retry of an accept whose outcome was unknown, with its key still kept (`:190-193`). The queue refresh shows the truth, but the cook is told "too late" for an order they may have accepted.
- `OrderDetailViewModel.kt:41-42`: the "ROUTE GAP" comment is stale (see H2).

---

## Checked and fine
- **Money on the wire:**
  - Every amount is `Paise` (a `Long` value class with `addExact`/`subtractExact`). The `*_paise` and `amount_minor` names match the server's json tags.
  - Legacy float-rupee fields decode through `RupeesAsPaiseSerializer` via `BigDecimal`, never `Double`.
  - Kitchen and rider price entry parses decimal text to paise (`RupeeFormat.kt`, `MenuItemEditorViewModel.kt:42`).
  - Tax rates are `BigDecimal` or basis points. No screen adds money: `CartBill` only reads `totals_paise` and `taxes_and_charges`, and `total` is `final_amount_paise`.
- **Checkout idempotency:**
  - One `Idempotency-Key` per decision, persisted in `SavedStateHandle` before the request leaves, and resent after process death in `SUBMITTING` with no order id.
  - Place order is disabled while placing, and the method cannot change while placing.
  - The payment-intent key is per attempt.
- **Payment result:**
  - The sheet's ending is never treated as paid. Every `SheetClosed` polls `GET /orders/:id/payment` through `PaymentCoordinator.confirm`, and only the server's `paid` renders `Paid`.
  - Refund outranks paid, and an unknown status keeps confirming. A poll timeout shows "Still confirming — don't pay twice".
  - Events are matched by application id `feast` plus the attempt, and consumed once.
  - Process death resumes from the saved phase by asking the server, never by reopening a sheet. No provider session material is persisted.
  - Server side, every intent for an order shares the payments idempotency key `food_order:<id>` (`internal/payments/client.go:191`), so "Try paying again" cannot mint a second provider order for the same food order. The amount comes from the database.
- **Order tracking reconnect:**
  - SSE gets a fresh scoped token on every (re)connect (`FoodRealtimeTokenSource`, no cache).
  - A `Connected` event re-reads the order, and stale `rider.location` frames (older `recorded_at`) are dropped.
  - Non-location events trigger a re-read rather than a patch, a poll backs up a silent drop, and streaming stops at a terminal status.
  - The delivery code shows only in `PICKED_UP` / `OUT_FOR_DELIVERY`, and only when the server sent it.
- **Kitchen transitions:**
  - Accept, reject and mark-ready each have a per-order busy guard and a key kept only while the outcome is unknown.
  - Server-side state guards turn a double accept, accept against reject, or accept against a customer cancel into `FOOD_ORDER_TRANSITION_NOT_ALLOWED`, which the app shows as "already moved on" before refreshing.
  - The accept deadline uses `seconds_to_breach` anchored to the fetch time, not the device clock.
  - Verify-pickup is replay-safe server-side (`delivery_proof.go:111-113`), and the button is disabled while verifying.
  - Pickup and delivery codes are verified only on the server, with attempt lockouts.
- **Rider location:**
  - A location foreground service of type `location`, started only after the prominent disclosure, the foreground grant and the server's yes.
  - No background-location permission: it is removed from the manifest.
  - The cadence is 30 s / 50 m balanced when idle and 8 s / 15 m high accuracy on a job. Pings are conflated, so there is no backlog, and out-of-order fixes are dropped.
  - The `LocationCallback` is removed on every reconfigure, on offline and in `onDestroy`, and the scope is cancelled.
  - Feast and Kitchen use a one-shot `getCurrentLocation` with a cancellation token, and the geocoder runs on IO.
- **Rider steps:** assignment steps carry keys kept only on unknown outcomes. Delivery-code entry is digits only, at most 4, with no double submit, and handles wrong, locked, not-picked-up, not-yours and not-active as distinct outcomes.
- **Privacy:**
  - The rider sees the customer's first name, address, landmark and instructions only in `drop`, which the server sends from `ACCEPTED` until delivery. History keeps only city and locality (`drop_summary`).
  - No customer phone is ever sent to the rider; the dialler is for the restaurant phone.
  - Nothing persists addresses, phones or locations on the device. The only `SharedPreferences` writes are the disclosure flag and the pending DigiLocker `state` and expiry.
  - `allowBackup=false` with `fullBackupContent=false` in both partner apps. No food module logs coordinates, addresses or phones.
- **Exported components:**
  - Each partner app exports only its launcher activity. The rider's `RiderLocationService` and `SelfieFileProvider` are `exported=false`.
  - Push and deep-link parsing (`kitchenPushTarget`, `RiderDeepLinks.parse`) accepts only relative paths with UUID or charset-checked ids and never forwards an arbitrary URI.
  - Maps hand-off is limited to the server's https links.
  - Momentum exposes no Feast deep link: tracking opens from its own notification with a server-checked order id.
- **Paging and memory:**
  - Server lists are capped (customer orders 50, rider history 100, settlements 100, restaurants `limit=50`), and the client never fetches further pages.
  - Menus, orders and queues render in Lazy lists with stable keys.
  - No map SDK ships (`MapSurface` and `MapPreview` are placeholders), so there is no map memory.
  - The kitchen and rider live feeds conflate fetch requests through one channel.

## Not verified
- Whether payments-service returns the same provider order when a retry switches UPI to Card under the per-order idempotency key, or refuses the switch. That decides whether Feast's "Pay with Card instead" works after a failed UPI attempt.
- Whether `food-service`'s cart pricing already uses the selected address for the delivery fee. L2 holds either way, because nothing compares the two figures.
- The real offer and accept-deadline behaviour on Android 14+ cached-app freezing for the kitchen (M4). I reasoned from platform rules, not from a device.
