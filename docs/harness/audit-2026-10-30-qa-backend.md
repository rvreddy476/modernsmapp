# Audit: Q&A backend (qa-service, its gateway edge, notifications, media and the web client contract)

Branch `codex/module-01-02-launch-safety`. Read-only audit: no edits except this file, no git writes, no databases, no deploys.
Paths are relative to `Architecture/services/qa-service/` unless they start with `Architecture/`, `deploy/` or `postbook-ui/` (the read-only web clone at `/home/user/rvreddy476/postbook-ui`).

Covered: every route in `internal/http/handler.go:91-216` (questions, topics, answers, best answer, comments, votes, follows, saves, answer requests, profile, reputation, leaderboard, feeds, reports, search, drafts, community Q&A, legacy `/v1/qa/admin/*`), the token admin family (`internal/http/admin_token.go`, `internal/store/admin_moderation.go`), the service layer (`internal/service/qa.go`, `community_qa.go`, `media_claims.go`), the store, the boot schema (`database/setup.sql` via `internal/store/postgres/bootstrap.go`), the platform-events consumer (`internal/events/consumer.go`, `community_sync.go`), the producer (`internal/events/producer.go`), notification-service's Q&A consumer (`Architecture/services/notification-service/internal/events/qa_consumer.go`), `deploy/services/qa-service/values-*.yaml`, the gateway front (`Architecture/services/api-gateway/cmd/server/main.go:730,1382`) and `postbook-ui/src/hooks/useQA.ts` plus the pages that render its data. There is no Android client. Reputation exists; bounties, escrow and payments do not (no money path in Q&A). There is no trust-safety or search-service integration: search is in-service SQL and moderation is in-service.

Launch posture that changes some severities: the gateway keeps `/v1/qa` closed in prod, staging and QA (`QA_PUBLIC_ENABLED: "false"` and `/v1/qa` in `GATEWAY_DISABLED_PREFIXES`, `deploy/services/api-gateway/values-*.yaml`). Everything below is therefore latent at the edge today; the H items are launch blockers for flipping that flag. qa-service itself still boots in every environment (prod: 1 replica, HPA 1-4; azure-prod: 3-12), so M5 (boot DDL) is live now. The admin console path is not enabled anywhere (`SERVICE_CALLERS` commented out in every qa-service values file), and `QA_MODERATOR_USER_IDS` is set nowhere (M1).

`scripts/verify/go.sh --fast Architecture/services/qa-service`:

```
PASS build          qa-service  (3s)
SKIP vet/test       qa-service  (no changed package)
RESULT PASS  (logs: /tmp/verify-logs/20261030T213729-28952)
```

---

## HIGH

### H1. Reputation and vote notifications can be farmed without limit: every vote call awards points and pushes, even when nothing changed, and best answer pays out on every call
- **Files:**
  - `internal/service/qa.go:430-457` (`VoteQuestion`) and `:463-485` (`VoteAnswer`) call `awardReputation` (+5 / +10) and `PublishQuestionVoted`/`PublishAnswerVoted` after **every** successful store call. The store (`internal/store/votes.go:50-69,103-122`) upserts and computes the delta from the previous vote, but returns only `error`, so the service cannot tell that a repeat `up` changed nothing. `RemoveQuestionVote`/`RemoveAnswerVote` (`qa.go:459-461,487-489`) never reverse the points.
  - `internal/service/qa.go:350-374` (`SelectBestAnswer`) checks only that the caller authored the question. It does not check that `answerID` belongs to `questionID`, that it is not already best, or that the answer is not the caller's own. `internal/store/answers.go:140-158` sets `is_best` on whatever answer id it is given and increments `best_answer_count` every time. The service then awards +25 to the answer author and +2 to the selector, every call. `UnselectBestAnswer` (`answers.go:160-169`) reverses nothing.
  - notification-service turns every `qa.question.voted`/`qa.answer.voted` with `vote_type=up` into a push with no dedupe (`notification-service/internal/events/qa_consumer.go:259-302`, `CreateNotification` is the non-idempotent variant).
