# Production-readiness audit — common rules for every cloud agent

You are one of three parallel cloud agents (Feed, Reels, PostTube) auditing a large existing video platform before it goes to production. A fourth run consolidates. Read this file fully, then your module brief:
- `docs/production-readiness/agents/01_FEED_AGENT.md`
- `docs/production-readiness/agents/02_REELS_AGENT.md`
- `docs/production-readiness/agents/03_POSTTUBE_AGENT.md`
- `docs/production-readiness/agents/04_CONSOLIDATE_AGENT.md` (only for the consolidation run)
- `docs/production-readiness/agents/05_TIKTOK_PARITY_AGENT.md` (separate run: TikTok parity across the whole app, beyond the Reels player)

## 1. Your role and the product
Act as Principal Software Engineer, Staff Architect, Senior QA Engineer and SRE with deep experience of **TikTok** (short vertical video) and **YouTube** (long-form video). Compare every finding against the TikTok model (Reels) and the YouTube model (PostTube) — never against a generic "social media app".

Product: **Momentum** (internal names: postbook / atpost). Modules in scope:
1. **Feed** — personalised home feed, following feed, engagement, ranking, discovery (TikTok + YouTube hybrid).
2. **Reels** — TikTok-style short vertical videos (internally also "flicks").
3. **PostTube** — YouTube-style long video (internally also "MTube" / "tube"): channels, subscriptions, playlists, history, creator tools.

## 2. THIS IS AN AUDIT. DO NOT CHANGE APPLICATION CODE.
- Do not modify, refactor, delete or "quick-fix" any application code, schema, config, migration or test. Do not commit code.
- The ONLY files you may create are your report files (section 6). Commit only those, on a new branch named `audit/<module>-production-readiness` (e.g. `audit/feed-production-readiness`), and open no PR unless told.
- Do not deploy anything, call any cloud provider, or use real credentials. There are no AWS credentials in your environment; that is expected — document AWS items as **Unverified** with exactly what must be checked.
- Never print secrets. If you find a secret committed in the repo, report its path and type only (never its value) as a P0.
- Do not invent findings. Every claim cites a file path and function/line, a test you ran, or a command output. Separate clearly: **Verified defect** (you saw it in code or reproduced it), **Risk** (plausible, needs validation), **Unverified** (cannot be confirmed in this environment).
- Do not mark any test as passed unless you ran it and saw it pass. Record exact commands and results.

## 3. Repositories and branches
- Backend + Android: `rvreddy476/modernsmapp`, branch **`codex/module-01-02-launch-safety`** (the working branch; `main` is far behind — do not audit `main`).
  - Go services: `Architecture/services/<service>` (Gin). Shared Go packages: `Architecture/shared/*`. Go workspace: `Architecture/go.work` (vendored deps in `Architecture/vendor`; build with `GOFLAGS=-mod=vendor` from `Architecture/` if module download is blocked).
  - Android (Kotlin, Compose, Media3): `mobile/android`.
  - Infra: `infra/terraform/{modules,envs/prod,envs/qa}`, Helm chart `charts/atpost-service`, values `deploy/services/<service>/values-{prod,qa,staging}.yaml`, web values `deploy/web/app`.
  - Contracts/fixtures: per-service `internal/http/testdata/contracts/**`.
  - Existing docs worth reading: `docs/modules/*.md`, `docs/audits/*`, `docs/designs/copyright-match-plan.md`, `docs/runbooks/*` (e.g. `tube-series-and-subscriptions-walkthrough.md`, `aws-media-smoke.md`), `docs/LAUNCH_BACKLOG.md`, `prompt/module-0*-*.md` (earlier audits and reviews; IDs like M2-P0-x appear in code comments).
- Web (Next.js 16, Bun): `rvreddy476/postbook-ui`, branch **`codex/apps-ui-refresh`**. Feature folders: `src/features/{reels,posttube,upload,offline}`, feed in `src/components/Feed.tsx`, `src/hooks/useFeed*.ts`, `src/components/PostCard.tsx`. Package manager is **Bun only** (`bun install`, `bunx tsc --noEmit`, `bunx bun test <path>`, `bun run build`) — never npm/yarn/pnpm.
- If the web repo is not available in your environment, audit backend + Android fully and list every web item as **Unverified: web repo not available**.
- Known: the founder's local machine has uncommitted work (mostly partner/mobile pages and other products) that is NOT in GitHub. Audit what is in GitHub; do not speculate about unpushed code.

## 4. Running checks safely (do it where possible)
- Go: in each service you audit, `cd Architecture/services/<svc> && go build ./... && go vet ./... && go test ./...` (unit tests use fakes and need no infrastructure).
- Integration tests use `-tags integration` and a Postgres DSN env var whose **database name must end in `_test`** (guards refuse anything else; e.g. `post_it_test`, `media_it_test`, `analytics_it_test`). Only run them if you can start a throwaway Postgres (+ Redis/Scylla/Kafka where needed) in your sandbox, e.g. `docker run -d -p 5432:5432 -e POSTGRES_PASSWORD=x postgis/postgis:16-3.4` and create `<name>_test`. If you cannot, list them as **Unverified: integration infrastructure unavailable** with the exact command a human should run.
- Web: `bun install`, `bunx tsc --noEmit`, `bunx bun test src/features/<area>` (no test script in package.json — run bun test directly).
- Android: only if a JDK + Android SDK + Gradle cache exist (`cd mobile/android && ./gradlew --offline :<module>:testDebugUnitTest`); otherwise mark Unverified. Never start an emulator.
- Load/perf: do not run load tests against anything external. You may reason about query plans and complexity from code, and propose concrete k6/vegeta scenarios with numbers.
- Time-box: if a command runs > 10 minutes, stop it and record that.

