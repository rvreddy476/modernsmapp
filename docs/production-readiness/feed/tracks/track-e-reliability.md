# Track E: reliability, observability and deployment (home feed)

- **Audit date:** 2026-11-16. Read-only. Tree: `C:\workspace\modernsmapp`, branch `codex/module-01-02-launch-safety` @ `3c860874e`, dirty working tree read as it is.
- **Scope:** feed-service, post-service, graph-service, api-gateway, the shared chart and deployment machinery.
- Backend paths are relative to `Architecture/services/` unless they start with another top-level folder.
- Helm is not installed on this machine (`helm: command not found`) and Docker was not started, so no chart was rendered. Chart statements are from reading the templates.

## Short answers

| Question | Answer |
|---|---|
| Is there a production environment? | **No.** Terraform, values and ArgoCD manifests for prod and QA exist as code. Nothing shows they were ever applied; the only running environment is the dev box (Docker Compose on one EC2 server). See E-2. |
| Are alerts wired to a person? | **No.** Six alert rules exist, for feed-service prod only. No Alertmanager receiver or route is in the repo. See E-3. |
| Has rollback been exercised? | **No.** There is no written rollback procedure; the deployment plan lists writing and drilling one as a future step. See E-4. |
| Has a restore been tested? | **No.** Aurora and ElastiCache backups are defined in Terraform (not applied). Scylla has no backup at all. No restore evidence. See E-5. |

## Findings

```
ID:            E-1
Title:         The chart's liveness probe calls /livez, which feed-service, post-service and graph-service do not serve, so their pods would be restarted in a loop on Kubernetes.
Severity:      P0
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      charts/atpost-service/values.yaml:145-146 (livenessProbe.path: /livez); charts/atpost-service/templates/deployment.yaml:140-146 renders it for every service.
               Architecture/shared/health/checker.go:135-138 registers only /healthz and /readyz.
               feed-service/cmd/server/main.go:262, post-service/cmd/server/main.go:692, graph-service/cmd/server/main.go:315 call checker.RegisterRoutes and add no /livez.
               No livenessProbe override in deploy/services/{feed,post,graph}-service/values-{prod,qa,staging}.yaml (ripgrep "livez|livenessProbe" over deploy/, charts/, infra/ matches only the chart and deploy/web/*, whose comment says "the chart's defaults /livez and /readyz do not exist on a Next app").
               Only api-gateway serves /livez (api-gateway/cmd/server/probes.go:16).
How to reproduce: Render the chart for feed-service prod and read the livenessProbe path; then GET /livez on a running feed-service (expect 404 or 401, not 200).
Impact:        With initialDelaySeconds 10, periodSeconds 10, failureThreshold 3, the kubelet kills each pod about 30-40 s after start, for ever. The home feed, posting, likes, follows and blocks never stay up on EKS. The dev box does not use the chart, which is why this has not been seen.
Fix:           Smallest: make shared/health also register GET /livez (same handler as /healthz). Alternative: change the chart default to /healthz and set /livez for api-gateway only.
Acceptance:    A unit test in shared/health asserts /livez answers 200; render_test.sh asserts that each service's liveness path is one its binary registers.
Effort:        S
```

```
ID:            E-2
Title:         No production (or QA) environment exists and CI has never produced a deployable image of the current code.
Severity:      P0
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      docs/runbooks/aws-first-production-deployment.md:7 "Nothing in this plan has been run. The repo's AWS design has never been applied" (2 Oct; last commit to that file 8082dca4a, 2026-10-02).
               `git ls-remote --heads origin` shows refs/heads/main only for the names main / qa / release/prod: the branches ArgoCD tracks (deploy/argocd/applicationset.yaml:64, deploy/README.md:27) do not exist.
               `gh run list --workflow build-push.yml --limit 4`: all four are failure, the latest 2026-10-03. `gh run list --workflow promote.yml`: no runs.
               .github/workflows/build-push.yml:33-36 builds on push to main only; `git log -1 origin/main` = adad00644 (2026-08-26) and `git rev-list --count origin/main..HEAD` = 1058. build-push.yml:73-87 skips every build while secrets.AWS_CI_ROLE_ARN is unset ("it did not on 25 Oct 2026").
               deploy/services/feed-service/values-prod.yaml:13 tag "" ; deploy/services/api-gateway/values-prod.yaml:183,186 still hold __TF_*__ placeholders; no *.tfvars in infra/terraform/envs/prod or envs/qa.
               deploy/README.md:204 says the new AWS account "is QA for good; production gets its own account later". infra/devbox/README.md:3-5: the running stack is Docker Compose on one EC2 server.
How to reproduce: Run the commands above.
Impact:        There is nowhere to launch to. Every production control in this track (probes, HPA, PDB, ExternalSecrets, alert rules, backups) is untested code. The first deployment will be the first test of all of it.
Fix:           Follow the plan's own order on QA first: Terraform, secrets seeder, a successful build-push run from the branch that ships, promote, sync, then scripts/smoke against QA. No new infrastructure is needed; the designed one has to be applied once.
Acceptance:    A green build-push run for feed/post/graph/gateway at the launch commit; `kubectl -n atpost get deploy` on QA shows all four Available for 24 h with zero restarts; smoke passes against QA.
Effort:        L
```

