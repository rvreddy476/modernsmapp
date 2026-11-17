# Reels verification v2: security and performance

Independent verifier, 17 Nov 2026. Read-only. For each finding I read only the
track's Title and Evidence, then went looking for whatever would make it false:
network policy anywhere, a mesh, an internal-key middleware, purge hooks under
other names, CDN overrides, web build configuration, HPA/KEDA objects, alerts
defined elsewhere. I ran no tests (none would settle these claims), started no
servers, and made no network calls.

Paths: `ms/` = `Architecture/services/media-service`; `pb/` = `C:\workspace\wt\pb-echo`.

---

## REELS-B-1: media-service read routes trust a bare X-User-Id, and network policy is off

**Verdict: CONFIRMED.** Evidence level: VERIFIED-READ for the trust and for the
missing policy. Exploitation is SUSPECTED, because it needs code running inside the cluster.

What I checked:
- `ms/internal/http/delivery_viewer.go:27-33`: the viewer is `uuid.Parse(c.GetHeader("X-User-Id"))`, with no check.
- `ms/internal/http/handler.go:159-181`: `/batch`, `/:id`, `/:id/url`, `/serve`, `/hls/...` and the download routes are registered with no middleware. `RequireInternalKey` is used only on `/v1/media/internal` (`:195`).
- `ms/cmd/server/main.go:440-444`: the only global middleware is Recovery, RequestID, Logger and Metrics. Nothing strips or authenticates X-User-Id. `optionalAuthMW` is passed to RegisterRoutes but is not attached to the read routes. `AuthMiddlewareWithKeys` (`middleware.go:178-195`) only *sets* the header after it verifies a JWT, and it is attached to writes only.
- `ms/internal/service/record_read.go:110-113`: the uploader short-circuit trusts the header value. `post-service .../EvaluateMediaAccessFacts` (story_surface.go ~253) answers allowed or not_ready for `uploaderID == viewerID`, so the owner preview works for anyone who sends the author's id.
- Network policy: `charts/atpost-service/values.yaml:380-381` has `enabled: false`. Only `deploy/services/payments-service/values-{prod,qa}.yaml` set a networkPolicy, and nothing under `deploy/services/media-service/` does. It is worse than the track says: `infra/terraform/modules/eks/main.tf:58-61` installs the `vpc-cni` addon without `enableNetworkPolicy`, so even a NetworkPolicy object would not be enforced. There is no mesh or mTLS: no Istio, Linkerd, Cilium, Calico or SecurityGroupPolicy in charts/, deploy/ or infra/terraform (the only hit is infra/azure, which is not the AWS path).
- I found nothing that disproves this.

Severity: **P1 stays.** It is not P0, because the public edge (the gateway) is not a path in: an attacker needs a foothold inside the cluster or an SSRF that can set headers.

---

## REELS-B-2: account deletion never reaches analytics-service

**Verdict: CONFIRMED, with one correction to the evidence.** Evidence level: VERIFIED-READ.

What I checked:
- `identity-platform/services/auth-service/internal/purge/worker.go:44`: `DefaultRequiredServices` has no `analytics` and no `monetization`. `REQUIRED_PURGE_SERVICES` is set in no deploy or terraform file (grep shows it only in code and tests), so the default applies.
- **Correction.** The track says analytics has no purge path at all ("no match for purge ... anywhere"). That is not quite true. Two erasure paths exist, and both are dead:
  - `analytics-service/internal/consumers/engagement_consumer.go:127-162` handles `user.deletion_requested` on `social.events.v1` (main.go:42, 219-220) and runs `DELETE FROM analytics.events_raw WHERE user_id=$1`. But auth-service no longer emits that event. `auth-service/internal/store/lifecycle.go:142-149` says ScheduleDeletion emits `user.deletion_scheduled` and "deliberately NOT user.deletion_requested", and `http/account_control.go:5-16` records why. The purge worker emits `user.purge_requested` on the identity topic, which analytics does not consume. No non-test Go file publishes `EventUserDeletionRequested` (grep shows consumers only).
  - `analytics-service/internal/store/postgres/events.go:72-93` `DeleteEventsByUser` (described as the "account-erasure path") is called only by `follower_deltas_integration_test.go:174`.
  - Neither path touches `playback_sessions` or the Scylla watch tables, and neither sends an ack to `platform.purge-acks.v1`.
- Net effect: nothing deletes the analytics rows of a purged user, and auth-service's purge completes without waiting for analytics.

Severity: **P1 stays.** It is an erasure obligation that is not met, and auth-service reports success. The fix is smaller than the track implies: re-point the existing handler at `user.purge_requested`, extend it to sessions and watch history, ack as "analytics", and add "analytics" to the required list.

---

## D-1: web HLS withCredentials vs CloudFront allow_credentials=false

**Verdict: CONFIRMED (static chain). The browser outcome is still SUSPECTED, and the stated fallback consequence is wrong.**

