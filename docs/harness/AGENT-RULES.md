# Agent rules

Every agent working in these trees follows this page. The lead (the session
that spawned you) verifies your work, commits it and deploys it; you never do.
Background: the "VChat agent harness" doc (https://claude.ai/artifact/1YQQPkgDp8X9bmBPfXjVoF).

## Trees

| Tree | Path | Branch | Check |
|---|---|---|---|
| Backend (Go) + Android | `C:\workspace\modernsmapp` | `codex/module-01-02-launch-safety` | `scripts/verify/go.sh`, `scripts/verify/android.sh` |
| Web (postbook-ui) | `C:\workspace\wt\pb-integ` | `feat/tiktok-parity-web` | `scripts/verify.sh` (Bun only) |
| Admin console | `C:\workspace\atpost-web-ui` `apps/admin` | `feat/commerce-catalogue-attributes` | lint, typecheck, vitest |

## Never

- Run a git write: no commit, add, rm, reset, stash, checkout, push. Reading git is fine.
- Print, paste or log a secret, token, password, OTP or key, even in a test failure.
- Touch `.env*` (the `.example` templates excepted), `local.properties`, `google-services.json`, `.secrets/`, `tmp/`, `output/`, `vchat-site/`.
- Start Docker, a database or a server locally. Integration tests run only in CI, on databases whose name ends in `_test`; `go.sh --db` refuses anything else.
- Drive an emulator or a phone (adb, uiautomator, screen taps). The founder runs the device.
- Deploy, rebuild a dev container or change AWS.
- Take money any way but payments-service on its signed event.

## Checking your work: fast while working, full once

Run the same commands the lead runs. Do not invent your own build lines.

| Tree | While working | Once, before you report |
|---|---|---|
| Go | `scripts/verify/go.sh --fast <module-dir>` | `scripts/verify/go.sh --full <module-dir>` |
| Android | `scripts/verify/android.sh --fast :module ...` | `scripts/verify/android.sh --full` |
| Web | `scripts/verify.sh --fast [paths]` | `scripts/verify.sh --full` |

With no module named, each script checks every module with a changed file.
Each prints one `PASS`/`FAIL` line per stage and ends with `RESULT PASS` or
`RESULT FAIL`; paste the final block into your report. A `FAIL` you could not
fix goes in the report with its log path, never hidden.

Android `--full` takes minutes; run it once at the end, not after every edit.
One Android check per tree at a time (the script holds a lock).

## Lanes

- One agent per folder or service tree; never two agents in the same files.
- **Android lanes are small: one founder decision per lane.** The lead commits
  and builds an APK when each lane lands, then starts the next. If a request
  arrives mid-lane, it becomes the next lane unless the lead says otherwise.
- **Two Android lanes may run at once only in two worktrees and only on
  disjoint modules.** The lead creates the second one with
  `scripts/harness/android-worktree.sh`; the script refuses overlapping
  module lists.
- Contracts first: routes, fields and error codes are pinned before clients
  are built. Golden JSON fixtures are copied byte for byte from the service's
  `testdata/contracts/` into web and Android.

## Proving a guard works (mutation checks)

A guard is a check that refuses something: an auth gate, a money rule, a
privacy filter, a permission, a validation. Prove it by breaking it on purpose
and watching a named test fail, then restoring the file.

- **Server (Go): every new guard.**
- **Android: guards in money, security, privacy and permissions only**
  (founder, 25 Oct). Screen wording and layout get ordinary tests, not
  mutations. List each mutation in your report with its category.
- Use `scripts/harness/mutate.sh`: it locks the tree, backs up each file in a
  per-run folder, verifies every restore by hash, and refuses to start if a
  run is already going. Never hand-roll a mutation loop.
- A mutation that does not compile proves nothing; break the logic, not the syntax.
- A surviving mutation is reported with the reason (for example: unreachable
  backstop), never quietly dropped.

## Your report

1. What changed, by area, with file paths.
2. The final `RESULT` block of each full check.
3. Mutations: count killed / total, each survivor with its reason.
4. Judgement calls you made and anything you left undone.
5. Files outside your lane that you noticed changed (not yours to touch).
