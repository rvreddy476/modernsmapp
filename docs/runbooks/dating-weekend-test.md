# Dating — weekend test guide

Everything below runs on this PC. Nothing here is started for you in advance:
the code is committed and pushed, and the dev containers still run the images
built before the last few commits, so step 2 is the one that matters.

Pilot accounts: **call_a** and **call_b** (both already allow-listed). Test
data is already seeded: 22 profiles, 5 matches with chats, 1 report, 1 panic.

---

## 1. Check the stack is up

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' | head -40
```

If containers are missing, start the whole stack:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d
```

## 2. Rebuild the services whose code changed since they were last built

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d --build dating-service dating-data-exporter notification-service graph-service chat-message-service call-service
```

Wait about 30 seconds, then confirm they are healthy:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' dating-service dating-data-exporter notification-service graph-service chat-message-service call-service && docker compose exec -T dating-service wget -qO- http://localhost:8112/healthz
```

Expect six `Up` lines and `{"status":"alive"}`.

## 3. Check the seeded data is still there

```bash
docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
select (select count(*) from dating_profiles where profile_status = 'active') as active_profiles,
       (select count(*) from dating_matches where conversation_id is not null) as matches_with_chat,
       (select count(*) from dating_reports) as reports;
SQL
```

Expect `22|5|1` — active profiles, matches with a chat, reports. To reset and reseed:

```bash
bash /c/workspace/modernsmapp/Architecture/services/dating-service/scripts/dev-seed-dating.sh --reset && bash /c/workspace/modernsmapp/Architecture/services/dating-service/scripts/dev-seed-dating.sh
```

## 4. Find this PC's Wi-Fi address

It changes, and a stale address makes the app look "offline" when the backend
is fine.

```bash
powershell -NoProfile -Command "Get-NetIPAddress -AddressFamily IPv4 | Where-Object { $_.IPAddress -notlike '127.*' -and $_.IPAddress -notlike '169.254.*' } | Select-Object IPAddress, InterfaceAlias"
```

Take the Wi-Fi one. Your phone must be on the same Wi-Fi.

## 5. Build the app

```bash
cd /c/workspace/modernsmapp/mobile/android && ./gradlew.bat --offline :app:assembleDevDebug -PdevHost=<ip from step 4>
```

Confirm the address landed in the build:

```bash
grep API_BASE_URL /c/workspace/modernsmapp/mobile/android/app/build/generated/source/buildConfig/dev/debug/com/us/android/BuildConfig.java
```

The APK is at
`mobile/android/app/build/outputs/apk/dev/debug/app-dev-debug.apk`. Install it
the way you normally do (Android Studio Run, or `adb install -r <path>`).

## 6. Walkthrough, in this order

Sign in as **call_a**, then open the **Match** tile.

1. **Onboarding** — call_a already has a profile, so you land on the deck. To
   see onboarding, use a third account, or reset with step 3 and skip the
   pilot accounts.
2. **Pulse deck** — the photo shows openly (no blur), and the card scrolls:
   swipe the gallery, read the description and the prompt answers, then the
   languages. Distance is a range such as "< 5 km"; there are no exact
   distances anywhere.
3. **Spark** someone, **pass** on someone.
4. **Sparks tab** — incoming sparks carry the same detail. Tap one to open the
   person view. "Spark back" forms a match.
5. **Matches** — open a match, then open the chat. The message should send and
   arrive. Then try the **call** button in the chat header: a match may call
   without being a friend in the main app. Block or unmatch during a call and
   it should end for both sides.
6. **Report** a test profile: fixed reasons, `other` needs a description.
   After reporting, that person disappears from every list.
7. **Panic button**, then check it was recorded:

   ```bash
   docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
   select status, created_at from dating_panic_incidents order by created_at desc limit 3;
SQL
   ```

8. **Trusted contacts and location share** — add a match as a contact, share
   your location for 15 minutes, then stop it.
9. **Selfie** — the blink-twice recording. The preview should stay live, the
   video should be upright, and two clear blinks should pass.
10. **Premium** — open the plans. Purchases go through the payments service;
    on dev the payment sheet will not complete, so a "Premium isn't available
    yet" or a stuck "confirming" is expected, not a bug.
11. **Privacy** — request a data export, then check it completed:

    ```bash
    docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -tAf -' <<'SQL'
    select status, requested_at, completed_at from dating_data_exports order by requested_at desc limit 3;
SQL
    ```

## 7. Known limits on dev — not bugs

- **Calls between matches are on in dev only.** They are switched off in
  staging and production until you say otherwise (`DATING_CALLS_ENABLED`).
  Someone who has set "no calls" still cannot be called, even by a match.
- **Test profiles have only one photo each**, so the gallery swipe is best
  seen on a profile you build yourself.
- **Test profiles have no chat name or photo.** The 20 seeded profiles are not
  real accounts, so a chat with one may show a blank name. call_a and call_b
  look normal with each other.
- **Panic alerts reach no staff.** No responder ids are configured yet; a panic
  writes an incident, notifies trusted contacts, and raises an internal alert.
- **Admin screens don't exist.** Moderation is done with the commands in
  `docs/runbooks/dating-internal-pilot.md`; admin routes also return 403
  through the gateway until an admin role is granted.
- **Real payment completion is off.** Premium is wired end to end, but dev has
  no live payment gateway.
- **The deck shows at most 7 cards a day**, so distant seeded profiles may not
  appear.

## 8. If something looks broken

Logs for the last few minutes:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose logs --since 5m dating-service | tail -40
```

Tell me what you saw and what you expected; I'll take it from there.