What I checked:
- `pb/src/features/reels/components/ReelVideo.tsx:348-354`: `xhrSetup: xhr.withCredentials = true` on every hls.js request. hls.js is `^1.7.3` (package.json:36), whose default XhrLoader serves playlists and fragments. The same pattern is in `posttube/components/TubePlayer.tsx:362` and `posttube/hub/coverFrame.ts:160`.
- `ms/internal/service/playback.go:313-322` and `anonymous_scope.go:269-297`: child-playlist segment lines are replaced by absolute signed URLs. `deploy/services/media-service/values-prod.yaml:60-61,102` sets CloudFront key-pair secrets and `MEDIA_CDN_BASE_URL: https://media.cleestudio.com`.
- `infra/terraform/modules/media/main.tf:318` attaches the policy to the protected default behaviour, and `:299` attaches it to `public/*` too. `:365-378` sets `access_control_allow_credentials = false` and `origin_override = true`, so S3's own CORS (`:186-192`) is replaced. Prod origins (envs/prod/main.tf:175-178) are `https://cleestudio.com` and `https://app.cleestudio.com`, which are cross-origin to media.cleestudio.com. No other `response_headers_policy` exists in infra/terraform.
- **The track's consequence is wrong.** The track says the fallback MP4 goes through the Next proxy. In a prod web image it does not. `.github/workflows/build-web.yml:103-110` refuses to build prod unless `NEXT_PUBLIC_API_BASE_URL` is `https://api.cleestudio.com`, and `:154` passes it as a build arg. `pb/src/features/reels/model.ts:187-195` (`mediaHref`) then makes `fileUrl` and `hlsUrl` absolute on the API host. The fallback is browser → gateway → 307 → CloudFront.
- **That fallback probably fails too.** The `<video>` element sets `crossOrigin="use-credentials"` (ReelVideo.tsx:646), so the progressive request is a credentialed CORS fetch. After the gateway's 307 to CloudFront, the final response again lacks `Access-Control-Allow-Credentials: true`, and the origin may be tainted to `null` by the cross-origin redirect. Safari's native-HLS path (`video.src = hlsUrl`, :339-342) has the same problem. If this holds, reels do not play on the production web at all; they do not just degrade. The playlist itself is fine: the gateway sends `Access-Control-Allow-Credentials: true` (api-gateway/cmd/server/main.go:1294) and has prod CORS_ORIGINS (values-prod.yaml:82).
- A contradiction to settle: `deploy/web/app/values-prod.yaml:8` says the prod build must pass `NEXT_PUBLIC_API_BASE_URL=""`, which is the opposite of build-web.yml. If the build ever follows the values comment, the track's original Next-proxy fallback applies instead.

**What would confirm it:** a browser test on staging, or on QA with the CloudFront distribution, using a web image built by build-web.yml:
1. Open /reels in Chrome with DevTools open.
2. Confirm the console shows a CORS error naming `Access-Control-Allow-Credentials` on a `media.<domain>/…/*.ts` request.
3. Confirm the next request is `/v1/media/<id>/serve` on the API host.
4. Check whether that request's redirect to media.<domain> also fails with a CORS error (my reading predicts it does) or plays.
5. Repeat in Safari (native HLS).

Without a browser, use curl: `curl -sI -H 'Origin: https://app.<domain>' '<signed segment url>'`. It should show `Access-Control-Allow-Origin` and no `Access-Control-Allow-Credentials`. The curl check proves the header half only.

Severity: **P1 stays.** It becomes **P0 for web** if step 4 shows the progressive fallback is also blocked, because the core journey would be broken on web. Android is unaffected.

---

## D-2: Next.js proxy streams non-HLS media bytes and buffers 64 MiB upload parts

**Verdict: DISPROVED for production as the pipeline builds it. True for any build with an empty API base (compose and devbox).**

What I checked:
- The proxy code works as described: `pb/src/app/api/proxy/[...path]/route.ts:98-101` (`await req.arrayBuffer()` for writes), `:179-203` (follows redirects by hand and streams), `:28-31` (the only timeout is the connect attempt). `next.config.ts:13-19` rewrites `/v1/:path*` to the proxy.
- But it is only reached through relative `/v1` URLs. The reel URLs (`model.ts:338-339,368-370`, `sounds.ts` `soundServeHref`) go through `mediaHref`, which prefixes `NEXT_PUBLIC_API_BASE_URL`. Uploads (`src/lib/resumableUpload.ts:54-65`) use the axios client with `baseURL: process.env.NEXT_PUBLIC_API_BASE_URL` (`src/lib/api.ts:142`). The prod build is forced to `https://api.cleestudio.com` (build-web.yml:103-110,154). In prod the browser therefore talks to the gateway directly, follows the 307 to CloudFront itself, and uploads parts straight to the gateway. The Next server carries none of these bytes.
- Residual risk: `deploy/web/app/values-prod.yaml:8` documents the opposite (`""`, which means proxy mode). The compose stacks use `""` (`Architecture/docker/docker-compose.yml:2985`). If the prod build is ever aligned with that comment, D-2 applies exactly as written.