```
ID:            E-3
Title:         Alert rules exist only for feed-service in prod and nothing routes any alert to a person.
Severity:      P1
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      deploy/services/feed-service/values-prod.yaml:93-137 defines six rules (lag, DLQ, fan-out, ranker fallback, hydration, p95). `grep -rln prometheusRule deploy/services` = 1 file: no rules for post-service, graph-service or api-gateway, and none in any values-qa.yaml or values-staging.yaml.
               infra/terraform/modules/observability/main.tf:126-147 configures Alertmanager with replicas, storage and a node selector only: no `config`, receivers or routes (grep for "receivers|route|webhook|sns" in the module: no match). The SNS e-mail topic in infra/terraform/modules/ops-baseline/main.tf:17-71 receives budget, backup, GuardDuty and SES events; nothing connects Alertmanager to it.
               No Grafana dashboard for these services in the repo (grep "grafana_dashboard" finds only the sidecar label in the observability module).
How to reproduce: Read the files above.
Impact:        A firing FeedHydrationFailing or FeedConsumerLagHigh is visible only to someone who opens the Alertmanager UI. A dead post-service, graph-service or gateway raises nothing at all. Outages are found by users.
Fix:           Add an Alertmanager config to the existing observability module with one receiver (e-mail through the existing ops SNS topic, or a Slack webhook) and a route for severity page/ticket. Add to the chart a default rule set for every service: pod restarts, 5xx ratio, p95 latency, readiness failing. Copy the feed rules into values-qa.yaml.
Acceptance:    On QA, scaling graph-service to zero produces a message in the chosen channel within 10 minutes.
Effort:        M
```

```
ID:            E-4
Title:         There is no rollback procedure and no evidence one has ever been exercised; migrations are forward-only and run inside the application at boot.
Severity:      P1
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      docs/runbooks/aws-first-production-deployment.md:133 lists "a written rollback (ArgoCD history) drill" as future hardening step 9. grep -i "rollback|roll back" over docs/runbooks, docs/harness, deploy/README.md, DEPLOY_SECRETS.md, SECURITY.md, PRODUCTION_READY_SUMMARY.md, infra/terraform/envs/prod/README.md finds no procedure. .github/workflows/promote.yml has no rollback or revert path.
               feed-service/cmd/server/main.go:67, post-service/cmd/server/main.go:129, and graph-service's bootstrap apply migrations at process start through shared/store/migrationrunner/runner.go:44-113 (forward only, no down files). post-service/cmd/server/main.go:134 also runs ensureSchema DDL at boot.
               The new version's migrations therefore run when its first pod starts, while old pods still serve. Rolling the image back does not undo them.
How to reproduce: Read the files above.
Impact:        A bad release cannot be backed out with confidence. Whether version N-1 runs against version N's schema is unknown for every release, and nobody has practised the steps.
Fix:           Write a one-page procedure: revert the values-<env>.yaml tag commit, sync in ArgoCD, check /readyz and smoke. Adopt the rule that a migration must be compatible with the previous image (add, backfill, then remove in a later release). Drill it once on QA.
Acceptance:    A dated record of a QA drill: deploy N, roll back to N-1, smoke passes, time taken written down.
Effort:        M
```

```
ID:            E-5
Title:         ScyllaDB (home timelines, likes, reactions, comment and share indexes) has no backup of any kind, and no restore has been tested for any store.
Severity:      P1
Category:      data
Evidence level: VERIFIED-READ
Evidence:      infra/terraform/modules/scylla/main.tf:89-159 defines the ScyllaCluster with racks, resources and storage only; grep -i "backup|snapshot|manager" in modules/scylla matches only the agent version variable description (variables.tf:23). No Scylla Manager, no backup task, no bucket.
               Aurora: backup_retention_period from var (infra/terraform/modules/aurora/main.tf:188; prod default 7 days, envs/prod/variables.tf:326-330), deletion_protection true (envs/prod/main.tf:120), plus AWS Backup daily, 14 days (envs/prod/ops.tf:4-13, modules/ops-baseline/main.tf:75-140).
               ElastiCache: daily snapshot, 5 days (envs/prod/main.tf:151, modules/elasticache/main.tf:142-146).
               None of this is applied (E-2). docs/runbooks/aws-first-production-deployment.md:133 lists "Backups verified by a restore" as a future step. grep for "restore drill|restore test|pg_restore" in docs/, infra/, deploy/ finds no record.
               Dev box: no database backup (infra/devbox/devbox.cfn.yaml:406 DeleteOnTermination true on the data disk; only the S3 buckets are retained).
How to reproduce: Read the files above.
Impact:        A lost or corrupted Scylla cluster loses social_engagement (post_likes, user_likes, reactions_by_post, comments_by_post, bookmarks) and every home timeline. Timelines could be rebuilt with cmd/backfill; engagement rows have no other complete copy that this track could find. For Postgres and Redis the backups are configuration that has never produced a restorable copy.
Fix:           Scylla: enable the operator's backup task (Scylla Manager, which the operator chart already supports) to an S3 bucket, daily, 7-14 days. Then run one restore of each store on QA and write down the steps and the time.
Acceptance:    A dated restore record for Aurora (point in time), ElastiCache (snapshot) and Scylla (one keyspace) on QA, with row counts compared.
Effort:        M
```