- **Scenario:** (a) A user answers their own question, then loops `POST /v1/qa/questions/<q>/best-answer {"answer_id":"<own answer>"}`: +27 reputation per call, straight to the top of `/v1/qa/leaderboard`. (b) A sock account loops `POST /v1/qa/answers/<victim answer>/vote {"vote_type":"up"}` 1,000 times: one vote row, `upvote_count` correct, but the author gets +10,000 reputation and 1,000 "upvoted your answer" pushes. Remove/re-add works the same way. (c) The question author marks an answer from a *different* question as best: that question now has two `is_best` answers and the stranger gets +25.
- **Fix:** Return the previous vote from the store and award/reverse reputation from the delta, inside the vote transaction (a flip up→down is −10−2, a remove reverses the last award). Publish the vote event only on a real transition. In `SelectBestAnswer`, refuse unless `answers.question_id = $question AND deleted_at IS NULL`, make it a no-op when already best, pay nothing when the answer author is the selector, and reverse the +25/+2 and `best_answer_count` on unselect or reselect. A unique `(user_id, event_type, source_id)` on `reputation_events` for one-shot events is a cheap backstop. Guard tests: repeat upvote awards once; self-accept pays nothing; foreign answer id refused.

### H2. Any signed-in user can close any question, and the author can reopen anything, undoing moderator locks, merges and the community approval queue
- **Files:**
  - `POST /questions/:questionId/close` (`internal/http/handler.go:106`, `handler_questions.go:149-172`) → `qa.go:158-166` → `store/questions.go:399-404`. No author or moderator check anywhere on the path; no status guard.
  - `ReopenQuestion` (`qa.go:171-183`, `store/questions.go:406-411`) checks only that the caller is the author, then sets `status='open'` from **any** status.
  - Moderator lock writes the same `status='closed'` (legacy `store/moderation.go:126-130`, token `store/admin_moderation.go:230`), merge writes `status='merged'` (`moderation.go:133`, `admin_moderation.go:262`), and a `require_approval` community stores new questions as `pending_approval` (`qa.go:68-70`). No approve route exists at all (the only writers of `pending_approval` are `qa.go:69` and filters).
- **Scenario:** (a) A troll walks `/v1/qa/feed/trending` and closes every question; `CreateAnswer` then refuses them all ("question is not open for answers", `qa.go:265`). The question is marked `closed_by` the troll. (b) A spammer's question is locked by a moderator; the spammer calls `/reopen` and it is open again. (c) In a community with `require_approval`, a member posts and immediately calls `/reopen` on their own `pending_approval` question; it goes live without review. Merged questions can be resurrected the same way.
- **Fix:** Close: author or moderator only, and only from `open`. Store moderator locks distinctly (a `locked_at`/`locked_by` column or a `locked` status) so the author's reopen cannot clear them. Reopen: `UPDATE ... WHERE id=$1 AND status='closed' AND closed_by = author_id AND locked_at IS NULL`. Add a community-moderator approve/reject route for `pending_approval`. Guard tests: stranger close → 403; author reopen of a locked, merged or pending question → 409.

### H3. Stored XSS: client-supplied `body_html` is stored raw and the web renders it with `dangerouslySetInnerHTML`
- **Files:**
  - Server accepts `body_html` verbatim on question create/update (`store/questions.go:20,34,81`, `:238-242`) and answer create/update (`http/handler_answers.go:22,81` → `store/answers.go:20-24,76-78`). No sanitizer exists anywhere in qa-service (no bluemonday, no escaping).
  - Web: `postbook-ui/src/app/qa/questions/[id]/page.tsx:156-159` renders `answer.body_html || answer.body` as HTML. The web also builds `body_html` itself as `` `<p>${body.trim()}</p>` `` without escaping (`src/app/qa/ask/page.tsx:86,141`, `questions/[id]/page.tsx:236`), so even the fallback path is HTML.
