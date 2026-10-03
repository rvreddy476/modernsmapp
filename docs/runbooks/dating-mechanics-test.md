# Dating (Pulse) — testing the new mechanics

The ten mechanics added on 2 October 2026: refilling swipe deck, rewind, Super
Spark, who liked you, first move, filters, daily picks, travel, read receipts
and calls, and the allowances read. Every one sits behind its own flag in
dating-service.

Run every command in **Git Bash** from `C:\workspace\modernsmapp` unless a step
says otherwise. Pilot accounts are **call_a** and **call_b** (see
`dating-internal-pilot.md`; the gateway stays closed to everyone else).

---

## 0. Before you start: what has to be true

1. The code is on branch `claude/determined-herschel-21fb46` (backend, chat and
   Android) and on `chore/production-hardening` in `C:\workspace\postbook-ui`
   (web). The dev stack builds from the main checkout, so the backend branch
   must be merged there first.
2. **chat-service must be deployed before dating-service.** First move, read
   receipts and the call rule are enforced in chat; an older chat ignores the
   new fields and those three simply do not apply.
3. **The flags are off on the dev stack today.** They default on only when
   `ENV` is `local` or `dev`, and the dev compose leaves `ENV` unset for
   dating-service on purpose (it changes other boot behaviour). Each flag must
   be listed in dating-service's `environment:` block in
   `Architecture/docker/docker-compose.yml`:

   ```yaml
      DATING_DECK_REFILL_ENABLED: ${DATING_DECK_REFILL_ENABLED:-true}
      DATING_REWIND_ENABLED: ${DATING_REWIND_ENABLED:-true}
      DATING_SUPER_SPARK_ENABLED: ${DATING_SUPER_SPARK_ENABLED:-true}
      DATING_LIKED_YOU_GATE_ENABLED: ${DATING_LIKED_YOU_GATE_ENABLED:-true}
      DATING_FIRST_MOVE_ENABLED: ${DATING_FIRST_MOVE_ENABLED:-true}
      DATING_FILTERS_V2_ENABLED: ${DATING_FILTERS_V2_ENABLED:-true}
      DATING_PICKS_ENABLED: ${DATING_PICKS_ENABLED:-true}
      DATING_TRAVEL_ENABLED: ${DATING_TRAVEL_ENABLED:-true}
      DATING_READ_RECEIPTS_ENABLED: ${DATING_READ_RECEIPTS_ENABLED:-true}
      DATING_CALL_AFTER_EXCHANGE_ENABLED: ${DATING_CALL_AFTER_EXCHANGE_ENABLED:-true}
      # Batch 2 (3 Oct 2026)
      DATING_PICKS_MUTUAL_ENABLED: ${DATING_PICKS_MUTUAL_ENABLED:-true}
      DATING_FAIR_TURN_ENABLED: ${DATING_FAIR_TURN_ENABLED:-true}
      DATING_DEALBREAKERS_ENABLED: ${DATING_DEALBREAKERS_ENABLED:-true}
      DATING_KIND_CHECK_ENABLED: ${DATING_KIND_CHECK_ENABLED:-true}
      DATING_DATE_CHECKIN_ENABLED: ${DATING_DATE_CHECKIN_ENABLED:-true}
      DATING_MEDIA_PROMPTS_ENABLED: ${DATING_MEDIA_PROMPTS_ENABLED:-true}
      DATING_HIDE_KNOWN_ENABLED: ${DATING_HIDE_KNOWN_ENABLED:-true}
      DATING_SCAM_ALERT_ENABLED: ${DATING_SCAM_ALERT_ENABLED:-true}
      DATING_SCREEN_PROTECTION_ENABLED: ${DATING_SCREEN_PROTECTION_ENABLED:-true}
      DATING_PAST_MATCH_REPORT_ENABLED: ${DATING_PAST_MATCH_REPORT_ENABLED:-true}
   ```

   Voice/video prompt answers also need media-service's own flag, in its
   `environment:` block: `MEDIA_DATING_CLIPS_ENABLED: ${MEDIA_DATING_CLIPS_ENABLED:-true}`.
   Hide-from-people-I-know needs `GRAPH_SERVICE_URL` on dating-service (the
   dev compose already sets it).

   Limits, all optional: `DATING_DECK_DAILY_LIMIT_FREE` (25),
   `DATING_DECK_DAILY_LIMIT_PASS` (100), `DATING_REWIND_DAILY_LIMIT_FREE` (1),
   `DATING_SUPER_SPARK_DAILY_LIMIT_FREE` (1), `DATING_SUPER_SPARK_DAILY_LIMIT_PASS` (5),
   `DATING_PICKS_EXPOSURE_CAP` (30), `DATING_FAIR_TURN_LIMIT` (6).
   A malformed value stops dating-service from starting, by design.
4. **Deploy order for batch 2:** chat-service, notification-service and
   media-service before dating-service (fair turn asks chat; scam alerts and
   check-ins are rendered by notification-service; clips live in
   media-service).

## 1. Rebuild the two services

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d --build chat-message-service dating-service
```

Wait about 30 seconds, then check both are healthy and the flags were read:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose ps --format '{{.Name}} {{.Status}}' chat-message-service dating-service && docker compose logs dating-service 2>&1 | grep "pulse mechanics configured" | tail -1
```

Expect two `Up` lines and a log line listing every flag as `true`.

## 2. Check what each account sees as switched on

Use the `dzu` helper from `dating-internal-pilot.md` section 0.2.

```bash
dzu 2d598287-eee7-40b4-a7f5-b46b9412e4e7 GET /v1/dating/allowances
```