```
ID:            E-6
Title:         The feed consumer gives an event about 7 seconds of retries, then parks it on the dead-letter topic, and there is no tool to replay that topic.
Severity:      P1
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/events/consumer.go:61-66 builds the shared consumer without MaxRetries, RetryBackoff or RetryForever. Architecture/shared/kafka/consumer.go:118-123 defaults to 3 retries with 1 s backoff, doubled (lines 316-333): 1 s + 2 s + 4 s. After that lines 336-344 write to the DLQ, mark the event processed and commit.
               No replay command: feed-service/cmd holds backfill, server, timeline-maintenance; Architecture/tools holds none for a DLQ; grep -il dlq in those folders matches only cmd/server/main.go.
               The alert text says "replay once fixed" (deploy/services/feed-service/values-prod.yaml:109) without a means.
How to reproduce: Stop Scylla or graph-service for 10 s on dev while a post is created; the PostCreated event appears on social.events.v1.dlq and the post never reaches followers' timelines.
Impact:        Any Scylla, graph-service or Postgres interruption longer than about 7 s (a node restart, a deploy of graph-service) drops every fan-out, delete, visibility change and unfollow purge that arrives in that window. New posts are silently missing from followers' feeds. Stale rows from a missed delete or visibility change are still hidden at read time by the hydration filters (E "Checked and sound"), so this is a completeness problem, not a privacy leak.
Fix:           Set RetryForever: true for the two feed consumers and wrap undecodable payloads with sharedkafka.Permanent, so an outage pauses the partition instead of discarding events (the shared consumer already supports both). Add a small `cmd/dlq-replay` that republishes messages whose x-dlq-consumer-group is feed-service-group.
Acceptance:    Test: handler fails for 60 s then succeeds; the event is applied once and nothing is written to the DLQ. A malformed payload still goes to the DLQ.
Effort:        S
```

```
ID:            E-7
Title:         A stalled Kafka consumer is invisible: readiness does not cover it and the lag gauge freezes when fetching stops.
Severity:      P2
Category:      observability
Evidence level: SUSPECTED
Evidence:      feed-service/cmd/server/main.go:118-125 registers readiness checks for postgres, redis and scylladb only. The consumer goroutines (lines 208, 218, 234, 252) report to nothing.
               The loop no longer exits on a fetch error (shared/kafka/consumer.go:186-195), which is sound, but retryDurable (lines 435-455) blocks a partition for ever on a failing commit or DLQ write, logging only.
               The lag gauge reads reader.Stats().Lag every 10 s (consumer.go:462-481). In the vendored kafka-go the periodic lag reader is off for consumer groups (vendor/github.com/segmentio/kafka-go/reader.go:1145-1151) and the gauge is set only when a fetch returns (reader.go:1531), as the last fetched partition's value, not a sum.
               The channel-update consumer (internal/consumers/channel_updates.go:89-112) and the account-purge consumer have no metrics at all.
What would confirm it: on dev, block the handler (for example pause Scylla) and watch atpost_feed_service_kafka_consumer_lag while producing events: it should stay flat.
How to reproduce: As above.
Impact:        The pod stays Ready and FeedConsumerLagHigh may not fire while feeds stop updating.
Fix:           Record the time of the last successful handler return and of the last fetch in the shared consumer; export `kafka_consumer_last_progress_timestamp_seconds`; alert when it is older than 5 minutes while the topic has traffic. Do not add it to readiness (removing the pod from the Service does not help a consumer).
Acceptance:    The dev experiment above raises the alert within 10 minutes.
Effort:        S
```

```
ID:            E-8
Title:         On shutdown the three services close their databases before draining HTTP, and the gateway does not handle SIGTERM at all.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      Architecture/shared/server/server.go:83-94: on a signal it calls cfg.OnShutdown() first (line 88), then srv.Shutdown(ctx) (line 91).
               feed-service/cmd/server/main.go:270-279 OnShutdown closes rdb, scyllaSession and dbPool; post-service/cmd/server/main.go:745-753 and graph-service/cmd/server/main.go:323-328 do the same. Requests still in flight during the drain then run against closed stores and fail.
               api-gateway/cmd/server/main.go:309 `log.Fatal(edgelimits.NewServer(...).ListenAndServe())`; grep "signal\." in api-gateway/cmd/server finds nothing. SIGTERM ends the process and cuts every proxied request.
               charts/atpost-service/templates/deployment.yaml has no preStop hook and no terminationGracePeriodSeconds (grep: no match), so a pod also receives SIGTERM while the load balancer and kube-proxy may still send it new requests.
               feed-service/cmd/server/main.go:252 starts the account-lifecycle consumer on ctx, not consumerCtx, so it is never cancelled before its reader is closed (line 251 defer only).
How to reproduce: Send SIGTERM to feed-service while a /v1/feed/home request is in flight; the request fails with a closed-pool error.
Impact:        Every rolling deploy and every node drain produces a burst of 5xx on feed, like, comment and follow requests. Consumers are safe: offsets are committed only after the handler (see Checked and sound).
Fix:           In shared/server.Run call srv.Shutdown before OnShutdown. Add a 5 s preStop sleep to the chart. Give the gateway the same signal handling (http.Server.Shutdown with a 10 s timeout).
Acceptance:    A unit test in shared/server proves an in-flight request completes before OnShutdown runs; a rolling restart on QA under a light k6 load shows zero 5xx.
Effort:        S
```

```
ID:            E-9
Title:         Readiness fails on any shared store, so a short Redis problem takes every feed, post and graph pod out of service at once even where the code degrades without Redis.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ (readiness); SUSPECTED (that the home read works fully without Redis)
Evidence:      Architecture/shared/health/checker.go:62-109 marks the service down if any check fails; feed-service/cmd/server/main.go:119-125 includes Redis; post-service/cmd/server/main.go:344-348 and graph-service/cmd/server/main.go:123-124 likewise. Probe: every 5 s, 3 failures (charts/atpost-service/values.yaml:151-155), check timeout 3 s.
               The feed code itself tolerates Redis errors on the paths read: rate limit fails open (feed-service/internal/http/handler.go:866-874), ranker falls back to chronological (internal/service/feed.go:584-588), policy cache falls back conservatively (internal/service/longvideo.go:382-391).
What would confirm it: stop Redis on dev and call /v1/feed/home with readiness ignored.
How to reproduce: Stop Redis for 20 s on a cluster; all pods of the three services go NotReady together.
Impact:        A 15-second Redis failover (ElastiCache has one replica in prod) becomes a full outage of feed, posting and graph instead of a slower, unranked feed.
Fix:           Keep Postgres and Scylla in feed-service readiness; report Redis in the /readyz body but do not fail on it for feed-service. Leave post-service as is (its like path needs Redis).
Acceptance:    With Redis stopped, feed-service /readyz answers 200 with redis: down, and /v1/feed/home returns a page.
Effort:        S
```