- **Scenario:** An attacker posts an answer with `body_html: "<img src=x onerror=\"fetch('/v1/qa/questions/…/vote',{method:'POST',…})\">"` (or a plain `body` containing the same, which the fallback renders). Every viewer of the question runs it on the postbook origin with their session: it can vote, post, follow, read their DMs through the same-origin API, or exfiltrate anything not HttpOnly. Answers on popular questions reach every reader.
- **Fix:** Stop accepting `body_html` from clients. Store `body` (markdown or plain text) and render HTML server-side through a strict allow-list (bluemonday UGC policy, `rel="nofollow ugc noopener"` on links), or sanitize any accepted HTML on write. On the web, render through DOMPurify (or text only) and never feed `body` to `dangerouslySetInnerHTML`. Add a test that `<script>`/`onerror` never survive a round trip.

### H4. Anonymous posting is undone by the public profile lists, reputation history and notifications
- **Files:**
  - `GET /profile/:userId/questions` (`handler.go:164`, `handler_profile.go:90-103`) calls `ListQuestionsByAuthor`, whose own comment says it is for the author ("leave AuthorID intact (they posted, they see)", `qa.go:200-208`). It returns the target's anonymous questions with their `author_id`, to any caller, signed in or not.
  - `GET /profile/:userId/answers` (`handler_profile.go:105-118`) calls `store.ListAnswersByAuthor` directly (`store/answers.go:200-213`): anonymous answers, unmasked, and also answers on deleted, hidden and private-community questions (no join to `questions`).
  - `GET /profile/:userId/reputation` (`handler_profile.go:61-74`) returns `source_id` for every event, including `question_asked`/`answer_posted` on anonymous items.
  - notification-service sends "X answered your question" / "X commented" / "X upvoted" with the real author as actor (`qa_consumer.go:165-184,215-233`); the producer payloads carry the real `author_id` and no anonymity flag (`events/producer.go:57-61,82-86`).
- **Scenario:** A user posts an anonymous question about a medical or workplace issue. Anyone who knows their user id (it is on every social profile and post) calls `/v1/qa/profile/<id>/questions` and sees it listed under their id with `is_anonymous: true`. Separately, the asker of a question receives a push naming the colleague who answered "anonymously".
- **Fix:** In both profile lists, drop `is_anonymous` rows unless the viewer is the target (and apply the H5 visibility predicate). Restrict reputation history to the owner, or null `source_id` for anonymous sources. Add `is_anonymous` to the `answer.created`, `comment.created` and vote payloads and have the consumer send with no actor (or skip) when set. Guard test: another viewer's profile list never contains an anonymous item.

### H5. Private-community and unapproved questions leak through search, every feed, topic lists, saves and comment reads
- **Files:** Membership is enforced only in `ListQuestions` (`store/questions.go:315-322`) and the by-id reads `GetQuestion`, `GetAnswer`, `ListAnswers`, `CreateAnswer` (`ensureQuestionVisible`, `service/community_qa.go:97-111`). Everywhere else has no private-community filter:
  - Search: `store/questions.go:547-591` (no privacy or status filter), and `community_id` is taken from the query string without `getCommunityAccess` (`http/handler_search.go:21-43`).
  - Feeds: trending, unanswered, following, for-you, home, answer-queue and local (`store/feeds.go:10-175`; the file never mentions `community`). Topic questions (`store/topics.go:110`). Saved questions/answers (`store/saves.go:30-34,60-62`). Profile lists (H4).
  - Comments: `ListComments` (`qa.go:424-426`) and `CreateComment` (`qa.go:378-390`, `store/comments.go:10-33`) do no visibility or deleted-answer check, so comments on hidden answers can still be posted and read by answer id. Votes, follows, saves and answer requests (`qa.go:430-579`) likewise act on any question or answer id.
  - Unapproved content: `ensureQuestionVisible` ignores `status`, so a `pending_approval` question is readable by id by anyone; `GET /questions?status=pending_approval` lists them publicly (`store/questions.go:308-309`, the only filter on that branch is privacy).