Expect `sparks`, `deck`, `rewind` and `super_spark`. A mechanic that is off is
missing from this answer, and the apps hide its controls.

## 3. Walkthrough on the phone (call_a)

1. **Deck** — swipe right to spark, left to pass. The deck keeps refilling;
   after 25 cards it shows "out of cards" with the reset time. A person you
   sparked or passed never comes back.
2. **Undo** — pass someone, then tap Undo: the card returns. A second undo the
   same day shows "no undos left" with the reset time. A spark can never be
   undone.
3. **Super Spark** — swipe up (or the star button). The second one today shows
   "out of Super Sparks". On call_b, the Super Spark is first in the list and
   marked.
4. **Liked you** — on call_b without a pass: a count and blurred tiles, no
   names; tapping offers Premium. Spark back is refused.
5. **First move** — in Settings, turn on "I'll send the first message" and add
   a question. Match with call_b: call_b sees your question and cannot open the
   chat to write first, but can answer the question or give you 24 more hours
   once a day. You have 24 hours to write.
6. **Filters** — the sliders button on the deck. Age, distance and intent are
   free; the rest are locked without a pass.
7. **Picks** — the Picks tab: up to 10 profiles, new at local midnight.
   Sparking from Picks does not use a deck card.
8. **Travel** — needs a pass. Pick a city: the deck becomes that city, and
   people there see you as "Visiting <city>" — never your home city.
9. **Read receipts and calls** — needs a pass for receipts (Settings). The
   match screen shows call buttons only after you have both sent a message.

## 3b. Batch 2 walkthrough (call_a and call_b)

1. **Mutual picks** — set call_b's age range so call_a falls outside it
   (Filters on call_b). Tomorrow's picks for call_a no longer include call_b.
   (Picks are made once a day, so this shows the next local day.)
2. **Dealbreakers** — on call_b, mark the age preference as a dealbreaker.
   call_a, outside that range, no longer sees call_b in the deck either.
3. **Fair turn** — with six open matches where the other person wrote last,
   call_a's deck shows "matches are waiting for your reply" and new sparks
   are refused. Sparking back someone who sparked you still works.
4. **Kind-message check** — in a dating chat, type "you are an idiot" and
   send: the app asks whether to send anyway. On the other phone the message
   arrives blurred with "Did this bother you?".
5. **Comment filter** — call_b sparks call_a with the note "you look
   stupid": in call_a's Liked you / sparks the comment is tucked away. Add a
   hidden word in Settings → Comment filter and spark again using it.
6. **After-date check-in** — plan a safe meet between call_a and call_b from
   the match screen, then (dev only) move it 4 hours into the past and wait
   up to a minute for the sweeper:

   ```bash
   docker exec atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -c "UPDATE dating_meets SET scheduled_at = now() - interval '"'"'4 hours'"'"' WHERE date_checkin_asked_at IS NULL AND user_id = '"'"'2d598287-eee7-40b4-a7f5-b46b9412e4e7'"'"'"'
   ```

   Both phones get "How did it go?". Answer "No" to "did you feel safe" and
   the app offers to report.
7. **Past matches** — unmatch call_b from call_a, then open Safety: "Report
   someone from a past match" lists call_b.
8. **Hide from people I know** — connect call_a and call_b on Momentum
   (accepted connection), then turn on Settings → Hide me from people I know
   on call_a. Neither sees the other in the deck any more.
9. **Scam alert** — needs an admin: report call_b as "scam" from a third
   account that matched call_b, then in the admin console suspend call_b on
   that report. call_a gets the "Safety notice" push naming call_b.
10. **Screen protection (Android)** — screenshots of the deck and profiles
    come out black; screenshots of a chat still work.
11. **Voice/video answers** — on a prompt, record a short voice or video
    answer (30 s max). A voice clip waits for a moderator; there is no
    console page for clips yet, so a moderator uses the API
    (`GET /v1/dating/admin/clips/pending`, then `POST /v1/dating/admin/clips/review`
    with `{user_id, prompt_id, decision: "approved"}`). Once approved it plays
    on the card for the other phone.

## 4. Giving an account a pass on dev without paying

The pilot runbook's section 7 covers a real test purchase. For a quick check
only, on dev only:

```bash
docker exec -i atpost_stack-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d app -v ON_ERROR_STOP=1' <<'SQL'
INSERT INTO dating_premium_subscriptions (user_id, plan, started_at, expires_at, source)
VALUES ('2d598287-eee7-40b4-a7f5-b46b9412e4e7', 'pass_30d', now(), now() + interval '7 days', 'dev-test')
ON CONFLICT (user_id) DO UPDATE SET expires_at = EXCLUDED.expires_at;
SQL
```

Remove it again by setting `expires_at = now()` the same way.

## 5. Turning a mechanic off

Set its flag to `false` in `Architecture/docker/.env` (for example
`DATING_TRAVEL_ENABLED=false`) and recreate dating-service:

```bash
cd /c/workspace/modernsmapp/Architecture/docker && docker compose up -d --no-deps dating-service
```

A plain `restart` does not re-read `.env`.

## 6. Known limits

- Nothing here was run on a phone or in a browser before this guide was
  written; every check above is a first look.
- The data export still lists who sparked you, so it shows what "Liked you"
  hides from a free user once a week. That is the user's own data; whether
  that is acceptable is a founder decision.
- A height, once set, cannot be cleared (only changed).
- Staging and production: every flag defaults to off; nothing changes there
  until a flag is set to `true` in the deploy values.