```
ID:            E-10
Title:         The home feed fails completely when identity-profile-service, media-service or trust-safety-service is down, although only the trust-safety lookup is a safety filter.
Severity:      P2
Category:      reliability
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/service/hydration.go:658-663: a profile or media fetch error fails the whole hydration ("profile hydration failed", "media hydration failed"); the channel card on the next lines is best-effort. internal/service/keywordfilter.go:112-116 fails closed on trust-safety (cached 60 s per viewer, line 31). feed-service/internal/http/handler.go:179-182 answers 502 HYDRATION_FAILED.
How to reproduce: Stop identity-profile-service on dev; GET /v1/feed/home returns 502.
Impact:        Seven services and stores must all be up for one feed page (Scylla, Postgres, graph, post, trust-safety, profile, media). Author names, avatars and media URLs are needed to draw a card, so serving without them is a product decision; failing the whole page for one missing avatar batch is the costly choice.
Fix:           Decide per dependency. Minimum: when the profile or media batch fails, retry once, then serve the posts whose data is cached and drop the rest with a cursor, instead of 502 for all. Keep the graph and keyword filters fail-closed.
Acceptance:    Test: profile fetch errors, cached authors still render, response is 200 with a shorter page.
Effort:        M
```

```
ID:            E-11
Title:         A like is recorded in Redis first; the ScyllaDB write is best-effort and the Kafka event that feeds the durable counters is fire-and-forget.
Severity:      P2
Category:      data
Evidence level: VERIFIED-READ (code path); SUSPECTED (permanent loss)
Evidence:      post-service/internal/service/post_engagement.go:152-156 toggles in Redis (Lua). Lines 159-171: Scylla React/Unreact errors are logged with log.Printf and ignored. Lines 193-200: the engagement event is published in a goroutine with context.Background(); an error is logged and dropped. Only PostReacted (the notification trigger) goes through the outbox (lines 205-216).
What would confirm it: read engagement.ToggleLike and the pg/scylla counter consumers to see whether anything other than that Kafka event writes the durable like row; then on dev stop Kafka, like a post, restart Redis without persistence.
How to reproduce: As above.
Impact:        With Scylla down the user sees a successful like that hydration may not show; with Kafka down the durable counters miss it. Neither failure has a metric. reconcile.NewEngagementReconciler (post-service/cmd/server/main.go:709) recounts but can only count what was stored.
Fix:           Publish the engagement event through the existing outbox table (as PostReacted already is) and add a counter for Scylla reaction write failures.
Acceptance:    Test: Kafka producer returns an error; the event is still delivered after recovery.
Effort:        S
```

```
ID:            E-12
Title:         A request cannot be followed from the gateway through feed-service to graph-service or post-service: the request ID is not forwarded and the three services never start tracing.
Severity:      P2
Category:      observability
Evidence level: VERIFIED-READ
Evidence:      The gateway sets X-Request-Id on the upstream request (api-gateway/cmd/server/main.go:1264-1269) and injects the trace context (line 386). feed-service reads it into the logger (Architecture/shared/middleware/requestid.go:22-49).
               feed-service outbound calls set only X-Internal-Service-Key and Content-Type (for example internal/service/privacyfilter.go:175-182, internal/service/feed.go:1444-1452); grep -i "X-Request-ID|traceparent|otelhttp|trace.Init" over feed-service (non-test) returns nothing.
               grep "otel|o11y/trace|Tracing" in post-service and graph-service cmd/server/main.go returns nothing, although every values file sets OTEL_EXPORTER_OTLP_ENDPOINT (deploy/services/feed-service/values-prod.yaml:55) and the FeedHomeLatencyHigh alert says "See the per-dependency breakdown in traces" (line 137).
               feed-service has 160 log.Printf lines against 45 slog calls (grep count, non-test); log.Printf lines are plain text with no request_id, for example internal/service/feed.go:412, 467, 587.
               Architecture/shared/httpclient/client.go has no Prometheus metric (grep prometheus: 0), so an open circuit breaker is visible only in logs.
How to reproduce: Call /v1/feed/home with X-Request-Id: test-1 and search graph-service logs for test-1: nothing.
Impact:        A slow or failing feed page cannot be tied to the downstream call that caused it without matching timestamps by hand. Traces sent to Tempo contain the gateway span only.
Fix:           Add a RoundTripper in shared/httpclient that copies X-Request-Id (and injects traceparent) from the context; it covers every client built by NewWithBreaker. Add the existing OTel middleware and init to the three mains, or remove the endpoint from values and the sentence from the alert. Export breaker state as a gauge.
Acceptance:    The same request_id appears in gateway, feed-service and graph-service logs for one home request.
Effort:        S
```