Severity: **P3.** The behaviour is latent: it needs an empty API base, which the enforced build rejects. Fix the stale values-prod comment, or the build check, so the two agree. If the team chooses proxy mode deliberately, D-2 returns at P1.

---

## D-3: two short transcodes at once, no HPA, no back-pressure, no lag alert

**Verdict: CONFIRMED.** Evidence level: VERIFIED-READ for the structure. The queueing times are SUSPECTED until a load test runs.

What I checked:
- `ms/cmd/worker/main.go:214-252`: one `kafka.Reader` per class, one goroutine each. `transcode_classes.go:229-256` `consume` is strictly sequential (fetch, handle to durable, commit, then the next fetch).
- `deploy/services/media-service/values-prod.yaml:153-176`: `worker.replicaCount: 2`, classes `short,long` in the same pods, cpu limit 3.
- `charts/atpost-service/templates/hpa.yaml:1-13` targets only the server Deployment. The worker template says "NO SERVICE, NO INGRESS, NO HPA BY DEFAULT" (worker-deployment.yaml:23). No `ScaledObject` or KEDA object exists in charts/, deploy/ or infra/.
- `infra/terraform/modules/msk/topics-job/variables.tf:62-66` sets 6 partitions by default.
- Back-pressure: the only `QueueDepth` in media-service is the fingerprint gauge (`fingerprint_runner.go:52`). No init-time check exists.
- Lag alert: the only `kafka_consumergroup_lag_sum` rule is `Architecture/docker/alerts/slo_rules.yaml:65-66`, which belongs to the local compose Prometheus, not the cluster. No kafka exporter, MSK CloudWatch lag alarm, or rule referencing `media_transcode*` exists in charts/, deploy/ or infra/terraform.
- Aggravating factor, not in the track's evidence: E-1 (no `media.events.long` topic in prod) means long videos also run in the short loop, which shrinks reel capacity further.

Severity: **P1 stays.**

---

## reels-E-2: worker metrics never scraped, no worker alerts

**Verdict: CONFIRMED, with one nuance about crash alerts.** Evidence level: VERIFIED-READ.

What I checked:
- `charts/atpost-service/templates/worker-deployment.yaml:23-26` ("NO SERVICE"). The worker container (`:85`) declares no `ports` and has no `prometheus.io/*` annotations.
- `servicemonitor.yaml:12-19` selects the Service on port `http`, and that Service has only the server's named port, so worker pods are never endpoints. No PodMonitor is rendered, and no `additionalScrapeConfigs` are set in `infra/terraform/modules/observability/main.tf`.
- `prometheusrule.yaml:41-74` matches `container="media-service"` and `deployment="media-service"` exactly. The worker is `media-service-worker`, and the template has no worker rule (grep "worker": none). Media values have no `prometheusRule` block, and no file under charts/, deploy/ or infra/ outside the service code mentions `media_transcode`.
- **Nuance:** kube-prometheus-stack is installed with its default rules on (no `defaultRules` override, observability/main.tf:54-130), so the generic `KubePodCrashLooping` and `KubeDeploymentReplicasMismatch` alerts do evaluate for the worker. But `alerting.tf:19-23,77-83` routes everything without `source=atpost-service` to the `null` receiver, and `route_platform_critical` is set in no env. A crash-looping worker therefore shows in the Alertmanager UI and notifies nobody. The claim "no alert covers worker crashes" should read "no alert that reaches anyone".

Severity: **P1 stays**, because the transcode path has no alerting.

---

## Summary

| ID | Verdict | Evidence level | Severity (track → corrected) | Key correction |
|---|---|---|---|---|
| REELS-B-1 | CONFIRMED | VERIFIED-READ (exploit SUSPECTED) | P1 → P1 | Worse: VPC CNI has no network-policy enforcement, so enabling the chart's policy alone would do nothing |
| REELS-B-2 | CONFIRMED | VERIFIED-READ | P1 → P1 | Two dead erasure paths exist (handler on the retired `user.deletion_requested`, unused `DeleteEventsByUser`); re-point one |
| D-1 | CONFIRMED (static), runtime SUSPECTED | VERIFIED-READ chain | P1 → P1, P0-web if fallback also fails | In prod the fallback is direct (API base forced to api host) and the `<video crossOrigin="use-credentials">` fallback likely fails CORS too |
| D-2 | DISPROVED for prod | VERIFIED-READ | P1 → P3 | build-web.yml forces `NEXT_PUBLIC_API_BASE_URL=https://api…`, so media and uploads bypass the Next proxy; values-prod comment contradicts it |
| D-3 | CONFIRMED | VERIFIED-READ (timings SUSPECTED) | P1 → P1 | E-1 makes it worse (long videos in the short loop) |
| reels-E-2 | CONFIRMED | VERIFIED-READ | P1 → P1 | Default kube-prometheus crash alerts exist but route to `null` |