- **Scenario:** A logged-out caller sends `GET /v1/qa/search?q=salary&community_id=<private community id>` (community ids appear on public community cards and in event payloads) and gets the private community's question titles, slugs and authors. A non-member saves a private question id they learned once and keeps reading its title from `/saved/questions`. A member's question awaiting approval is readable and listable before any moderator has seen it.
- **Fix:** Factor one SQL predicate (not deleted; status in the viewer-visible set; `community_id IS NULL OR community_type NOT IN ('private','invite') OR viewer is member`; `pending_approval` only to author and community moderators) and apply it to every list query, including search, feeds, topics, saves and profile lists. Call `EnsureQuestionVisible` (resolving answer → question, and refusing deleted answers) on comments, votes, follows, saves and answer requests. Guard test: a private question never appears in search, trending or saves for a non-member.

---

## MEDIUM

### M1. There is no working moderation path in any environment, and the legacy path still exists alongside the token path
- **Files:** Legacy `/v1/qa/admin/*` (`handler.go:203-215`) is gated by `requireModerator` (`handler.go:57-74`): an env allowlist `QA_MODERATOR_USER_IDS` matched against the gateway-verified `X-User-ID`. It does **not** trust consumer `X-Scopes` (grep finds no `X-Scopes` read in qa-service), so the scope problem fixed elsewhere does not exist here. But `QA_MODERATOR_USER_IDS` is set in no values file, and every qa-service values file has `SERVICE_CALLERS` commented out (`deploy/services/qa-service/values-prod.yaml:52-69` and the staging/qa/azure files), so the legacy path answers 403 `MODERATION_DISABLED` and `/v1/qa/internal/admin/*` answers 401. Reports are not routed anywhere else (notification-service logs and drops them, `qa_consumer.go:150-153`). The legacy store calls also swallow every error and skip state checks: `HideContent`, `LockQuestion`, `MergeQuestion` (`store/moderation.go:113-136`) always return nil, lock or merge deleted/merged questions, and hiding an answer does not decrement `answer_count`, unlike the token path (`admin_moderation.go:160-176`).
- **Scenario:** The day `QA_PUBLIC_ENABLED` flips, user reports accumulate and nobody can hide, lock or merge anything. If an operator "fixes" this by setting `QA_MODERATOR_USER_IDS`, moderation runs from a consumer session without admin-service MFA, step-up or audit, through store functions that report success on failure.
- **Fix:** Retire the legacy `/v1/qa/admin` group as the other services did, and make enabling the admin-service token path (keys plus `SERVICE_CALLERS`) a precondition of `QA_PUBLIC_ENABLED`.

### M2. Concurrent first votes double-count; vote counters drift from rows
- **Files:** `readAndUpsertVote` (`store/votes.go:35-48`) does a plain `SELECT` and then `INSERT ... ON CONFLICT DO UPDATE`. A missing row cannot be locked by the `SELECT`.
- **Scenario:** A double-tap sends two `POST .../vote {"up"}` at once. Both transactions read no prior vote, one inserts, the other blocks and then takes the conflict branch. Both compute delta `+1` from `""`, so `upvote_count`/`vote_score` go up by 2 with one row in `answer_votes`. Under H1 both also award reputation. GDPR erasure (M8) deletes vote rows without touching counters, so drift accumulates both ways.
- **Fix:** Take `pg_advisory_xact_lock(hashtext(table||user||target))` before the read, or use `INSERT ... ON CONFLICT DO NOTHING RETURNING` followed by `UPDATE ... WHERE vote_type <> $new RETURNING`, and derive the delta from which statement touched a row. Add a periodic reconcile of the counters against the rows.

### M3. Topics can be created anonymously, featured, with any icon URL
- **Files:** `POST /v1/qa/topics` (`handler.go:113`, `handler_topics.go:53-66`) never calls `getUserID`. `CreateTopicParams` binds `is_featured` and `icon_url` from the body (`store/topics.go:11-31`).
- **Scenario:** A logged-out script creates "free-followers-dm-me" topics with `is_featured: true` and an `icon_url` on an attacker host. They show on the Q&A home's featured list (`GET /topics?featured=true`), and every viewer's browser fetches the attacker's image (IP and referrer logging). Topic names and slugs are globally unique, so squatting good names is also possible.
- **Fix:** Move topic creation to the admin token family; at minimum require a user, force `is_featured=false`, and accept icons only as media-service ids.