```
ID:            E-13
Title:         The code that would ship has no current CI verdict for unit tests, lint or security scans, and feed-service and api-gateway have no tests against a real database.
Severity:      P2
Category:      tests
Evidence level: VERIFIED-RUN
Evidence:      .github/workflows/go-ci.yml:18-35 and security.yml:3-12 run on pull_request, push to main and manual dispatch, not on push to the working branch. `gh run list --workflow go-ci.yml`: latest run 2026-10-31, workflow_dispatch, head 44f00fafe, success; HEAD is 3c860874e. Lint, Security and E2E on this branch: also last run 2026-10-31 (success).
               Integration (all Go services) does run on push: run 43861254948 at 3c860874e is success for 33 modules including post-service, graph-service and Architecture/shared. The two pushes before it failed in commerce-service (run 43858523110). 10 of the last 17 runs on the branch failed.
               That job list has no feed-service and no api-gateway: `grep -rl "go:build integration"` in those two modules returns nothing, and scripts/ci/integration-services.yaml lists post-service (line 41) and graph-service (line 76) only.
               Security on main (weekly schedule) failed on 2026-11-01, 11-08 and 11-15 in every job: gitleaks, trivy, govulncheck x5 (run 43695693441). main is the 26 Aug commit.
               Scanners: gitleaks, govulncheck per module, trivy filesystem with exit-code 1 on CRITICAL/HIGH (security.yml:79-85): they fail the workflow. govulncheck and trivy are skipped on pull requests (lines 31, 76). trivy uses aquasecurity/trivy-action@master (unpinned). Whether a failed check blocks a merge depends on branch protection, which was not read.
How to reproduce: Run the gh commands above.
Impact:        A regression in feed-service unit tests or a new vulnerable dependency since 31 Oct would not be reported by CI. The feed's Scylla and Postgres stores are exercised only with fakes.
Fix:           Add the working branch (or all branches) to the push trigger of go-ci.yml and lint.yml, or open the PR to main so the pull_request triggers run. Run Security by dispatch on the launch commit. Add one integration suite for feed-service's Scylla timeline store and Postgres MetaStore to integration-services.yaml.
Acceptance:    Green Go CI, Lint, Security and Integration runs whose head SHA is the launch commit.
Effort:        M
```

```
ID:            E-14
Title:         post-service's outbox worker takes no claim on rows, so with two replicas each event is published twice, each time with a new event_id.
Severity:      P2
Category:      reliability
Evidence level: SUSPECTED
Evidence:      post-service/internal/service/outbox.go:19-36 reads up to 100 unpublished rows, publishes each, then marks it. grep "SKIP LOCKED" in post-service/internal/store/postgres matches action_links.go, drafts.go and notes_v2.go but not outbox.go (GetUnpublishedOutboxEvents is at outbox.go:53). outbox.go:25 builds the envelope with events.NewEnvelope, which sets EventID to uuid.New() (Architecture/shared/events/events.go:2046), so the consumer's dedupe key (shared/kafka/consumer.go:248) differs for the two copies.
               graph-service's relay does lease rows with FOR UPDATE SKIP LOCKED (graph-service/internal/store/outbox_relay.go:26,130).
What would confirm it: read store/postgres/outbox.go:53-78 for a lock or claim column; run two post-service replicas on dev and count PostReacted or CommentCreated messages for one action.
How to reproduce: As above.
Impact:        The feed's handlers are written to tolerate a repeat (feed-service/internal/events/consumer.go:43). Other consumers of the same events, notifications in particular, may act twice. Prod runs two replicas (deploy/services/post-service/values-prod.yaml:16).
Fix:           Select with FOR UPDATE SKIP LOCKED inside a transaction as graph-service does, and use the outbox row id as the EventID.
Acceptance:    Integration test with two workers: each row is published once and carries its row id.
Effort:        S
```

```
ID:            E-15
Title:         The one-off timeline repair has not been run anywhere and every environment still reads the legacy timeline index.
Severity:      P2
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      FEED_INDEX_LEGACY_READ: "true" in deploy/services/feed-service/values-prod.yaml:78, values-qa.yaml:87, values-staging.yaml:73. Architecture/docker/docker-compose*.yml does not set it (grep: no match), and the code default is on (feed-service/internal/store/scylla/timelines.go:78), so dev reads the legacy index too.
               deploy/jobs/timeline-maintenance/run.sh:21-23 accepts only qa or prod, which do not exist (E-2). The old audit lists the QA run as a to-do (docs/production-readiness/FEED_AUDIT.md:62). grep for a record of a run in docs/harness and docs/production-readiness: none.
How to reproduce: Read the files above.
Impact:        Production starts with empty Scylla tables, so for prod the repair has nothing to fix and the flag can be "false" from day one. On dev the legacy index and unrepaired rows remain, so dev behaviour differs from what prod will run.
Fix:           Set FEED_INDEX_LEGACY_READ "false" in values-prod.yaml and values-qa.yaml now (empty clusters). Run the binary's dry run against dev Scylla once and record the counts.
Acceptance:    Values show "false"; a dated note with the dev dry-run output.
Effort:        S
```

```
ID:            E-16
Title:         Boot-time migrations for feed, post and graph run without the advisory lock the shared runner already offers.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      feed-service/internal/store/postgres/bootstrap.go:26, post-service/internal/store/postgres/bootstrap.go:31, graph-service/internal/store/bootstrap.go:31 call migrationrunner.Run. Architecture/shared/store/migrationrunner/exclusive.go:49 (RunExclusive, pg_advisory_lock) exists and is not used by these three. Run (runner.go:44-113) reads the applied set, then applies each missing file in its own transaction.
How to reproduce: Start two fresh replicas against an empty database at the same moment.
Impact:        On a first deploy or a scale-up during a release, two pods can apply the same migration concurrently; one fails and exits (main.go os.Exit(1)) and restarts. It converges, through a crash.
Fix:           Call RunExclusive in the three bootstrap files.
Acceptance:    The existing exclusive_integration_test pattern passes for the three services.
Effort:        S
```

