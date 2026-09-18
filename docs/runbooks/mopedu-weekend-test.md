# Mopedu — weekend test guide

Everything below runs on this PC. Nothing here is started for you in advance:
the code is committed, but the dev containers still run images built before
the Mopedu commits (rider-service's image is from 16 August), so step 2 is
the one that matters.

Accounts: **call_a** is the customer (Momentum app → Mopedu). **call_b** is
the captain (the separate Mopedu Captain app). Your admin account
(`7cd6ea3a-9c80-4f20-806f-5d08de0f914b`, superadmin, 2FA enrolled per the
admin-console guide) drives the console steps.

Every command block is Git Bash. Blocks that read the database print one
line per row; the expected line follows each block.

---

## 1. Check the stack is up

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' | head -60
```

If containers are missing or `Exited`, start the whole stack:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d
```

## 2. Rebuild the services whose code changed since their last dev build

Five images. `identity-user` is the compose name of identity's user-service
(the "mobility" module allowlist change). `api-gateway` has **no** Mopedu
change: it only needs the flag in step 3 (if you have not yet run the
admin-console guide's step 3 rebuild, add `api-gateway` to this line).

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d --build rider-service payments-service notification-service admin-service identity-user
```

Wait about 30 seconds, then confirm they are healthy:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' rider-service payments-service notification-service admin-service identity-user && docker compose exec -T rider-service wget -qO- http://localhost:8116/healthz && echo && curl -s -o /dev/null -w 'payments: %{http_code}\n' http://localhost:8102/healthz
```

Expect five `Up` lines, a `{"status":...}` line from rider and `payments: 200`.

rider-service applies its migrations 001–004 at boot (Hyderabad city, the
pricing engine, fare windows, coupons, outstanding fees, online payments).
Confirm they ran:

```bash
docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
select (select count(*) from rider_cities) as cities,
       (select count(*) from rider_fare_windows) as fare_windows,
       (select count(*) from information_schema.tables where table_schema = 'public' and table_name in ('rider_coupons','rider_customer_outstanding','rider_ride_track_points','rider_ride_refunds','rider_payment_inbox')) as new_tables;
SQL
```

Expect `4|16|5`. Anything else: stop and send me

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 10m rider-service | tail -60
```

## 3. Settings that must be in place

### 3a. `.env` lines (presence only — never print the values)

```bash
cd /c/workspace/modernsmapp/Architecture/docker && for p in '^RIDER_PII_KEYS=v1:' '^RIDER_SERVICE_TOKEN_KEY=.' '^RIDER_SERVICE_TOKEN_KID=.' '^SERVICE_CALLER_RIDER_SERVICE_PUBKEY=.' '^SERVICE_CALLER_RIDER_SERVICE_KID=.' '^SERVICE_CALLERS=.*rider-service' '^RIDER_SERVICE_CALLERS=.*admin-service' '^ADMIN_SERVICE_TOKEN_PUBKEY=.' '^RAZORPAY_KEY_ID=rzp_test_' '^RAZORPAY_WEBHOOK_SECRET=.'; do printf '%-45s %s\n' "$p" "$(grep -c "$p" .env)"; done
```

Expect `1` on every line (they were all `1` on 18 September). What each one
does:

- `RIDER_PII_KEYS` — seals ride OTPs; without it a captain cannot accept an
  offer (`OTP_SEALING_NOT_CONFIGURED`).
- `RIDER_SERVICE_TOKEN_KEY/KID` + `SERVICE_CALLER_RIDER_SERVICE_PUBKEY/KID`
  + `rider-service` in `SERVICE_CALLERS` — rider signs its calls to
  payments-service (UPI rides). Missing: intents answer `503`.
- `RIDER_SERVICE_CALLERS` / `ADMIN_SERVICE_TOKEN_PUBKEY` — the admin console
  reaches rider's `/v1/rider/internal/admin/*`.
- `RAZORPAY_KEY_ID=rzp_test_…` — payments must be on Razorpay **test** keys.
  On the stub gateway an intent has no checkout session and rider refuses it.

`MOPEDU_*` needs no `.env` line: the compose file sets
`MOPEDU_PLATFORM_GSTIN` (synthetic dev GSTIN), `MOPEDU_COUPONS_ENABLED`
(default `true`) and `MOPEDU_SURGE_CAP_BPS` (default `5000`) itself. Check
they reached the container:

```bash
docker exec atpost_stack-rider-service-1 sh -c 'echo "gstin_set=$([ -n "$MOPEDU_PLATFORM_GSTIN" ] && echo yes || echo no) coupons=$MOPEDU_COUPONS_ENABLED surge_cap=$MOPEDU_SURGE_CAP_BPS pii_set=$([ -n "$RIDER_PII_KEYS" ] && echo yes || echo no) digilocker=$DIGILOCKER_MODE"'
```

Expect `gstin_set=yes coupons=true surge_cap=5000 pii_set=yes digilocker=mock`.

### 3b. Open `/v1/rider` at the gateway (`RIDER_PUBLIC_ENABLED`)

The gateway answers `404` on every `/v1/rider` route until its own
environment carries `RIDER_PUBLIC_ENABLED=true` (the dormant-product gate).
The compose file forwards it from `.env` (default `false`), so it takes one
line and a recreate of the gateway container (no rebuild — the gateway code
did not change; note that `docker compose restart` does **not** re-read
environment changes, `up -d` does).

1. `Architecture/docker/.env`, add one line:

   ```
   RIDER_PUBLIC_ENABLED=true
   ```

2. Recreate the gateway and check the gate opened:

   ```bash
   cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d api-gateway && sleep 5 && docker exec atpost_stack-api-gateway-1 printenv RIDER_PUBLIC_ENABLED && curl -s -o /dev/null -w 'cities via gateway: %{http_code}\n' http://localhost:8080/v1/rider/cities
   ```

   Expect `true` and `cities via gateway: 200` (before the flag: `404`).

Set it back to `false` (or remove the `.env` line) after the weekend; staging
and production keep it closed.

## 4. Seed the test data

```bash
bash /c/workspace/modernsmapp/Architecture/services/rider-service/scripts/dev-seed-mopedu.sh
```

Expect a summary ending in `Warnings 0`, with `Captains created=4`, coupon
`created; validate: valid`, `Weekend window created; 1 row(s)` and `4 of 4
seeded captains approved`. Re-running is safe (everything reads `reused` /
`existing`). What it made: call_b as an approved captain with an auto and an
active free-trial subscription (`trial_7d`); three synthetic captains (two
autos, one bike) online near MG Road; coupon `WELCOME50`; the fare window
"Weekend test peak" (Sat–Sun 09:00–21:00, x1.15). Details:
`Architecture/services/rider-service/scripts/README.md`.

**No admin touched a captain.** Onboarding is automatic on the dev stack:
the DigiLocker mock (`DIGILOCKER_MODE=mock`) returns the Aadhaar, the
driving licence and the vehicle RC as issuer-verified, the face-compare mock
(`MOPEDU_FACE_COMPARE_MODE=mock`) matches the selfie with the licence photo,
and rider-service approves the partner the moment all four are verified
(audit rows under the fixed system actor, the `rider.partner.approved`
event, the identity role intent). The subscription is the free trial, taken
with one tap through `POST /v1/rider/subscriptions/checkout` — no payment
proof, no admin verify (that route now answers `410`). The only thing that
still waits for a human is a document a captain uploads by hand (a DL or RC
photo instead of DigiLocker): it sits in the admin document queue, the
captain sees `under_review` with the document named, and the approval
completes itself the moment the admin verifies it.

**Open a second Git Bash window and leave this running for the whole
session** — the stale-GPS worker takes a captain offline 90 s after its last
location ping, and the synthetic captains have no phone:

```bash
bash /c/workspace/modernsmapp/Architecture/services/rider-service/scripts/dev-seed-mopedu.sh --keep-online
```

It prints one line every 30 s. Ctrl-C when you are done.

To wipe and reseed:

```bash
bash /c/workspace/modernsmapp/Architecture/services/rider-service/scripts/dev-seed-mopedu.sh --reset && bash /c/workspace/modernsmapp/Architecture/services/rider-service/scripts/dev-seed-mopedu.sh
```

## 5. Find this PC's Wi-Fi address

It changes (DHCP), and a stale address makes both apps look "offline" when
the backend is fine. Check the PC address **and** that the phones are on the
same subnet (same first three numbers); if they are not, tell me and we
build against the api-dev tunnel instead.

```bash
powershell -NoProfile -Command "Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | Select-Object IPAddress, InterfaceAlias"
```

Take the Wi-Fi one. On each phone: Settings → Wi-Fi → the network's details
show its address; the first three numbers must match.

## 6. Build and install the two APKs

Customer app (Momentum, hosts the Mopedu customer flow) — install on the
**call_a** phone:

```bash
cd /c/workspace/modernsmapp/mobile/android && ./gradlew.bat --offline :app:assembleDevDebug -PdevHost=<ip from step 5>
```

```bash
grep API_BASE_URL /c/workspace/modernsmapp/mobile/android/app/build/generated/source/buildConfig/dev/debug/com/us/android/BuildConfig.java
```

APK: `mobile/android/app/build/outputs/apk/dev/debug/app-dev-debug.apk`.

Captain app — install on the **call_b** phone:

```bash
cd /c/workspace/modernsmapp/mobile/android && ./gradlew.bat --offline :app-captain:assembleDevDebug -PdevHost=<ip from step 5>
```

```bash
grep -r API_BASE_URL /c/workspace/modernsmapp/mobile/android/app-captain/build/generated/source/buildConfig/dev/debug/ | head -2
```

APK: `mobile/android/app-captain/build/outputs/apk/dev/debug/app-captain-dev-debug.apk`.
Install the way you normally do (Android Studio Run, or `adb install -r <path>`).

## 7. One-time helpers for the command steps

rider-service has no host port on purpose, so the command steps below run
`wget` inside its container with the same headers the gateway would stamp.
Paste this block once in the Git Bash window you use for the walkthrough
(again in any new window). It prints nothing.

```bash
export MSYS_NO_PATHCONV=1
mz() { printf '%s' "${4:-}" | docker exec -i atpost_stack-rider-service-1 sh -c 'm=$1; p=$2; u=$3; sc=$4; set -- -S -T 30 -O - --header "X-Internal-Service-Key: $INTERNAL_SERVICE_KEY" --header "Accept: application/json"; [ -n "$u" ] && set -- "$@" --header "X-User-Id: $u"; [ -n "$sc" ] && set -- "$@" --header "X-Scopes: $sc"; if [ "$m" = POST ]; then cat >/tmp/mz.b; set -- "$@" --header "Content-Type: application/json" --post-file /tmp/mz.b; fi; wget "$@" "http://localhost:8116$p" 2>/tmp/mz.h; echo; grep -o "HTTP/[0-9.]* [0-9]* [A-Za-z ]*" /tmp/mz.h | tail -1; rm -f /tmp/mz.b /tmp/mz.h' _ "$1" "$2" "$3" "${5:-}"; }
newid() { powershell.exe -NoProfile -Command "[guid]::NewGuid().ToString()" | tr -d '\r'; }
CALL_A=2d598287-eee7-40b4-a7f5-b46b9412e4e7; CALL_B=66668bc2-a3f6-40a5-9cdd-c998dcf72f29; ADMIN=7cd6ea3a-9c80-4f20-806f-5d08de0f914b
BLR=$(docker exec atpost_stack-postgres-1 psql -U postgres -d app -tAc "select id from rider_cities where name='Bengaluru'"); echo "bengaluru city id: $BLR"
```

Usage: `mz GET <path> <user id>`, `mz POST <path> <user id> '<json>'`, and
`mz POST <admin path> $ADMIN '<json>' admin` for the legacy admin family.
Each call prints the JSON body, then the HTTP status line.

## 8. Walkthrough, in this order

Do it on a Saturday or Sunday between 09:00 and 21:00 — that is what the
seeded window covers. Pickup is MG Road Metro (12.9758, 77.6045), drop
Koramangala 4th Block (12.9352, 77.6245), vehicle **auto**.

1. **Estimate carries the weekend window.** Phone: as call_a open Mopedu,
   set the pickup and drop above, choose Auto. The fare card shows the
   "Weekend test peak" label and a x1.15 surge. Command:

   ```bash
   mz POST /v1/rider/estimate $CALL_A "{\"pickup_lat\":12.9758,\"pickup_lng\":77.6045,\"pickup_label\":\"MG Road Metro\",\"drop_lat\":12.9352,\"drop_lng\":77.6245,\"drop_label\":\"Koramangala 4th Block\",\"vehicle_type\":\"auto\",\"city_id\":\"$BLR\"}"
   ```

   Expect `HTTP/1.1 200 OK` and, in the body, `"surge_bps":1500`,
   `"surge_reason":"peak_hours"`, `"window_name":"Weekend test peak"`,
   `"outstanding_paise":0`, `"tax_note":...`. Outside the window (a weekday,
   or after 21:00) `surge_reason` is `none` and `window_name` is absent —
   that is the window working, not a bug.

2. **Coupon WELCOME50 discounts the first ride.** Phone: enter `WELCOME50`
   in the coupon box; the fare drops by 50% up to Rs 50. Command (same
   estimate plus the coupon; note the `quote_id` — you book with it):

   ```bash
   mz POST /v1/rider/estimate $CALL_A "{\"pickup_lat\":12.9758,\"pickup_lng\":77.6045,\"pickup_label\":\"MG Road Metro\",\"drop_lat\":12.9352,\"drop_lng\":77.6245,\"drop_label\":\"Koramangala 4th Block\",\"vehicle_type\":\"auto\",\"city_id\":\"$BLR\",\"coupon_code\":\"WELCOME50\"}"
   ```

   Expect `"coupon_code":"WELCOME50"` and `"discount_paise"` between 1 and
   5000. A wrong code answers `422` with `COUPON_INVALID`; after call_a's
   first completed ride the same call answers `422 COUPON_FIRST_RIDE_ONLY`.

3. **Book a cash ride.** Phone: Cash → Book. The captain phone (call_b,
   online) gets the "New ride offer" push and the offer card. Command
   alternative (uses the quote from step 2; a quote lives 5 minutes):

   ```bash
   QUOTE=<quote_id from step 2>; mz POST /v1/rider/rides $CALL_A "{\"quote_id\":\"$QUOTE\",\"pickup\":{\"address\":\"MG Road Metro\",\"lat\":12.9758,\"lng\":77.6045},\"drop\":{\"address\":\"Koramangala 4th Block\",\"lat\":12.9352,\"lng\":77.6245},\"vehicle_type\":\"auto\",\"city_id\":\"$BLR\",\"payment_method\":\"cash\",\"idempotency_key\":\"$(newid)\"}"
   ```

   Expect `HTTP/1.1 201 Created` with `"status":"requested"`. Save the ride
   id: `RIDE=<id>`. Within a few seconds dispatch offers it to every eligible
   captain within 5 km (call_b plus the seeded autos):

   ```bash
   mz GET /v1/rider/offers/incoming $CALL_B
   ```

   Expect one offer with `"ride_id":"<RIDE>"` and `"status":"sent"`. Offers
   expire after 15 s and are re-batched; if you missed it, the ride expires
   after 5 minutes of searching — book again.

4. **Captain accepts and drives.** Captain phone: Accept → "Arriving" →
   "Arrived". The customer phone gets "Captain assigned", "Captain arriving",
   then "Captain has arrived" with the **OTP** on screen. Captain phone:
   enter the OTP → ride starts → "Complete" → "Cash collected". Each step
   on the customer phone: Ride started → Ride completed → receipt.

   Command alternatives (the captain routes need the ride's current
   `revision`; read it as call_a before each step):

   ```bash
   mz GET /v1/rider/rides/$RIDE $CALL_A | grep -o '"revision":[0-9]*\|"status":"[a-z_]*"'
   ```

   ```bash
   OFFER=<offer id>; mz POST /v1/rider/offers/$OFFER/accept $CALL_B '{}'
   mz POST /v1/rider/rides/$RIDE/arriving $CALL_B '{"expected_revision":<rev>}'
   mz POST /v1/rider/rides/$RIDE/arrived $CALL_B '{"expected_revision":<rev>}'
   mz GET /v1/rider/rides/active $CALL_A | grep -o '"otp":"[0-9]*"'
   mz POST /v1/rider/rides/$RIDE/start $CALL_B '{"otp":"<otp>","expected_revision":<rev>}'
   mz POST /v1/rider/rides/$RIDE/complete $CALL_B "{\"final_distance_km\":7.2,\"final_duration_min\":22,\"idempotency_key\":\"$(newid)\",\"expected_revision\":<rev>}"
   mz POST /v1/rider/rides/$RIDE/payment/cash-confirm $CALL_B '{"expected_revision":<rev>}'
   ```

   The OTP is only readable by the customer, on the active ride and on
   `GET /v1/rider/rides/<id>`, from the moment a captain is assigned until
   the captain uses it (`partner_assigned`, `partner_arriving`, `arrived`);
   before assignment and from `in_progress` on, `otp` is absent. The
   captain never receives it.

5. **Receipt.** Phone: the receipt screen. Command:

   ```bash
   mz GET /v1/rider/rides/$RIDE/receipt $CALL_A
   ```

   Expect `"payment_method":"cash"`, `"payment_status":"succeeded"`, a
   `"payment"` block with `"method":"cash","status":"cash_confirmed"`,
   `"surge_reason":"peak_hours"`, `"coupon_code":"WELCOME50"` with the same
   `discount_paise` as the quote, and `total_paise` equal to the quote's
   total (plus a `waiting_charge_paise` only if the captain waited more
   than 3 minutes between Arrived and the OTP).

6. **Book a UPI ride and pay in the app.** Phone: same route, choose UPI,
   book; captain accepts, arrives, OTP, completes (step 4 again; the
   `WELCOME50` box now refuses with "first ride only"). After completion
   the customer phone shows **Pay ₹x** → tapping it opens the intent; the
   Razorpay test sheet appears. On dev do **not** finish the sheet — close
   it. Command alternative to open the intent:

   ```bash
   RIDE=<upi ride id>; mz POST /v1/rider/rides/$RIDE/payment/intent $CALL_A '{"method":"upi"}'
   ```

   Expect `200` with `"intent_id"`, `"amount_paise"` and a `client_session`
   carrying an `order_id` starting `order_`. The ride's payment is now
   **confirming**:

   ```bash
   mz GET /v1/rider/rides/$RIDE/payment $CALL_A
   ```

   Expect `"method":"upi","status":"confirming"`.

7. **Simulated capture (the payments test webhook).** The same signed
   `payment.captured` webhook the Feast seeder and the Dating pilot guide
   use: payments-service verifies the signature with its TEST webhook
   secret, settles the intent and emits `payment.succeeded`; rider marks
   the ride paid only from that event. The secret is read from the
   payments container into a shell variable and never printed (dev test
   secret only — never do this with staging or production keys).

   ```bash
   read -r ORDER AMT < <(docker exec atpost_stack-postgres-1 psql -U postgres -d app -tAc "select provider_reference||' '||amount_paise from rider_ride_payments where ride_id='$RIDE'")
   BODY="{\"entity\":\"event\",\"account_id\":\"acc_devseed\",\"event\":\"payment.captured\",\"contains\":[\"payment\"],\"payload\":{\"payment\":{\"entity\":{\"id\":\"pay_devseed$(openssl rand -hex 4 | cut -c1-7)\",\"entity\":\"payment\",\"amount\":$AMT,\"currency\":\"INR\",\"status\":\"captured\",\"order_id\":\"$ORDER\",\"method\":\"upi\",\"captured\":true}}},\"created_at\":$(date +%s)}"
   SIG=$(docker exec atpost_stack-payments-service-1 printenv RAZORPAY_WEBHOOK_SECRET | tr -d '\r\n' | { read -r s; printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$s" | sed 's/.*= //'; })
   curl -s -o /dev/null -w 'webhook: %{http_code}\n' -X POST http://localhost:8102/v1/payments/webhook -H 'Content-Type: application/json' -H "X-Razorpay-Signature: $SIG" -H "X-Razorpay-Event-Id: evt_devseed_$(openssl rand -hex 6)" --data-binary "$BODY"; unset SIG
   ```

   Expect `webhook: 200`. Within about 10 seconds:

   ```bash
   mz GET /v1/rider/rides/$RIDE/payment $CALL_A
   ```

   Expect `"status":"paid"`. The customer phone's payment screen flips to
   paid on its own (realtime frame), and the captain phone gets
   **"Payment received ₹x"**. `ORDER` empty means the intent was never
   opened (step 6); `webhook: 400` means the signature did not match — run
   the block again in one go (the body and the signature must be from the
   same shell).

8. **Cancellation after the free window → outstanding fee → next
   estimate.** Phone (call_a): book a cash ride; captain accepts; wait
   **more than 2 minutes** after the acceptance (the seeded auto rule has a
   120 s free window), then cancel from the customer phone. The customer
   phone shows the Rs 15 cancellation fee. Commands:

   ```bash
   mz POST /v1/rider/rides/$RIDE/cancel $CALL_A '{"reason":"weekend test","expected_revision":<rev>}'
   mz GET /v1/rider/me/outstanding $CALL_A
   ```

   Expect the cancel to answer `200` with `"cancellation_fee_paise":1500`,
   and the outstanding list to hold one row: `"amount_paise":1500,
   "reason":"cancellation_fee","status":"pending"`. (Cancel within the 2
   minutes instead and the fee is 0 with no row — also correct.) Now
   estimate again (step 1's command): expect `"outstanding_paise":1500` and
   a `previous_cancellation_fee` line in the breakdown; the phone shows
   "Previous cancellation fee ₹15" on the fare card. Book and complete that
   ride and the row becomes `settled`; or pay it directly from the
   customer's outstanding screen (an intent like step 6, settled by step 7).

9. **The captain-reported distance never changes the fare.** Book a cash
   ride, accept, arrive, start with the OTP (step 4), then complete it
   **by command** with an absurd distance:

   ```bash
   mz POST /v1/rider/rides/$RIDE/complete $CALL_B "{\"final_distance_km\":999,\"final_duration_min\":5,\"idempotency_key\":\"$(newid)\",\"expected_revision\":<rev>}"
   mz GET /v1/rider/rides/$RIDE/receipt $CALL_A
   ```

   Expect the receipt's `total_paise` to equal the quote's total (plus any
   waiting charge), `"reported_distance_km":999` shown as telemetry only,
   and `tracked_distance_m` from the captain phone's own GPS fixes. Only the
   server-tracked route can re-price a ride, and only when it exceeds the
   quoted distance by 20% and 1 km, capped at twice the quoted fare.

10. **Admin console.** `http://localhost:3002/admin/login` (the admin site
    from the admin-console guide, `bun run dev` running), sign in with your
    admin account and the authenticator code.
    - **Mopedu → Pricing**: the fare windows list shows "Weekend test peak"
      (Sat–Sun, 09:00–21:00, x1.15). Edit it — change the multiplier to
      x1.20 — with a reason; the console asks for a fresh code (step-up).
      Run step 1's estimate again: `"surge_bps":2000`. Put it back to x1.15.
    - **Mopedu → Coupons**: `WELCOME50` is listed with its redemptions
      (one `applied` from the cash ride). Deactivate it with a reason. Step
      2's estimate now answers `422 COUPON_INVALID` ("coupon is not active").
    - **Mopedu → Money**: the ride payments list shows the UPI ride as
      `paid`. Choose **Refund** on it, full amount, with a reason. Expect the
      step-up prompt, then the action goes through as **founder-alone
      approval** (you are the only `rider:payments.settle` holder; the audit
      row says `sole_holder`). Command alternative:

      ```bash
      mz POST /v1/rider/admin/rides/$RIDE/refund $ADMIN '{"reason":"weekend test refund"}' admin
      ```

      Expect `202 Accepted` with `"status":"accepted"`. Then the receipt:

      ```bash
      mz GET /v1/rider/rides/$RIDE/receipt $CALL_A
      ```

      Expect a `refunds` entry with the amount and reason. Its status is
      `accepted`, and the payment stays `paid`, **because the capture in
      step 7 was simulated**: Razorpay has no such payment, so payments
      parks the refund as `needs_attention` (resolve it with
      `docs/runbooks/payments-refund-needs-attention.md`). Pay a ride for
      real from the phone (Razorpay test sheet, test UPI id) and the same
      refund ends as `"status":"refunded"` with the payment `refunded` and
      `refunded_paise` equal to the amount — that is the path to expect in
      production.

11. **Captain subscription paid in the app (UPI).** call_b's seeded
    subscription is the free trial. Captain phone: Subscription → choose
    **Basic ₹199** → UPI → the Razorpay test sheet opens (close it on dev).
    Command alternative:

    ```bash
    mz POST /v1/rider/subscriptions/checkout $CALL_B '{"plan_code":"basic_199","method":"upi"}'
    mz GET /v1/rider/subscriptions/me/payment $CALL_B
    ```

    Expect `200` with `"status":"pending_payment"`, a `subscription_id`, an
    `intent_id`, `"amount_paise":19900` and a `client_session` with an
    `order_id` starting `order_`; the payment status answers
    `"status":"confirming"`. Nothing is active yet — `GET
    /v1/rider/subscriptions/me` still shows the trial. Now the **same
    payments test step as the ride payment (step 7)**, reading the order and
    amount from the subscription row instead:

    ```bash
    read -r ORDER AMT < <(docker exec atpost_stack-postgres-1 psql -U postgres -d app -tAc "select provider_reference||' '||amount_paise from rider_partner_subscriptions where partner_id=(select id from rider_partners where user_id='$CALL_B') and payment_status='confirming' order by created_at desc limit 1")
    ```

    then the `BODY=` / `SIG=` / `curl` lines of step 7 unchanged. Expect
    `webhook: 200`, and within about 10 seconds
    `mz GET /v1/rider/subscriptions/me/payment $CALL_B` answers
    `"status":"paid"` with an `expires_at`, and
    `mz GET /v1/rider/subscriptions/me $CALL_B` shows `basic_199`
    `"status":"active"` — activated by the signed `payment.succeeded` event
    alone. Because a trial was running, the new period **starts where the
    trial ends** (`starts_at` = the trial's `expires_at`), never today; the
    trial row is marked `cancelled` with reason `superseded_by_renewal`.
    The captain phone gets the "subscription activated" push. A second
    checkout of the same plan while it is confirming re-opens the same
    intent (idempotent). The old proof route answers `410
    SUBSCRIPTION_PROOF_GONE`; the console's "verify payment" refuses any
    subscription that has an intent.

12. **Captain onboarding approves itself.** Sign a fresh captain up in the
    Mopedu Captain app (a third test account, or `--reset` call_b first):
    create the profile → "Verify with DigiLocker" (the mock accepts any
    code) → add the vehicle → take the selfie. Watch the onboarding screen:
    Aadhaar and licence are verified the moment the DigiLocker callback
    returns, the vehicle the moment it is added (RC from DigiLocker), the
    selfie a second after upload (face compare), and the status flips to
    **Approved** with no admin action. Command alternative for the state:

    ```bash
    mz GET /v1/rider/partners/me/onboarding $CALL_B
    ```

    Expect `"status":"approved"` with empty `pending` and `missing`. To see
    the one human fallback: upload a licence photo by hand instead
    (`POST /v1/rider/partners/me/documents` with `document_type
    driving_license` and no DigiLocker) — the status reads `under_review`
    with `"pending":["driving_license"]`, the captain phone gets the "under
    review" notice once, the document appears in **Mopedu → Partners →
    Documents** in the console, and verifying it there approves the partner
    immediately (no separate approve click).

13. **Automatic refund: the captain cancels a paid ride.** Book a UPI ride
    as call_a, accept as call_b, pay it (steps 6–7: on dev the capture is
    simulated, which is enough for the rule), then cancel it **from the
    captain phone** (Cancel → any reason). Command alternative:

    ```bash
    mz POST /v1/rider/rides/$RIDE/cancel $CALL_B '{"reason":"vehicle breakdown","expected_revision":<rev>}'
    mz GET /v1/rider/rides/$RIDE/receipt $CALL_A
    ```

    Expect the receipt's `refunds` to hold one entry for the full amount
    with `"reason":"captain_cancel"` and `"status":"accepted"` — filed by
    rule the moment the captain cancelled, requested by the system actor,
    with no admin approval. (As in step 10, a simulated capture makes
    Razorpay refuse the money movement, so it stays `accepted`; a real test
    payment ends `refunded`.) A customer's own cancellation, or a cash ride,
    never files one. The SQL check is 9.8.

14. **Pushes — which lands where.**

    | Moment | call_a phone (Momentum) | call_b phone (Mopedu Captain) |
    |---|---|---|
    | ride booked, offer sent | — | **New ride offer** (high priority, channel `captain_offer`) |
    | captain accepts | Captain assigned | — |
    | captain arriving / arrived | Captain arriving / Captain has arrived (OTP) | — |
    | OTP verified, ride starts | Ride started | — |
    | completed | Ride completed (tap for receipt) | — |
    | customer cancels | Ride cancelled | Ride cancelled |
    | captain cancels | Ride cancelled — book again | — |
    | UPI payment settles (step 7) | payment paid | **Payment received ₹x** (captain earnings channel) |
    | partner approved (automatic, step 12) | — | welcome / approved push (only if the app was already registered; the seed approves before that, so expect none) |
    | a hand-uploaded document waits for an admin (step 12) | — | **Under review** notice, once per change of the pending set |
    | subscription paid (step 11) | — | **Subscription activated** (the same event the trial sends) |
    | captain cancels a paid ride (step 13) | Ride cancelled — refund on its way | — |

    A push that does not arrive while the app is in the foreground is
    normal on Android (the in-app screen updates instead); background the
    app to see the notification.

## 9. SQL checks for each expected state

Each block prints one line per row. Replace `<ride id>` where shown.

1. **Fare windows for Bengaluru** (after step 4; after the console edit the
   multiplier line reads `12000`):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select f.name, f.days_of_week, f.start_minute, f.end_minute, f.multiplier_bps, coalesce(f.vehicle_type::text,'all') as vt, f.is_active
   from rider_fare_windows f join rider_cities c on c.id = f.city_id where c.name = 'Bengaluru' order by f.priority desc, f.name;
   SQL
   ```

   Expect `Weekend test peak|96|540|1260|11500|all|t` first, then
   `Evening peak|31|1050|1230|12500|all|t`, `Morning peak|31|480|630|12500|all|t`,
   `Night|127|1380|300|12000|auto|t`, `Night|127|1380|300|12000|bike|t`.

2. **Coupon and its redemptions** (after the cash ride: one `applied`;
   after a cancelled ride that carried it: `released`; after the console
   deactivation `is_active` is `f`):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select code, discount_type, percent_bps, max_discount_paise, first_ride_only, per_user_limit, used_count, is_active from rider_coupons where code = 'WELCOME50';
   select r.status, r.discount_paise, r.ride_id, r.created_at from rider_coupon_redemptions r join rider_coupons c on c.id = r.coupon_id where c.code = 'WELCOME50' order by r.created_at desc limit 5;
   SQL
   ```

   Expect `WELCOME50|percent|5000|5000|t|1|1|t` and one
   `applied|<1..5000>|<ride id>|<time>` row.

3. **Outstanding rows** (after step 8: `pending`; after the next completed
   ride: `settled` with `settled_by_ride_id` set; after a console waive:
   `waived`):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select amount_paise, reason, status, ride_id, settled_by_ride_id, created_at from rider_customer_outstanding where customer_user_id = '2d598287-eee7-40b4-a7f5-b46b9412e4e7' order by created_at desc limit 5;
   SQL
   ```

   Expect `1500|cancellation_fee|pending|<cancelled ride id>||<time>`.

4. **Payment rows** (cash ride: `cash|succeeded`; UPI ride before step 7:
   `upi|confirming` with an `intent_id` and an `order_…` reference; after
   step 7: `upi|succeeded`; after a real refund: `refunded` with
   `refunded_paise` equal to the amount):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select ride_id, payment_method, status, amount_paise, refunded_paise, intent_id, provider_reference, settled_at from rider_ride_payments order by created_at desc limit 5;
   SQL
   ```

   And the signed events rider applied (one `payment.succeeded` per paid
   ride; a real refund adds `payment.refunded`):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select event_type, reference_id, amount_minor, outcome, applied_at from rider_payment_inbox order by applied_at desc limit 5;
   SQL
   ```

   Expect `payment.succeeded|<upi ride id>|<amount>|succeeded|<time>`.

5. **Refund rows** (after step 10: `accepted` for a simulated capture,
   `refunded` for a real one; `failed` means payments refused it outright):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select ride_id, amount_paise, status, reason, requested_by, provider_reference, created_at from rider_ride_refunds order by created_at desc limit 5;
   select action, admin_user_id, entity_id, response_status, created_at from rider_admin_audit_logs where action in ('ride_payment.refund','fare_window.update','coupon.deactivate') order by created_at desc limit 6;
   SQL
   ```

   Expect one refund row with `requested_by` = your admin id, and audit rows
   for the refund, the window edit and the coupon deactivation with
   `response_status` 202 / 200 / 200.

6. **Track points per ride** (the captain phone's fixes while `arrived` →
   `in_progress`, at most one per 5 s; this is the only distance that can
   re-price a ride):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select r.id, r.status, r.tracked_distance_m, r.final_distance_km as reported_km, r.final_fare_paise, (select count(*) from rider_ride_track_points t where t.ride_id = r.id) as track_points
   from rider_rides r where r.customer_user_id = '2d598287-eee7-40b4-a7f5-b46b9412e4e7' order by r.created_at desc limit 5;
   SQL
   ```

   Expect, for the step-9 ride, `reported_km` = `999.00` while
   `final_fare_paise` equals the quote's total; `track_points` is 0 when the
   ride was completed by command without the captain phone moving (then
   `tracked_distance_m` is empty and the quote stands), and grows with a
   phone-driven ride.

7. **Ride status history** for any ride (every transition with its actor):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select from_status, to_status, actor_kind, reason, created_at from rider_ride_status_history where ride_id = '<ride id>' order by created_at;
   SQL
   ```

   Expect `requested → searching_partner → partner_assigned →
   partner_arriving → arrived → otp_verified → in_progress → completed`,
   then a `completed → completed` row "cash collected and confirmed" for a
   cash ride.

8. **Automatic onboarding, subscription checkout and rule refunds** (launch
   safety). Documents and vehicles with who verified them — every seeded
   row reads `digilocker|auto` (or `upload|auto` for the selfie); a
   hand-uploaded document reads `upload|` with status `pending` until an
   admin id appears:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select p.full_name, d.document_type, d.status, d.source, coalesce(d.verified_by_actor,''), coalesce(d.auto_check_detail,''), d.verified_at
   from rider_partner_documents d join rider_partners p on p.id = d.partner_id order by d.created_at desc limit 12;
   select v.registration_number, v.status, coalesce(v.verified_by_actor,''), coalesce(rc.source,''), coalesce(rc.status::text,'') as rc_status
   from rider_vehicles v left join rider_vehicle_documents rc on rc.vehicle_id = v.id and rc.document_type = 'vehicle_rc' order by v.created_at desc limit 6;
   select action, entity_type, new_value->>'reason' as reason, created_at from rider_admin_audit_logs
   where admin_user_id = uuid_generate_v5('6ba7b811-9dad-11d1-80b4-00c04fd430c8'::uuid, 'https://momentum.app/actor/mopedu-system') order by created_at desc limit 12;
   SQL
   ```

   Expect `aadhaar|approved|digilocker|auto`, `driving_license|approved|digilocker|auto`
   and `profile_photo|approved|upload|auto|face_compare similarity 96.0 ...`
   per captain, every vehicle `approved|auto|digilocker|approved`, and audit
   rows `document.auto_verify` (reason `digilocker` / `face_compare`),
   `vehicle.auto_verify` and `partner.auto_approve` under the system actor
   (if `uuid_generate_v5` is missing, filter on `new_value->>'actor' =
   'system'` instead). Subscriptions after step 11:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select pl.code, s.status, coalesce(s.payment_status,'legacy'), s.amount_paise, s.intent_id, s.starts_at, s.expires_at, s.renews_subscription_id, coalesce(s.cancellation_reason,'')
   from rider_partner_subscriptions s join rider_subscription_plans pl on pl.id = s.plan_id
   where s.partner_id = (select id from rider_partners where user_id = '66668bc2-a3f6-40a5-9cdd-c998dcf72f29') order by s.created_at;
   select event_type, reference_id, amount_minor, outcome, applied_at from rider_payment_inbox where outcome like 'subscription%' order by applied_at desc limit 5;
   SQL
   ```

   Expect the trial row `trial_7d|cancelled|legacy|0|...|superseded_by_renewal:<id>`
   and `basic_199|active|paid|19900|<intent>|<trial expires_at>|<+30 days>|<trial id>|`,
   plus one inbox row `payment.succeeded|<subscription id>|19900|subscription_activated`.
   Before the webhook the row reads `pending_payment|confirming`; a
   wrong-amount capture leaves it there and adds a
   `rider_payment_reconciliation` row with `subscription_id` set. Rule
   refunds after step 13 (and any duplicate capture):

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select ride_id, rule_code, amount_paise, status, requested_by, payment_id, outstanding_id, intent_id, created_at
   from rider_ride_refunds where rule_code <> 'discretionary' order by created_at desc limit 5;
   select ride_id, canonical_status, terminal_reason from rider_payment_reconciliation where canonical_status = 'duplicate_capture' order by created_at desc limit 3;
   SQL
   ```

   Expect `<ride>|captain_cancel|<full amount>|accepted|<system actor uuid>|<payment id>||<intent>`
   with `requested_by` the same system actor as the audit rows (never an
   admin id); a `duplicate_capture` row appears only if payments-service
   ever captured the same ride twice.

## 10. What is not in this build — not bugs

- **No live map.** `maps-compose` is not in the offline Gradle cache, so
  both apps show pickup/drop as text and the captain's progress as status
  steps, not on a map.
- **No Google routing.** `GOOGLE_MAPS_SERVER_KEY` is empty on dev, so
  distances and durations are straight-line (haversine) estimates; fares are
  therefore a little lower than road distance would give.
- **Coupons are on in dev values only.** Staging and production keep
  `MOPEDU_COUPONS_ENABLED=false` until the adviser confirms the GST
  treatment of a discount (same question as Feast's coupons).
- **Production needs two secrets before rider-service will start**:
  `RIDER_PII_KEYS` (never the dev key) and the real e-commerce-operator
  `MOPEDU_PLATFORM_GSTIN` (dev runs on a synthetic, checksum-valid GSTIN).
- **DigiLocker is a mock** (`DIGILOCKER_MODE=mock`); the seed sets KYC
  approved through the admin approval, and no Aadhaar assertion is real.
- **Refunds of simulated captures park** in payments as `needs_attention`
  (step 10); only a real Razorpay test payment refunds end to end.
- **The synthetic captains never accept.** They exist so an offer is always
  sent; call_b is the only captain who can complete a ride.
- **The captain must be online near MG Road.** Dispatch searches 5 → 10 →
  20 km around the pickup; a captain phone left at home elsewhere in the
  city may be outside the first batches.

## 11. If something looks broken

Logs for the last few minutes (swap the service name):

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 5m rider-service | tail -40
```

The offer never reaches the captain: `rider-service` (dispatch) and
`notification-service` logs. A UPI payment stays `confirming` after step 7:
`payments-service` then `rider-service` logs. Console pages empty:
`api-gateway admin-service` logs, and the Git Bash window running
`bun run dev`.

Tell me what you saw and what you expected; I'll take it from there.