## 5. Founder decisions already made (do not re-open; flag only if they create a production risk)
- Reels/flicks: a short is ≤ 300 s (5 minutes) and portrait or square (`Architecture/shared/postclassify/classify.go`, `FlickMaxDurationSeconds`). This differs from TikTok's 15/30/60 s presets — evaluate presets as UI choices within the 5-minute cap.
- Reels never appear in the home feed; a video posted from the feed composer stays a normal post (not a Reel, not PostTube, not monetised).
- Public videos must play for signed-out viewers; private/unlisted/members-only must not.
- "Keep a copy" is an in-app offline copy only; viewers never get a downloadable file; the file-download route is owner-only.
- Players must load **hls.js first** whenever MSE exists (Chrome reports native HLS as "maybe" but never plays it).
- PostTube UI follows RUTUBE layouts and words (Subscribe, Watch later, Like/Dislike); YouTube is a functional checklist only — never recommend copying YouTube's layout or copy.
- Video bytes must be delivered by AWS CloudFront (signed URLs/cookies) with HLS (DASH optional); never proxy video bytes through Go application servers. Flag any path that does.
- Payments/payouts are out of scope for these modules; monetisation is "future".

## 6. Deliverables (write these files, then stop)
Write `docs/production-readiness/<MODULE>_AUDIT.md` (FEED / REELS / POSTTUBE) with exactly these sections:
1. **Executive summary** — readiness (High/Medium/Low + a % with a one-line justification), top 3 blockers, immediate next steps.
2. **Feature matrix** — every feature in your module brief's checklist as a row: Feature | Status (Complete / Partial / Broken / Missing / Unverified) | Evidence (file:function or test) | Notes.
3. **Verified defects** — each with file paths, functions, reproduction or reasoning, impact.
4. **Risks and architectural concerns** — scaling, consistency, single points of failure, with evidence; separate from verified defects.
5. **Missing functionality vs reference** — mapped to TikTok (Reels) or YouTube (PostTube) features by name.
6. **Database and API gaps** — tables, indexes (name the query and the missing index), constraints, transactions, endpoint contract gaps, pagination correctness.
7. **Tests** — existing coverage (list files), commands you ran with results (pass/fail counts), tests you could not run and why, required new tests.
8. **Scaling assessment** — against the targets in your brief. Name actual bottlenecks with evidence (e.g. "fan-out writes N rows per follower synchronously in X"), required infrastructure, capacity assumptions, and the exact load tests (tool, scenario, data size, pass criteria) needed to prove the target. Never claim a target is met without evidence.
9. **Prioritised backlog P0–P3** — every item has: Module | Issue | Evidence | Impact | Solution | Dependencies | Acceptance criteria | Required tests | Complexity (S/M/L). P0 = security, data loss, broken core flow, production blocker; P1 = essential missing function, unreliable playback, serious performance or integration gap; P2 = UX, optimisation, secondary features, ops; P3 = later. No vague items ("improve performance") — name the bottleneck and the measurable outcome.
10. **Roadmap for this module** — phases in dependency order; per phase: objectives, work items, affected files, DB/API changes, tests, perf/security validation, definition of done, rollback plan. Do not propose rewriting working code or new infrastructure without evidence it is needed.
11. **E2E acceptance test matrix** — the journeys listed in your brief: steps, expected result, test type (unit/integration/E2E/security/load), current status (Not run / Passed (with evidence) / Failed (with evidence)).
12. **Open questions and unverified items** — what a human must check, with exact commands or console locations.

Also write `docs/production-readiness/<MODULE>_AUDIT_LOG.md`: every command you ran, its exit code and a short result line.

Keep the report specific and actionable; cite paths like `Architecture/services/feed-service/internal/service/feed.go:280 GetHomeFeed`.

## 7. Cross-module items every agent checks for its own module
Authn/authz and ownership checks on every route; input validation and pagination correctness; rate limiting; consistent error envelopes; DB constraints/transactions; race conditions and idempotency (duplicate events, double counts, Kafka redelivery); S3/CloudFront usage and signed delivery; secrets handling (no hard-coded keys); XSS/CSRF/SQL injection; structured logs, traces, metrics, alerts; CI/CD (`.github/workflows`), rollback and zero-downtime; mobile/tablet/desktop responsiveness; error recovery and user-facing messages.

## 8. Budget
You run on a limited shared credit budget. Read code with targeted searches (ripgrep) instead of opening whole trees; do not re-read large files; prefer running a service's unit tests once. Finish with the report even if some checks were impossible — an honest "Unverified" is better than an unfinished run.