```
ID:            E-17
Title:         Builds are vendored but not fully pinned, and the working tree being tested differs from the committed tree in go.work.sum and 572 vendor files.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-RUN
Evidence:      feed-service/Dockerfile:1 golang:1.26.10-alpine and :24 alpine:3.19 are tags, not digests; :3 `apk add --no-cache build-base` is unpinned; :20,22 build with -mod=vendor from the committed Architecture/vendor (4,023 tracked files). post, graph and gateway Dockerfiles use the same bases. The image sets no USER; the chart forces uid 1000 (charts/atpost-service/templates/deployment.yaml:162-164).
               Web: C:\workspace\wt\pb-echo\Dockerfile:4,9 `oven/bun:1` (floating major), :63 node:20-alpine; `bun install --frozen-lockfile` (line 7).
               `git diff --stat -- Architecture/go.work.sum`: 34 insertions, uncommitted. `git status --short Architecture/vendor`: 572 files modified (git prints LF/CRLF warnings for them; whether the change is only line endings was not established). vendor/modules.txt is not among them.
How to reproduce: Run the two git commands.
Impact:        Two builds of the same commit weeks apart can differ in base image and compiler toolchain packages. Local test results on this machine are for a tree CI has not seen.
Fix:           Pin the three base images by digest. Commit or discard the go.work.sum and vendor changes before the launch commit is cut (run `go work vendor` and check the diff is empty).
Acceptance:    `git status --short Architecture/vendor Architecture/go.work.sum` is empty at the launch commit; Dockerfiles reference @sha256 digests.
Effort:        S
```

```
ID:            E-18
Title:         The staging values files are stale and disagree with prod and QA, and promote.yml takes its image tags from them.
Severity:      P3
Category:      deployment
Evidence level: VERIFIED-READ
Evidence:      deploy/README.md:16 "staging is not applied anywhere". deploy/services/feed-service/values-staging.yaml:14 `tag: latest` (ECR is immutable and CI pushes SHAs, build-push.yml:221-224); ENV: staging (line 48); no JWT_*_PREVIOUS keys (lines 44-45 against values-prod.yaml:46-49); no prometheusRule, HPA or PDB block.
               post-service treats only ENV=prod or APP_ENV=production as production (post-service/cmd/server/main.go:59-63), so at ENV=staging it would start without INTERNAL_SERVICE_KEY; feed-service refuses at staging (feed-service/cmd/server/main.go:322-326).
               .github/workflows/promote.yml:12-14 copies image.tag from values-staging.yaml into values-qa.yaml or values-prod.yaml; until build-push succeeds once that tag is "latest".
               feed-service mounts JWT_SECRET, JWT_KID and the previous pair (values-prod.yaml:46-49) but does not read them: its env reads (grep over non-test Go files) contain no JWT variable.
How to reproduce: Read the files above.
Impact:        A first promotion would write "latest" into QA or prod values. An HMAC signing secret is mounted into a service that has no use for it.
Fix:           Make promote.yml refuse a tag that is not a 40-character SHA. Remove the four JWT keys from feed-service's externalSecret in all three files. Either delete the staging files or state in each header that only image.tag is live.
Acceptance:    promote.yml fails on "latest"; render_test.sh still passes.
Effort:        S
```

## Production configuration, enumerated

feed-service (`deploy/services/feed-service/values-prod.yaml`; code default where the file is silent):

| Setting | Prod value | Evidence | Verdict |
|---|---|---|---|
| ENV | prod | values-prod.yaml:52 | makes the key mandatory |
| INTERNAL_SERVICE_KEY | from ExternalSecret; boot refuses if empty when ENV/APP_ENV/DEPLOY_ENV is prod, production, staging or qa | values-prod.yaml:45; cmd/server/main.go:174-178, 317-329 | sound |
| Gin mode | release, hard-coded | cmd/server/main.go:255 | sound |
| CORS | none in feed-service; gateway only | - | n/a |
| /metrics | on the service port, no auth, registered before the key gate; no ingress | cmd/server/main.go:262-264; values-prod.yaml:83-84 | acceptable inside the cluster; no NetworkPolicy (chart default off, charts/atpost-service/values.yaml:332-333) |
| /healthz, /readyz | no auth, registered before the key gate | cmd/server/main.go:262 | sound |
| FEED_HF4_FALLBACK | "false" | values-prod.yaml:77; store/scylla/timelines.go:87 (on only when "true") | sound |
| FEED_INDEX_LEGACY_READ | "true" | values-prod.yaml:78 | E-15 |
| FEED_CELEB_PULL | unset: on | internal/service/celebpull.go:71 | intended default |
| FEED_CELEB_FOLLOWER_THRESHOLD | unset: 10,000, capped at 10,000 | celebpull.go:56-77 | intended default |
| FEED_DISCOVERY_SHARE | "0.2" | values-prod.yaml:81 | product choice |
| FEED_SCYLLA_TIMEOUT / FEED_REQUEST_DEADLINE / FEED_EVENT_DEADLINE | unset: 3 s / code default / 2 min | cmd/server/main.go:83; events/consumer.go:98 | sound |
| ECHO_ENABLED | unset | internal/service/notes.go:60 | default not read by this track (UNVERIFIED); the gateway closes Echo (api-gateway values-prod.yaml:107) |
| Debug route /v1/feed/internal/debug | registered in every environment | internal/http/handler.go:109; handler :896-911 has no admin check of its own | relies on the gateway refusing /internal/ paths (api-gateway/cmd/server/main.go edgeChain comment, lines 336-345). Not traced here; see Track B. UNVERIFIED |
| Dev-auth bypass, test accounts, seed data | none found in feed-service values or main.go | values-prod.yaml | sound |

