# Service alerts: where they go, how to prove they arrive, what each one means

**Status: UNTESTED.** Written 16 November 2026. The rules, the Alertmanager route and the IAM role are configuration that has never been applied or rendered by Helm. Part A is the proof; do it on QA and fill in the record at the end.

How it works:

- Every service deployed with `charts/atpost-service` gets a `PrometheusRule` with default alerts (pods, readiness, scrape target, 5xx ratio, p99 latency). feed-service adds its own (stalled consumer, lag, dead letters, fan-out, hydration, latency).
- Every one of those alerts carries the label `source=atpost-service`. Alertmanager sends that label to the receiver `ops-email`, which publishes to the SNS topic `atpost-<env>-ops-alerts`, which e-mails the ops address (`ops_alert_email` in the tfvars).
- Severity `critical` or `page`: one e-mail when it fires, a reminder every 2 hours, one when it resolves. Other severities (`warning`, `ticket`): reminder every 24 hours.
- Alerts that come with the monitoring stack itself (nodes, disks, Kubernetes) are NOT e-mailed; they are visible in the Alertmanager UI only (`route_platform_critical` in `infra/terraform/modules/observability`).

Configured in: `charts/atpost-service/templates/prometheusrule.yaml` and `values.yaml` (`defaultAlerts`), `deploy/services/feed-service/values-<env>.yaml` (`prometheusRule`), `infra/terraform/modules/observability/alerting.tf`.

Run in Git Bash. `ENV` is `qa` or `prod`.

## Part A. Prove an alert reaches the inbox (after the Terraform apply and the first sync)

1. Set the variables.
   ```bash
   export ENV=qa
   aws eks update-kubeconfig --name atpost-$ENV --region ap-south-1
   export AM=$(kubectl -n observability get pods -l app.kubernetes.io/name=alertmanager -o name | head -1)
   ```
2. Check the e-mail subscription is confirmed.
   ```bash
   cd infra/terraform/envs/$ENV
   aws sns list-subscriptions-by-topic --topic-arn "$(terraform output -raw ops_alerts_topic_arn)" \
     --query 'Subscriptions[].SubscriptionArn' --output text
   cd -
   ```
   Expected: an ARN starting `arn:aws:sns:`. If it prints `PendingConfirmation`, open the e-mail "AWS Notification - Subscription Confirmation" in the ops inbox and click the link. Nothing is delivered until then.
3. Check Alertmanager has the role and the route.
   ```bash
   kubectl -n observability get sa alertmanager -o jsonpath='{.metadata.annotations.eks\.amazonaws\.com/role-arn}'; echo
   kubectl -n observability exec "$AM" -c alertmanager -- amtool config routes show --alertmanager.url=http://localhost:9093
   ```
   Expected: `arn:aws:iam::<account>:role/atpost-qa-alertmanager-irsa`, and a route tree whose branches name `ops-email` with `source="atpost-service"`.
4. Send a test alert through the route.
   ```bash
   kubectl -n observability exec "$AM" -c alertmanager -- amtool alert add RouteTest \
     source=atpost-service severity=critical service=route-test \
     --annotation=summary="Route test, ignore" --annotation=description="Sent by hand from docs/runbooks/service-alerts.md" \
     --alertmanager.url=http://localhost:9093
   date -u +%FT%TZ
   ```
   Expected: within 2 minutes an e-mail with the subject `[FIRING:1] RouteTest route-test (atpost-qa)`, and about 5 minutes later `[RESOLVED] RouteTest route-test (atpost-qa)`.
5. If no e-mail arrives, read why.
   ```bash
   kubectl -n observability logs "$AM" -c alertmanager --since=10m | grep -iE 'sns|notify|error' | tail -20
   ```
   `AccessDenied` or `no EC2 IMDS role found`: the role in step 3 is missing or the pod started before it existed (`kubectl -n observability delete "$AM"`, it comes back). `InvalidParameter: TopicArn`: the topic output is wrong.
6. Check every service has its rules and Prometheus loaded them.
   ```bash
   kubectl -n atpost get prometheusrule
   kubectl -n observability port-forward svc/prometheus-operated 9090 > /dev/null &
   sleep 3
   curl -s localhost:9090/api/v1/rules | grep -o '"name":"[a-z0-9-]*\.default"' | sort -u | wc -l
   curl -s localhost:9090/api/v1/rules | grep -o '"health":"[a-z]*"' | sort | uniq -c
   kill %1
   ```
   Expected: one `PrometheusRule` per service; the first number equals the number of services; every rule's health is `ok` (none `err`).