### M4. No rate limits or size caps on posting, and answer requests can target anyone repeatedly
- **Files:** qa-service has no rate limiting at all (no limiter in `internal/`), and no length caps on title, body, comment, tags, report reason/details or draft content (only the admin token path caps `reason`, `admin_token.go:263`). `CreateAnswerRequest` (`qa.go:570-579`, `store/answer_requests.go:10-22`) accepts any `requested_user_id`, does not check that the requester or target can see the question, and `answer_requests` has no unique constraint (`setup.sql:253-260`). Each request becomes a push (`qa_consumer.go:236-256`). Reports have no dedupe and no target validation (`store/moderation.go:11-24`).
- **Scenario:** A harasser loops `POST /questions/<their question>/request-answer {"requested_user_id":"<victim>"}` and the victim receives hundreds of "asked you to answer" pushes. A spammer posts thousands of questions with multi-megabyte bodies (bounded only by the gateway body limit), which also makes search (M6) slower.
- **Fix:** Unique `(question_id, requested_user_id)`, a per-requester daily cap, and refuse requests the target cannot see. Per-user caps for questions, answers, comments, votes and reports (Redis or a ledger, as dating does), and length caps on every text field.

### M5. Boot DDL relocks `questions` on every start, with no lock timeout
- **Files:** `cmd/server/main.go:85-89` runs all of `database/setup.sql` on every boot through `BootstrapSchema` (`internal/store/postgres/bootstrap.go:13-24`): one parameterless `Exec`, so pgx sends it as a simple-protocol multi-statement query, which Postgres runs as **one implicit transaction**. That script unconditionally does `ALTER TABLE questions DROP CONSTRAINT IF EXISTS questions_status_check; ALTER TABLE questions ADD CONSTRAINT ... CHECK (...)` (`setup.sql:121-124`), which takes ACCESS EXCLUSIVE and re-validates every row, plus `ADD COLUMN IF NOT EXISTS` on `topics`, `questions`, `answers` and `community_qa_settings` (`:38-44,120,416-418`), each of which takes ACCESS EXCLUSIVE even when the column exists. The locks are held until the whole script commits. There is no `lock_timeout` and no advisory lock.
- **Scenario:** An HPA scale-up or rollout starts a pod while a long read (an analytics query, or a search scan from M6) holds `questions`. The pod's ACCESS EXCLUSIVE request queues, and every later Q&A read and write queues behind it until the pod's readiness fails and it restarts, trying again. As `questions` grows, each boot also blocks the table for a full CHECK validation.
- **Fix:** Move schema changes to versioned migrations (a `schema_migrations` row per step, run once under an advisory lock with `SET lock_timeout`). Until then, wrap the constraint in a `DO` block that checks `pg_constraint` first, and add it `NOT VALID` followed by a separate `VALIDATE CONSTRAINT`.

### M6. Search and similar-questions are unindexed `ILIKE '%q%'` scans open to anonymous callers
- **Files:** `store/questions.go:526-545` (title) and `:547-591` (title **or** body). The comment promises trigram matching, but `setup.sql` creates no `pg_trgm` index and no full-text index. `%` and `_` in the query are not escaped. The routes are anonymous (`handler.go:101,181`), and offset is allowed up to 10,000.
- **Scenario:** Each search scans every question's title and full body (unbounded, M4). A few concurrent anonymous searches with deep offsets saturate the pool (`MaxConns 25`), and every other Q&A request waits.
- **Fix:** A `pg_trgm` GIN index on `title` (and a `tsvector` on `body`), created `CONCURRENTLY` in a migration. Escape LIKE metacharacters. Cap offset lower for search, and rate-limit anonymous search.