post-service (`deploy/services/post-service/values-prod.yaml`): ENV prod (:79) makes INTERNAL_SERVICE_KEY and POST_RESTRICTION_HMAC_KEY mandatory (cmd/server/main.go:59-63, 89-102); moderation HMAC keys refuse boot if invalid (:65-82); Gin release (:685); SCHEMA_MIGRATIONS_BASELINE "false" (:145); REVIEW_ALL_VIDEOS "false" (:131); WEB_RISK_MOCK and ACTION_LINK_MIN_ACCOUNT_AGE_HOURS unset (:120). No stub or bypass flag present.

graph-service (`deploy/services/graph-service/values-prod.yaml`): ENV prod (:57); key mandatory in production (cmd/server/main.go:295-299); GRAPH_WRITE_SOURCE_STRICT "true" (:70; code default also "true", main.go:285); DATING_CALLS_ENABLED "false" (:73); Gin release (main.go:308).

api-gateway (`deploy/services/api-gateway/values-prod.yaml`): APP_ENV "production" (:75); refuses to start without issuer, audience and RS256 key (main.go:122-127), without valid proxy config (:108-112), without REDIS_ADDR in production (:232-236), with an invalid disabled-prefix list (:205-208); CORS_ORIGINS is an explicit list of three origins (:82) (a "*" would only warn, main.go:60-64, not refuse); GATEWAY_TRUSTED_PROXY_CIDRS set (:88); parked products closed (:95-124); METRICS_ADDR unset, so /metrics is served on the public listener only to requests that did not come through a proxy (main.go:179, pkg/metricsgate; logic not read in detail).

Environment differences without code changes: yes, all through values files and ExternalSecrets. QA is defined as prod's file with QA data, enforced by `charts/atpost-service/render_test.sh` (not run here: needs Docker).

Secret definitions: every `remoteRef` in the four prod values files has an entry in `tools/prodsecrets/manifest.yaml` (feed-service block at line 311; drift test in go-ci.yml:85-107, last CI run 31 Oct). Whether the secrets exist in AWS Secrets Manager: UNVERIFIED (no AWS access used; E-2 says nothing is applied).

## Dependency-down behaviour of the home feed read

| Dependency down | What the code does | User sees | Privacy filter |
|---|---|---|---|
| ScyllaDB | timeline read error returned (internal/service/feed.go:402-404) | 500 INTERNAL_ERROR (http/handler.go:238-241); pod also goes NotReady | n/a |
| Postgres | feed mutes / author feedback lookups fail closed (feed.go:1379-1394); distribution policy falls back to cache and drops uncertain posts (longvideo.go:324-327, 373-391) | 500; pod NotReady | fails closed |
| Redis | rate limit fails open (http/handler.go:866-874); ranker falls back to chronological and counts it (feed.go:584-588) | page served, unranked; but all pods go NotReady (E-9) | not involved |
| graph-service down or slow | block/mute set is an error (feed.go:446-449, 1442-1464: non-200 and bad body are errors); can-view fails closed (privacyfilter.go:78-81, 187-195); following/circle filters fail closed (feed.go:464-497). 5 s timeout with breaker (feed.go:156) | 500 or 502 | **fails closed**: no page is served without the block and private-account filters |
| post-service | hydration error (hydration.go:500-512) | 502 HYDRATION_FAILED "Could not load post details - try again" (http/handler.go:179-182) | n/a |
| trust-safety-service | keyword filter fails closed (keywordfilter.go:112-116), 60 s cache | 502 | fails closed |
| profile / media service | hydration error (hydration.go:658-663) | 502 (E-10) | n/a |
| analytics-service | own 2 s client and breaker; view counts are decoration (feed.go:150-155) | page served, zero view counts | n/a |
| Kafka | not on the read path; fan-out pauses | feed served, new posts arrive late | n/a |

Clients: Android shows an error state with Try again (mobile/android/feature/feed/src/main/kotlin/com/us/android/feature/feed/ui/FeedScreen.kt:351-353, 636-648). Web shows an alert block with Try again (C:\workspace\wt\pb-echo\src\components\Feed.tsx:281-295). The exact wording per error code was not checked.

Like and comment: a comment is written to Postgres with an idempotency key, the count adjusted in Postgres, and CommentCreated goes through the outbox (post-service/internal/service/post_comments.go, CreateCommentPG from line 138: CreateCommentIdempotent, AdjustCommentCount, InsertOutboxEvent); Scylla and Kafka being down do not lose it. A like: see E-11.

## Checked and sound

