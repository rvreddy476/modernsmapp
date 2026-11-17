# Long video launch gate

**Verdict: NO-GO.** Gate lines 1, 3, 6 and 8 fail, and any fail on lines 1 to 8 means NO-GO.

Findings are cited by their track or verifier IDs (LV-A to LV-F, LV-V3). Each one's `Was:` line in `03-findings.md` maps it to its final `lv-N` number. `reels-N` IDs are findings inherited from `../reels/03-findings.md`; they sit on the shared media pipeline and apply here unchanged.

| Cited as | Final ID |
|---|---|
| LV-A-1 | lv-1 |
| LV-B-2 | lv-2 |
| LV-B-1, LV-D-3 | lv-3 |
| LV-E-1 | lv-5 |
| LV-E-2 | lv-6 |
| LV-V3-1 | lv-7 |
| LV-V3-5 | lv-8 |
| LV-F-1 | lv-10 |
| LV-E-3 | lv-13 |
| LV-C-1 | lv-16 |
| LV-C-2 | lv-17 |
| LV-E-7 | lv-21 |
| LV-B-3 | lv-28 |
| LV-A-6 | lv-30 |
| LV-A-7 | lv-31 |
| LV-C-3 | lv-53 |
| LV-E-11 | lv-67 |

| # | Gate line | Result | Evidence |
|---|---|---|---|
| 1 | Every core user journey works end to end | **FAIL** | No journey was run; there is no environment and no test (LV-F-1). Walked in code, three of the six fail:<br>• **Switch public to private:** an open player and a copied stream URL keep working for up to 12 hours (LV-B-2, confirmed and raised to P1 in `verify/v2`).<br>• **Subscribe and receive the next upload:** subscribers are never notified of an ordinary upload, because fan-out runs while the video is still processing and is then skipped for good (LV-A-1, confirmed in `verify/v2`).<br>• **Create, reorder and share a playlist:** neither client can share one (LV-A-7).<br>Also broken: web caption upload always fails (LV-V3-5). In prod, long transcodes run inside the reel loop because the `media.events.long` topic is missing (reels-3), and a transcode that is interrupted restarts from zero (LV-E-1). |
| 2 | No open P0 findings | PASS | No P0 was raised by any track or verifier. Through the gateway, no user can read, change or delete another user's videos, playlists, series, captions, progress or history (Track B; `verify/v3` part 2a). |
| 3 | Every route has an auth decision; every object-ID route has a tested authorization check | **FAIL** | • Every route has an authorization check, but several have no stranger test: trim, category override, publish, distribution, playlist delete, restore, caption owner refusal, and series episode add and delete (`verify/v3` part 2a).<br>• `POST /v1/video-series` and `/v1/playlists` store any `channel_id` without an ownership check, which lets one user block another's account erasure (LV-V3-1; re-checked independently).<br>• Inherited: media-service trusts a bare `X-User-Id` inside the cluster (reels-6), and the analytics view-count route has no identity check (reels-7). |
| 4 | No secrets in the repository, client bundle or logs | UNVERIFIED | A grep-based check of the long-video code, client config and logs is clean (`verify/v3` part 2b). Git history was not scanned, because no gitleaks or trufflehog is installed. |
| 5 | Dependency and static security scans clean | UNVERIFIED | No scanner is available locally, and the weekly Security workflow on main has failed since 30 Aug (`01-baseline.md`). |
| 6 | Upload and media paths meet section 4.4 | **FAIL** | • There is no per-user storage or transcode quota. The only limit is 10 uploads an hour and 30 a day, and it fails open when Redis errors (LV-B-1 + LV-D-3, confirmed in `verify/v1`).<br>• Deleting, making private, a copyright block or a lapsed membership does not invalidate signed segment URLs for up to 12 hours, and there is no CDN invalidation (LV-B-2).<br>• `POST /v1/videos/:id/cover-frame` stores any `thumbnail_url` unchecked, which bypasses image moderation (LV-B-3).<br>• The 4-hour length limit is enforced only after the full upload of up to 16 GiB (LV-A-6).<br>• A resumable upload and its asset expire 24 hours after the upload starts (LV-C-1).<br>Abandoned multipart cleanup holds: a Terraform lifecycle rule plus a sweeper (`verify/v3` part 2d). |
| 7 | Money paths meet the commerce checks | N/A | Long video takes no payments in this module. Creator-fund accrual and memberships are separate modules; see `06-extra-scope.md`. |
| 8 | Retried and concurrent writes are safe for core actions | **FAIL** | • The schedule worker publishes at `publish_at` while the video is still transcoding (LV-C-2).<br>• A post can stay `pending` for ever when create races the transcode-complete event (reels-1, inherited).<br>• After a Kafka rebalance, two pods can run the same transcode (`verify/v3` part 2d; the state stays safe, the compute is duplicated).<br>• The watch-progress ordering guard does nothing, because no client sends `sent_at_ms` (LV-C-3).<br>These pass: scheduled publish fires exactly once, playlist reorder is safe under concurrency, subscriptions are idempotent with an outbox, and progress is owned by the caller (`verify/v3`). |
| 9 | Build, tests and migrations pass in CI from clean | **FAIL** | • Android CI fails before Gradle starts.<br>• The web repo has no CI.<br>• Go CI is stale, and feed-service is in no integration run.<br>• No CI job transcodes a real video.<br>• The chart guard fails on two drifts (`01-baseline.md`; reels-9, reels-12, LV-E-11).<br>Integration and E2E are green at `770f5ed9d`. |
| 10 | Logs, metrics and alerts for core flows | **FAIL** | • The worker's metrics are never scraped, and the default alerts go to the `null` receiver (reels-5, inherited).<br>• Transcode metrics have no short or long label, and the duration histogram ends at 600 s.<br>• Nothing is counted for progress writes, scheduled publish or notification fan-out (LV-E-7).<br>• A crash, an OOM kill or an eviction of the worker is counted by nothing (LV-E-2). |
| 11 | Rollback path exists and backups restore | UNVERIFIED | The 15 long-video migrations are compatible with rolling back one version (`01-baseline.md`). No rollback has been exercised and no restore has been tested. Backup retention is a decision still open with the founder. |
| 12 | Prod config has debug, test accounts and permissive settings off | **FAIL** | The six services checked set `ENV: prod`, run gin in release mode, and have stub, mock and review-all flags off (`verify/v3` part 2c). The line still fails on one inherited finding: media-service accepts HS256 tokens signed with the shared secret in prod, with no `iss` or `aud` check and no required `exp` (reels-15). |

## To move to GO WITH CONDITIONS

1. Engineering fixes lines 1, 3, 6, 8, 9, 10 and 12, in the order given in `05-fix-plan.md`. Most of the reels P1 fixes clear the inherited items on both modules at once.
2. Someone with staging access runs the six journeys, including the reels-2 browser test (web playback through the CDN) and a real long encode on production-sized hardware (LV-E-3).
3. CI gets a secrets scan, and the weekly Security workflow is repaired (lines 4 and 5).
4. The founder decides backup retention, and ops runs one restore and one rollback (line 11).