### M7. Each answer recounts its whole community inside the write transaction and serializes on the community row
- **Files:** `CreateAnswer`/`DeleteAnswer` (`store/answers.go:38-49,104-110`) call `syncCommunityAnswerStatsTx` (`:229-272`): `COUNT(*)` over every answer in the community, then a `UNION` over all question and answer authors (`store/questions.go:669-705`), then `UPDATE qa_communities` and `community_qa_settings` for the same row, all inside the answer transaction. Question create does the contributor recount too (`questions.go:635-667`). `FollowQuestion`/`UnfollowQuestion` recompute `follow_count` with `COUNT(*) FROM question_follows WHERE question_id = $1` (`store/follows.go:13,21`), but the only index is the primary key `(user_id, question_id)` (`setup.sql:194-199`), so each follow is a sequential scan.
- **Scenario:** In a large community, every answer holds the community row lock for the duration of an O(N) count, so answers in that community proceed one at a time and slow down as it grows. This is the same pattern that the vote path already fixed (audit HQ2, `store/votes.go:11-17`).
- **Fix:** Apply deltas (`qa_answer_count + 1`), compute contributor counts asynchronously, and index `question_follows(question_id)` (and use a delta for `follow_count`).

### M8. Account erasure and community-membership events are dropped on failure, and erasure leaves content and drifts counters
- **Files:** The platform-events consumer uses `kafka.Reader.ReadMessage` with a `GroupID` (`internal/events/consumer.go:52-80`), which commits the offset before processing. A failed `processMessage` is only logged. `AnonymizeUser` (`store/feeds.go:191-206`) ignores every error and always returns nil. It soft-deletes questions, answers and comments but keeps their bodies, and leaves drafts, answer requests, reports and reputation events in place. It deletes the user's vote rows without adjusting `vote_score`/`upvote_count`/`downvote_count`.
- **Scenario:** A Postgres failover during a `user.deletion_requested` event, or during a `community.member_banned` event, means the user's Q&A text is never erased, or a banned member keeps private-community access, with nothing retrying. Every completed erasure leaves vote counts too high.
- **Fix:** `FetchMessage` and `CommitMessages` only after success (with a dead-letter after N tries). Run the erasure in one transaction that returns errors. Blank or hard-delete the bodies, delete drafts and answer requests, and decrement counters for the deleted votes.

### M9. Deleting or hiding the accepted answer leaves the question marked answered, and a double delete double-decrements
- **Files:** `DeleteAnswer` (`store/answers.go:85-112`) and both hide paths never clear `questions.best_answer_id`/`is_answered`, and they keep the +25 reputation. The ownership check (`qa.go:317-324`) runs outside the transaction, and the `UPDATE answers SET deleted_at` has no `deleted_at IS NULL` guard, so two concurrent deletes both decrement `answer_count`. The 500-answer cap (`qa.go:268`) reads a stale count (TOCTOU), which is only a soft limit.
- **Scenario:** An author deletes their accepted answer. The question stays out of the unanswered feed (`is_answered = true`), points to a deleted answer id, and the author keeps the points. A double-tap on delete makes `answer_count` one lower than the number of live answers.
- **Fix:** `UPDATE answers SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL RETURNING is_best`, decrement only when a row came back, and clear the best answer (and reverse its reputation) when `is_best`.

### M10. Web and notification contracts do not match the server, so several features are broken
- **Files and mismatches:**
  - Answer requests: the web sends `{target_user_id}` (`useQA.ts:555-557`); the server reads `requested_user_id` (`handler_engagement.go:246-255`), so every request is a 400. The web responds with `{response: "accept"}` (`useQA.ts:564`); the server reads `status` (`handler_engagement.go:293-301`), writes `''`, fails the CHECK and returns the raw SQL error as a 500.
  - Search: the server returns `data: {questions, pagination}` (`handler_search.go:49-56`); the web reads `res.data.data` as an array (`useQA.ts:770-774`).
  - Drafts: the web posts updates to `/v1/qa/drafts/questions/:id` and `/drafts/answers/:id` and reads `GET /drafts/questions/:id` (`useQA.ts:808-835,867`). None of these routes exists (`handler.go:184-189`; the server upserts with `id` in the body). The server binds camelCase `communityId`, `topicIds`, `isAnonymous` (`handler_drafts.go:14-22`); the web sends `community_id`, `topic_ids`, `is_anonymous`, so those fields are silently dropped.
  - Local feed: the web calls `/feed/local` without `lat`/`lng` (`useQA.ts:575`); the server requires them (`handler_feeds.go:89-93`), and nothing ever writes `local_scopes`, so the feed is empty regardless.
  - Notifications: notification-service resolves authors with `GET /v1/qa/questions/:id` and no user (`qa_consumer.go:61-90`). Anonymous questions come back with `author_id` masked to `uuid.Nil`, and private-community questions return 403, so their askers never get answer, vote or pin notifications.