7. The acceptance test: take one service down and wait for the e-mail.
   ```bash
   kubectl -n atpost scale deploy/graph-service --replicas=0
   date -u +%FT%TZ
   ```
   Expected: within 10 minutes an e-mail `[FIRING:1] ServiceNoReadyPod graph-service (atpost-qa)` (the rule waits 3 minutes). Feed alerts may follow. Then bring it back and wait for the resolved e-mail:
   ```bash
   kubectl -n atpost scale deploy/graph-service --replicas=1
   kubectl -n atpost rollout status deploy/graph-service --timeout=5m
   ```
8. Fill in the record below.

## Part B. What each alert means and the first thing to run

| Alert | Severity | It means | First command |
|---|---|---|---|
| `ServicePodCrashLooping` | critical | A pod exits right after starting, for 5 minutes | `kubectl -n atpost logs <pod> --previous` |
| `ServicePodRestarting` | warning | One pod restarted more than 3 times in 30 minutes | `kubectl -n atpost describe pod <pod>` (last state, exit reason) |
| `ServiceNoReadyPod` | critical | No ready pod for 3 minutes: the service is down | `kubectl -n atpost get pods -l app.kubernetes.io/name=<service>` |
| `ServiceReadinessFailing` | warning | Fewer ready pods than wanted for 15 minutes | same, then `kubectl -n atpost describe pod <pod>` |
| `ServiceMetricsTargetDown` | warning | Prometheus cannot scrape `/metrics`; the service's other alerts are blind | Prometheus UI, Status, Targets |
| `ServiceHttp5xxRatioHigh` | critical | More than 5% of requests answered 5xx for 10 minutes | `kubectl -n atpost logs deploy/<service> --since=15m \| grep -i error` |
| `ServiceHttpP99LatencyHigh` | warning | p99 above 1 s for 15 minutes | Grafana or Prometheus: p99 by `path` (the alert text has the query) |
| `FeedConsumerStalled` | critical | A feed consumer retried an event for 5 minutes with no success: a dependency is down, feeds are not updating | `kubectl -n atpost logs deploy/feed-service --since=10m \| grep 'retrying message' \| tail -5` |
| `FeedConsumerLagHigh` / `FeedConsumerLagCritical` | warning / critical | A feed consumer is more than 1,000 events behind for 10 / 30 minutes | same, and the fan-out duration panel |
| `FeedEventsDeadLettered` | ticket | An event that can never succeed was parked on `social.events.v1.dlq` | read the `x-dlq-error` header of the parked message |
| `FeedFanoutIncomplete`, `FeedRankerFallingBack`, `FeedHydrationFailing`, `FeedHomeLatencyHigh` | ticket / page | See the alert text | `kubectl -n atpost logs deploy/feed-service --since=15m` |

To roll a bad release back: `docs/runbooks/service-rollback.md`.

## Part C. Known limits (16 November 2026)

1. The lag number (`atpost_feed_service_kafka_consumer_lag`) is measured inside the consumer from what it has fetched. When the consumer is completely stuck it stops fetching after 100 queued messages and the number stops growing. `FeedConsumerStalled` covers that case from the error and success counters. A lag number measured from the broker (committed offset against the end of the topic) does not exist yet.
2. `FeedEventRetryStuck` never fires: it waits for a metric the Go code does not export yet (`atpost_feed_service_kafka_message_retry_age_seconds`).
3. A consumer that hangs without returning an error (no failed attempt, no success) is seen by neither feed rule. `ServiceReadinessFailing` and the home-feed latency rule are the only signals.
4. api-gateway, identity-auth/profile/user-service and chat-call/message-service, chat-ws-gateway record no HTTP request metrics, so they have no 5xx or p99 alert. The six identity and chat services have no `/metrics` endpoint at all.
5. E-mail is the only channel. Nothing wakes anyone at night.

## Drill record (copy this block for each run of Part A)

```
Alert route drill
Date (UTC):                              
Environment:                             qa
Run by:                                  
Subscription confirmed:                  yes / no
Route test sent (UTC):                   
FIRING e-mail received (UTC):            
RESOLVED e-mail received (UTC):          
PrometheusRule objects / services:       
Rules with health other than ok:         
graph-service scaled to 0 (UTC):         
ServiceNoReadyPod e-mail received (UTC): 
Minutes from scale-down to e-mail:       
Commands that had to be changed:         
Result:                                  PASS / FAIL
Follow-ups:                              
```
