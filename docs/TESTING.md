# VChat / atPost — Test Automation Plan

> Companion to `ARCHITECTURE.md`. Describes the **current** test footprint, the
> **target** test architecture, and the **workstreams** to get there. Grounded in
> what's actually in the repos (not aspirational) — each claim is checkable.

## 1. Current state (surveyed)

| Layer | State | Where |
|---|---|---|
| **CI** | GitHub Actions | `.github/workflows/`: `go-ci` (`go test -race ./...`), `lint` (golangci v1.61), `security` (govulncheck/gosec/trivy, weekly cron), `build-push`, `promote`, `frontend-ci` (flutter analyze+test) |
| **Go unit** | 186 test files; **22/31 services** have tests → **9 with none** | `Architecture/services/*`, `identity-platform/*`, `chat-service/*` |
| **Integration** | build-tagged (`-tags integration`), HTTP-level vs a running stack, **run by hand**, not in CI | `Architecture/tools/integration/` (+ `run-integration.sh`) |
| **Load** | k6-style, **only** rider + dating, not automated | `Architecture/services/{rider,dating}-service/loadtest` |
| **Mobile** | 19 Flutter tests, **in CI** | `mobile/atpost_app/test/`, `frontend-ci.yml` |
| **Web (postbook-ui)** | **Zero tests, not in CI** — biggest gap | — |

## 2. Known issue fixed (W1 — done)

The integration harness authenticated with synthetic `X-User-Id` headers. The
2026-06 auth hardening makes the **gateway strip inbound `X-User-Id`** and derive
identity from the verified JWT — so gateway-routed integration tests broke.
**Fixed:** `tools/integration/client.go` now mints a real HS256 JWT (signed with
the dev `JWT_SECRET`, `ATPOST_JWT_SECRET` override) and sends `Authorization:
Bearer` alongside `X-User-Id`; admin clients get privileged `scopes`. Works for
both gateway-routed and direct-to-service calls.

## 3. Target test architecture (mapped to the stack)

```
        ▲  fewer, slower, higher-confidence
   E2E  │  Playwright (web) + Flutter integration_test (mobile) → ephemeral stack
Contract│  consumer-driven contracts between services (gateway↔service drift guard)
Integr. │  per-service vs real Postgres/Redis/Kafka via testcontainers-go (in CI)
  Unit  │  Go per-service · Flutter widgets · Web Vitest + React Testing Library
        ▼  many, fast, run on every push
```

- **Unit** — pure logic, no network. Go (`go test -race`), Flutter (`flutter
  test`), **Web: Vitest + RTL** (new). Target: every service + every web
  feature module.
- **Integration** — one service + its real datastores, spun ephemerally with
  **testcontainers-go**; promote the existing build-tagged suite into CI.
- **Contract** — services call each other over HTTP; add consumer-driven
  contracts (Pact) or golden-response checks so a provider change can't silently
  break a consumer (esp. the gateway's identity-header contract).
- **E2E** — critical journeys through the whole system on an ephemeral stack
  (`stack.sh`): **Playwright** for web, Flutter `integration_test` for mobile.
- **Load/perf** — standardize k6, run nightly vs staging with pass/fail
  thresholds (p95 latency, error rate).

## 4. Workstreams (prioritized)

| ID | Workstream | Effort | Status |
|----|-----------|--------|--------|
| **W1** | Fix integration auth regression (real JWT minting) | S | ✅ done |
| **W2** | Web test foundation: Vitest + RTL + Playwright, wire into `frontend-ci` | L | next |
| **W3** | Integration-in-CI: testcontainers + ephemeral infra job; move build-tagged suite into the pipeline | M | |
| **W4** | Close Go unit gaps (9 bare services) + coverage threshold gate | M | |
| **W5** | E2E smoke: login→post→feed, chat send/receive, payment happy-path | M | |
| **W6** | Standardize k6 load + nightly thresholds vs staging | M | |

### W2 — Web test foundation (recommended next)
- Add `vitest` + `@testing-library/react` + `@testing-library/jest-dom`; unit-test
  pure logic first (`src/lib/api.ts` interceptor, response mappers, the new
  `/admin/access` console logic).
- Add `@playwright/test` for E2E; first specs: login, create post, admin role
  grant/revoke (the RBAC console).
- Extend `frontend-ci.yml` with a `web` job: `bun install` → `vitest run` →
  `playwright test` (against a preview build or the ephemeral stack from W3).

### W3 — Integration in CI
- `testcontainers-go` to boot Postgres + Redis + Redpanda per suite (fast, no
  full compose). For cross-service flows, a GitHub Actions `services:`/compose
  step bringing up the minimal set + `ATPOST_RUN_INTEGRATION=1`.
- New `integration` workflow (or job in `go-ci`) running `go test -tags
  integration` with the env wired to the ephemeral stack.

### W4 — Unit coverage gaps + gate
- Add unit tests to the 9 untested services (start with money-path: payments,
  wallet, bill-pay; then qa, channel, community, etc.).
- Add `-coverprofile` to `go-ci` and a soft threshold (e.g. fail under 50%,
  ratchet up) as a required check.

## 5. CI/CD wiring
Build on the existing Actions, don't replace:
- `go-ci`: add coverage profile + gate (W4).
- `frontend-ci` → add a `web` job (W2).
- new `integration` workflow (W3) — PR + nightly.
- `security.yml`: keep as-is (govulncheck/gosec/trivy).
- E2E + load run **post-deploy against staging** (ArgoCD stays the deploy path).
- Make unit + lint + integration **required checks** before merge to `main`.

## 6. Sequence
W1 ✅ → **W2** (biggest hole) → W3 (continuous cross-service safety) → W4
(coverage gate) → W5 (journey smoke) → W6 (perf). W3 and W4 can run in parallel
once W2 lands.