- **Fix:** Pin these contracts in `testdata/contracts/` with golden fixtures copied into the web. Add a token-authenticated internal author lookup for notification-service (or carry `author_id` and `is_anonymous` in the event payloads), and decide the anonymous-recipient behaviour explicitly.

---

## LOW

### L1. Raw database errors are returned to clients
Many handlers return `err.Error()` as the message (for example `CREATE_FAILED` in `handler_moderation.go:33-35`, `handler_engagement.go:257-259`, `handler_topics.go:61-63`), exposing SQL text, constraint names and SQLSTATEs. Map them to fixed messages and log the detail.

### L2. Q&A events are fire-and-forget
`producer.publish` (`events/producer.go:149-177`) writes from a detached goroutine with a 5 s timeout and no outbox, so events are lost on shutdown or broker hiccups, and their order is not guaranteed. Moving to the shared outbox (already fixed on this branch) also lets the M10 author lookups go away.

### L3. Editing has no history and changes the slug
`UpdateQuestion` (`store/questions.go:223-256`) lets the author rewrite the title and body at any time (also after answers, a lock or acceptance), keeps no revision, and regenerates the slug from the new title. Old slug links 404, because `GetQuestionBySlug` is an exact match. Keep revisions, and resolve old slugs or route by the id suffix.

### L4. Comment votes allow self-votes
`VoteComment` (`qa.go:491-496`) has no author check, unlike questions and answers. No reputation is attached, so the only effect is on comment ordering.

---

## Checked and sound
- **Gateway edge:** `/v1/qa` is closed by its own gate unless `QA_PUBLIC_ENABLED` is `"true"` (`api-gateway/cmd/server/main.go:1382`), and also by `GATEWAY_DISABLED_PREFIXES` in prod, staging and QA. `/internal/` paths are refused at the edge, so `/v1/qa/internal/admin/*` is reachable only in-cluster. Identity headers are stamped only from a verified token, and anonymous callers arrive with no `X-User-ID`.
- **Admin token path:** `requireAdminToken` (`admin_token.go`) admits only an admin-service token carrying the route's permission, takes the actor from the signed `act` claim, ignores `X-User-ID`, `X-Scopes` and the internal key, and is registered outside the internal-key group. The token-path store functions lock the target row, check its state and write the moderation action in the same transaction (`store/admin_moderation.go`). No qa-service code reads consumer `X-Scopes`.
- **Ownership checks:** question update/delete, answer update/delete, comment update/delete (re-checked in SQL), reopen (author), best-answer unselect, and answer-request respond all verify the caller. Drafts are scoped by `author_id` in every SQL statement (`store/drafts.go`).
- **Votes:** one row per `(user, target)` by primary key, a CHECK on `vote_type`, O(1) delta counters, and self-votes refused for questions and answers. The gaps are H1 and M2.
- **Media:** a question's media must be the author's own confirmed uploads and is claimed in media-service before the row names it. It fails closed (503) without a media client, and it is released on delete, hide and erasure, with a reconcile tool (`cmd/media-claims`). Answers have no media attach path.
- **Community rules:** `ListQuestions`, `GetQuestion`, `GetAnswer`, `ListAnswers` and `CreateAnswer` enforce private-community membership and the ask/answer permissions. Settings updates require a community admin, and pinning requires a community moderator and checks that the question belongs to the community.
- **Pagination:** `limit` is at most 100 and `offset` at most 10,000 (`handler.go:262-278`). Leaderboard, contributors and similar have their own caps. Answers per question are capped at 500 (soft, M9).
- **Workers:** the only background loop is the Kafka consumer group, which partitions across replicas. There is no polling worker that would need row claims.
- **Payments:** none. Q&A has no bounties, tips or escrow, so no money path.
