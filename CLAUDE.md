# modernsmapp

Backend Go services (`Architecture/`, `identity-platform/`, `chat-service/`)
and the Android app (`mobile/android/`). Agents follow
[docs/harness/AGENT-RULES.md](docs/harness/AGENT-RULES.md): no git writes,
no secrets, no local databases or devices, and the same checks every time:

- Go: `scripts/verify/go.sh --fast <module-dir>` while working, `--full` before reporting.
- Android: `scripts/verify/android.sh --fast :module` while working, `--full` once at the end.
- Integration tests run in CI only (`.github/workflows/integration-all.yml`) on `_test` databases.
- After a dev deploy: `scripts/smoke/dev-smoke.sh` (see `docs/harness/`).
