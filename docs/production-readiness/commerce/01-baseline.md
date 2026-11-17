# Commerce — Stage 1 baseline

Run on 17 Nov 2026 on branch `codex/module-01-02-launch-safety`. The tracks audited the committed code at `be57a46bf`; the map was written at `d83616ad6`. The web tree is `pb-echo` at `1b596b4`. The run was read-only: nothing was started against any deployed environment, payment provider or courier, and no local databases, Docker or devices were used.

About 700 files in the working tree hold another session's uncommitted changes, many of them in admin-service. The audit reads the committed code. Where a test run below includes uncommitted edits, the row says so.

## Build, lint and unit tests

| # | Command | Exit | Result |
|---|---|---|---|
| 1 | `scripts/verify/go.sh --full Architecture/services/commerce-service` | 0 | PASS: build, vet, vet-integ, test. |
| 2 | `scripts/verify/go.sh --full Architecture/services/payments-service` | 0 | PASS: build, vet, vet-integ, test. |
| 3 | `scripts/verify/go.sh --full Architecture/services/wallet-service` | 0 | PASS: build, vet, vet-integ, test. |
| 4 | `scripts/verify/android.sh --fast :core:commerce :feature:commerce :feature:seller :core:commerce-ui :core:payments` | 0 | RESULT PASS, unit tests on 5 modules. |
| 5 | Go unit tests per service (Track F) | 0 | • commerce-service: 989 pass, 13 skip (local-delivery database tests; the feature is off)<br>• payments-service: 487 pass, 1 skip<br>• admin-service: 828 pass<br>• wallet-service: 31 pass, 64 skip (database tests; commerce does not use the wallet)<br>The payments and admin counts include another session's uncommitted edits. |
| 6 | Android commerce modules, forced re-run (Track F) | 0 | 513 pass, 0 skip, 0 fail. |
| 7 | Web shop folders, `bun test` (Track F) | ≠0 | 642 pass, 23 skip, 2 fail. Both failures are contract fixtures that have drifted from the backend goldens. |
| 8 | `QA_GUARDS_ONLY=1 charts/atpost-service/render_test.sh` (Track E) | 1 | commerce and payments pass. The run as a whole fails on post-service's QA values file in the uncommitted working tree. |
| 9 | `scripts/ci/kafka-topics-check.sh` (Track E) | 0 | PASS. |
| 10 | `scripts/verify/migration-compat.sh` over the commerce and payments migrations (Track E) | 0 | 64 old statements flagged. Only commerce migrations 051 and 052 matter for rolling the app back one version. |
| 11 | payments-service `TestResolveRefundRequiredWorker` (verifier v1) | 0 | PASS, and it pins the finding: with `ENV` set to prod or unset, the refund-required worker is off. |

Integration tests were not run. They run in CI only, on the scratch database `commerce_it_test`.

## CI on the branch (from `gh run list` / `gh run view`)

| Workflow | State |
|---|---|
| Go CI, Lint, Security, Integration (all Go services) | Green on `a17b0fb0e` and later. Security includes a gitleaks scan of the full history. |
| E2E | Green on `be57a46bf`, run 44111297089, all 14 tiers including the commerce tier. The commerce tier pays through the stub confirm; no test sends a signed provider webhook (COM-F-1). |
| integration-commerce.yml | Last ran on 31 Oct, 19 commerce and payments commits ago. It is the only place the gated-migration proofs run (COM-F-4). |
| Android CI | Unit tests and compilation pass. Lint fails on app manifests outside the commerce modules; fixes are in progress. |
| web-ci | Green on `1b596b4`. The two failing shop fixture tests skip in CI, because the backend checkout is absent there (COM-F-7). |

## Security tooling

| Scan | State |
|---|---|
| Secret scan | Grep-based check of commerce and payments code, client config and logs is clean (`verify/v3`, part 2g). The CI Security run on HEAD (44111294714) scans the full history with gitleaks and passes. The `.gitleaksignore` suppressions were not reviewed. |
| Go dependency scan (govulncheck) | Passes in CI since the 17 Nov dependency bump. Not installed locally. |
| Android and web dependency scans | UNVERIFIED: none configured. |
| Static analyser (gosec / semgrep) | UNVERIFIED: not installed. |
| Container / IaC scan | trivy filesystem scan passes in CI. checkov and tfsec are UNVERIFIED: not installed. |

## Not run, and why

- **Journeys:** none was executed end to end. `02-feature-status.md` walks them in code.
- **Payment provider and courier:** no Razorpay or Shiprocket call was made. The Razorpay capture mode (COM-A-13) and Shiprocket's answer to a ₹0 declared value (COM-A-4) are unconfirmed.
- **Query plans and capacity:** no `EXPLAIN` and no load. Every capacity figure in Track D is arithmetic.
- **Deployed state:** whether the gated migrations ran anywhere, and what the live secrets hold (courier mode, Razorpay keys), could not be checked.