1. Block and mute filtering fails closed on any graph-service failure, including non-200 and undecodable bodies: feed-service/internal/service/feed.go:446-449, 1442-1478. Cold-start items pass the same filter: feed.go:525.
2. Private-account filter fails closed and denies authors the graph did not answer for: internal/service/privacyfilter.go:78-81, 134-142, 187-204.
3. Request-path and fan-out traffic use separate HTTP clients and breakers, so a failing fan-out cannot open the request path's breaker: internal/service/traffic.go:51-80.
4. The shared consumer commits an offset only after the handler succeeded or the DLQ write succeeded, writes the dedupe receipt only then, and does not exit on a fetch error: Architecture/shared/kafka/consumer.go:186-195, 277-296, 336-344, 426-455. VERIFIED-RUN: `go test ./kafka/` in Architecture/shared: ok (3.7 s).
5. Each feed event handler has a 2-minute deadline: feed-service/internal/events/consumer.go:98-124.
6. Unfollow purge is retried on an unresolved relationship rather than skipped: events/consumer.go:358-362.
7. feed-service refuses to start without INTERNAL_SERVICE_KEY in prod, production, staging or qa: cmd/server/main.go:317-329. VERIFIED-RUN: `go test ./cmd/server/` in feed-service: ok.
8. graph-service refuses to start without the key in production (cmd/server/main.go:295-299); post-service likewise (cmd/server/main.go:59-63).
9. The gateway refuses to start with a weak token policy, bad proxy config, or no Redis in production: api-gateway/cmd/server/main.go:108-127, 232-236.
10. Gin release mode is hard-coded in the three services: feed main.go:255, post main.go:685, graph main.go:308.
11. Probes are reachable without the internal key because they are registered before the gate: feed-service/cmd/server/main.go:262-264 against internal/http/handler.go:77-80.
12. Readiness in feed, post and graph checks Postgres, Redis (and Scylla for feed and post) with a 3 s timeout: Architecture/shared/health/checker.go:62-109; feed main.go:118-125.
13. HTTP servers have read, header, write and idle timeouts: Architecture/shared/server/server.go:13-56. VERIFIED-RUN: `go test ./server/`: ok.
14. Feed rate limiter cannot leave a counter without a TTL (single script): feed-service/internal/http/handler.go:859-866.
15. Ranker failure falls back to chronological and is counted: internal/service/feed.go:584-588; metric at internal/service/metrics.go:33-36.
16. Metrics exist for per-route rate, status and latency (Architecture/shared/o11y/metrics/http.go:33-64, route pattern label via c.FullPath, shared/middleware/metrics.go:29), DLQ count, processing errors, dedupe hits (shared/o11y/metrics/kafka.go:26-72), fan-out duration, incomplete fan-outs, ranker fallback, hydration failures, page size (feed-service/internal/service/metrics.go:17-46). The six alert expressions use metric and label names that exist (values-prod.yaml:97-132 against those files). Lag: see E-7.
17. Logs are JSON on stdout with request_id from the incoming header: Architecture/shared/o11y/logging/logger.go:28; shared/middleware/requestid.go:22-49; access log shared/middleware/logger.go:12-48. (Outbound forwarding: E-12.)
18. graph-service's outbox relay leases rows with FOR UPDATE SKIP LOCKED: graph-service/internal/store/outbox_relay.go:26, 130.
19. Dependencies are vendored and images build with -mod=vendor: feed-service/Dockerfile:10, 20, 22.
20. Prod ApplicationSets have no automated sync and the chart refuses an empty image tag or a missing account id (as documented; not rendered here): deploy/argocd/applicationset.yaml:93-106; deploy/README.md:42-45, 118-121.
21. Pods run non-root with a read-only root filesystem and no capabilities: charts/atpost-service/templates/deployment.yaml:156-169.
22. Prod has 2 replicas, HPA and a PDB of minAvailable 1 for all four services: values-prod.yaml of feed (15-20, 86-88), post (16-21, 154-156), graph (15-20, 79-81), gateway (18-23, 192-194).
23. Scylla prod schema uses NetworkTopologyStrategy RF 3 and the feed reads at QUORUM, so one node may be down: Architecture/docker/scylla/schema.prod.cql:2; feed-service/cmd/server/main.go:76; three racks of one member, infra/terraform/modules/scylla/main.tf:105-108.
24. Aurora has deletion protection and automated backups in the prod Terraform: infra/terraform/envs/prod/main.tf:119-120.
25. Clients show an error with a retry control when the feed fails: FeedScreen.kt:351-353; Feed.tsx:281-295.

## Not verified

- Chart rendering (`helm template`, `render_test.sh`): Helm absent, Docker not started. UNVERIFIED.
- Anything about a live cluster or AWS account: Terraform state, whether secrets exist, whether the QA account holds any of this. No AWS, kubectl or Terraform command was run. UNVERIFIED.
- GitHub branch protection (whether a failed check blocks a merge). Not read.
- The gateway's refusal of `/v1/feed/internal/debug` and the metricsgate logic: left to Track B.
- Whether the 572 modified vendor files differ in content or only in line endings.
- post-service `GetUnpublishedOutboxEvents` body (E-14) and `engagement.ToggleLike` plus the counter consumers (E-11).
- Graceful stop and restart safety of post-service's purge, schedule, draft and reconcile workers, and of feed-service's `cmd/backfill`: not read.
- feed-service behaviour with Redis fully down beyond the three paths cited (hydration cache, tombstone check, celeb cache).
- Scylla schema changes to existing tables: the schema job uses CREATE IF NOT EXISTS (deploy/jobs/scylla-schema/apply.sh:16); how an ALTER reaches an existing cluster was not checked.
- ECHO_ENABLED default in feed-service.
- Dev smoke (`scripts/smoke/dev-smoke.sh`) was not run: the rules forbid calling a deployed environment.

## Out of scope observations

- commerce-service integration suite failed on the two pushes before HEAD (run 43858523110) and passed on HEAD.
- The weekly Security workflow on `main` has failed in every job, including gitleaks, for at least three weeks; `main` is 1,058 commits behind the working branch.
- `build-web.yml` does not exist on the default branch (gh: "workflow build-web.yml not found on the default branch"), so it cannot be dispatched from there.
- The api-gateway process has no graceful shutdown; this affects every module, not only the feed (E-8).
- Alert rules are missing for all other services as well (E-3 counts one values file with rules across deploy/services).
- The dev box has no database backups; it is the only environment holding data today.
