# Reels launch gate

**Verdict: NO-GO.** Gate lines 1, 3, 6 and 8 fail, and any fail on lines 1 to 8 means NO-GO.

Findings are cited by their track or verifier IDs. Each one's `Was:` line in `03-findings.md` maps it to its final `reels-N` number. The ones cited below map like this:

| Cited as | Final ID |
|---|---|
| A-2, C-1, C-4 | reels-1 |
| D-1 | reels-2 |
| D-3 | reels-4 |
| reels-E-2, C-7, V4-16 | reels-5 |
| B-1 | reels-6 |
| V4-1 | reels-7 |
| F-11 | reels-9 |
| F-1 | reels-10 |
| F-3 | reels-12 |
| F-5 | reels-13 |
| V4-7, B-13 | reels-15 |

Every other ID cited (B-3, V4-2, V4-3, V4-17, reels-E-11) can be found by its `Was:` line in `03-findings.md`.

| # | Gate line | Result | Evidence |
|---|---|---|---|
| 1 | Every core user journey works end to end | **FAIL** | No journey was run end to end; there is no environment and no test (F-1, confirmed in `verify/v3`). Three journeys are known to break:<br>• A reel can stay `pending` for ever when create races the completion event, or when the consumer drops a failed flip (A-2 + C-1 + C-4, confirmed in `verify/v1`).<br>• The web cannot resume an upload after a reload (F-5, confirmed in `verify/v3`).<br>• Web playback may be blocked entirely by the CDN's CORS policy (D-1; SUSPECTED until tested in a browser, `verify/v2`). |
| 2 | No open P0 findings | PASS | No P0 survived verification. D-1 becomes P0 for the web if the browser test in `verify/v2` shows the MP4 fallback also fails. |
| 3 | Every route has an auth decision; every object-ID route has a tested authorization check | **FAIL** | • `GET /v1/analytics/content/:id/views` has no identity or owner check. The gateway stamps the internal key on anonymous requests too, so anyone can read the view counts of any reel, including private and deleted ones (V4-1; re-checked independently).<br>• media-service read routes trust a bare `X-User-Id`, and the cluster's network add-on enforces no network policy (B-1, confirmed in `verify/v2`).<br>• A stranger can like or comment on a pending, rejected or scheduled reel (V4-3). |
| 4 | No secrets in the repository, client bundle or logs | UNVERIFIED | The tracked tree is clean on a grep-based check (`verify/v4`). Git history was not scanned, because no gitleaks or trufflehog is installed. The devbox env falls back to values that are visible in the public repo (V4-17). |
| 5 | Dependency and static security scans clean | UNVERIFIED | No scanner was available locally, and the CI Security workflow is 177 commits stale (`01-baseline.md`). |
| 6 | Upload and media paths meet section 4.4 | **FAIL** | • Creator audio and voice files go through ffmpeg without a protocol whitelist, a forced container or a timeout (B-3).<br>• Deleting or making private does not invalidate the CDN. Signed segment URLs keep working for up to 35 minutes, signed URLs live 12 hours, and objects stay 90 days (B and D notes).<br>• prod sets no cap on the size of an original (reels-E-11).<br>The server-side probe, duration and size limits, GPS stripping and the fail-closed access gate hold (`verify/v4` part 2c). |
| 7 | Money paths meet the commerce checks | N/A | Reels takes no payments. Creator-fund accrual is in the monetization module and is off in prod (`06-extra-scope.md`). One input to it, watch time, can be inflated by the client (V4-2, P2); this is recorded under the findings. |
| 8 | Retried and concurrent writes are safe for core actions | **FAIL** | • Create racing the transcode-complete event leaves a reel `pending` for ever. The video_metadata insert after commit is only logged on failure, and consumer errors are dropped before the offset is committed (A-2 + C-1 + C-4).<br>• Transcode idempotency, the retry cap with quarantine, the stall sweeper, delete-during-processing and view dedupe all PASS (`verify/v4`). |
| 9 | Build, tests and migrations pass in CI from clean | **FAIL** | • Android CI fails at "Write google-services.json".<br>• The web repo has no CI.<br>• Go CI is stale for feed-service.<br>• No CI job transcodes a real video (F-3, F-11).<br>Integration and E2E are green at HEAD~1. |
| 10 | Logs, metrics and alerts for core flows | **FAIL** | • The media worker's metrics are never scraped.<br>• The default crash-loop alerts go to the `null` receiver (reels-E-2, confirmed in `verify/v2`).<br>• Quarantine and the DLQ have no alert or replay (C-7, V4-16). |
| 11 | Rollback path exists and backups restore | UNVERIFIED | The newest migrations are compatible with rolling back one version (`01-baseline.md`, Track E). No rollback has been exercised and no restore has been tested. Backup retention is a decision still open with the founder. |
| 12 | Prod config has debug, test accounts and permissive settings off | **FAIL** | media-service accepts HS256 tokens signed with the shared secret in prod. A token without `exp` never expires, and neither `iss` nor `aud` is checked (V4-7; re-checked independently: `media-service/internal/http/middleware.go:86-162`, `deploy/services/media-service/values-prod.yaml:51-55`). Release mode, the scanner's prod fail-closed and the startup checks all hold. |

## To move to GO WITH CONDITIONS

1. Engineering fixes lines 1, 3, 6, 8, 9, 10 and 12, in the order given in `05-fix-plan.md`.
2. Someone with staging access runs the D-1 browser test in `verify/v2-security-perf.md`.
3. CI gets a secrets scan and a dependency and static scan (lines 4 and 5).
4. The founder decides backup retention, and ops runs one restore and one rollback (line 11).
