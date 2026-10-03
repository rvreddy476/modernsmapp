# deploy/ — production values, ArgoCD and one-shot jobs (AWS, ap-south-1)

Everything here is rendered by ONE Helm chart, `charts/atpost-service`, and
delivered by ArgoCD from this repository (`https://github.com/rvreddy476/modernsmapp.git`).
Nothing in this directory holds a secret, an AWS account id, or a resource name
with a random suffix: those arrive at deploy time (sections 2–4).

Plan: `docs/runbooks/aws-first-production-deployment.md` (W4 chart and ArgoCD,
W6 web, W8 live). Terraform side: `infra/terraform/envs/prod/README.md`.

## 1. Layout

| Path | What |
|---|---|
| `services/<svc>/values-prod.yaml` | the 32 backend deployables (31 + `food-service`), one ArgoCD Application each (`prod-<svc>`) |
| `services/<svc>/values-staging.yaml`, `values-azure-*.yaml` | kept, not extended (only production is built); staging is not applied anywhere |
| `web/app/values-prod.yaml` | postbook-ui, image `atpost/web`, `app.cleestudio.com` + bare `cleestudio.com` (301 to `app.`) |
| `web/admin-console/values-prod.yaml` | atpost-web-ui `apps/admin`, image `atpost/admin-console`, `admin.cleestudio.com` |
| `parked/web-zones/` | the eleven retired micro-frontend zones, parked OUTSIDE `web/` so no ApplicationSet matches them |
| `argocd/applicationset.yaml` | `atpost-services-prod` and `atpost-web-prod`, MANUAL sync, branch `release/prod` |
| `argocd/applicationset-staging.yaml` | staging ApplicationSets, split out so Terraform never applies them to the production cluster |
| `argocd/repo-credentials.yaml` | ExternalSecret → ArgoCD repository Secret (token from `atpost/prod/argocd-repo-modernsmapp`) |
| `argocd/apply-prod-applicationsets.sh` | applies the prod ApplicationSets by hand, filling the account id |
| `jobs/scylla-schema/` | one-shot Job applying the production Scylla keyspaces and tables |
| `fill-tf-outputs.sh` | replaces every `__TF_*__` placeholder with its Terraform output |

How values reach a pod: literal, non-secret settings are in `env:`; every
connection string and credential (DSN, Kafka brokers + SCRAM user, Redis
address + AUTH token, OpenSearch URL + login, JWT/service keys, third-party
keys) is a key of the service's Secrets Manager secret `atpost/prod/<svc>`,
listed under `externalSecret.data` and filled by the seeder
(`scripts/prodsecrets.sh`, `tools/prodsecrets/manifest.yaml`). Kubernetes gives
`env` precedence over `envFrom`, so a key is in one place or the other, never
both (the seeder's drift test checks every `remoteRef` exists in its manifest:
`cd tools/prodsecrets && go test ./...`).

## 2. AWS account id

Never committed. The chart refuses to render unless `global.awsAccountId` is
exactly 12 digits; image repositories and IRSA role ARNs are built from it
(`<id>.dkr.ecr.ap-south-1.amazonaws.com/atpost/<svc>`,
`arn:aws:iam::<id>:role/atpost-prod-<svc>-irsa`, the names
`service_irsa_role_arns` outputs).

`argocd/applicationset.yaml` carries the Terraform template token
`aws_account_id` (dollar-brace form) in each list generator, and passes it as
the Helm parameter `global.awsAccountId`. Two ways to fill it:

1. **Terraform** (done): module `argocd` reads this file with
   `templatefile(..., { aws_account_id = var.aws_account_id })`, and `envs/prod/main.tf`
   passes `data.aws_caller_identity.current.account_id`.
2. **By hand** (only if Terraform does not manage the ApplicationSets):
   ```bash
   deploy/argocd/apply-prod-applicationsets.sh <12-digit account id> --dry-run
   deploy/argocd/apply-prod-applicationsets.sh <12-digit account id>
   ```

The AppProject `atpost` is owned by Terraform: module `argocd` in `envs/prod/main.tf`?
sets `allowed_source_repos = ["https://github.com/rvreddy476/modernsmapp.git"]` and?
substitutes the account id into `applicationset.yaml` with `templatefile`.

## 3. Placeholders → Terraform outputs

A value that only Terraform knows (a bucket name with a random suffix, an ARN)
is written `__TF_<OUTPUT_NAME>__` with a `# from terraform output <name>`
comment. The rule is mechanical (upper-case output name), and the chart
REFUSES to render while any placeholder is left, naming it. Fill them all on
`release/prod`:

```bash
cd infra/terraform/envs/prod && terraform output   # pass 2 applied, AWS_PROFILE set
cd ../../../..
deploy/fill-tf-outputs.sh --check                   # what is left
deploy/fill-tf-outputs.sh                           # rewrite in place
git diff deploy/                                    # review, then commit to release/prod
```

| Placeholder | Terraform output | Where |
|---|---|---|
| `__TF_MEDIA_BUCKET_NAME__` | `media_bucket_name` | media-service `S3_BUCKET` (server + worker) |
| `__TF_LIVE_RECORDINGS_BUCKET_NAME__` | `live_recordings_bucket_name` | live-service-v2 `MINIO_BUCKET_LIVE_RECORDINGS`; media-service `MEDIA_LIVE_RECORDINGS_BUCKET` |
| `__TF_COMMERCE_INVOICES_BUCKET_NAME__` | `commerce_invoices_bucket_name` | commerce-service `COMMERCE_BLOB_BUCKET` |
| `__TF_FOOD_FILES_BUCKET_NAME__` | `food_files_bucket_name` | food-service `FOOD_BLOB_BUCKET` |
| `__TF_PUBLIC_EDGE_CERTIFICATE_ARN__` | `public_edge_certificate_arn` | ALB `certificate-arn`: api-gateway, chat-ws-gateway, payments webhook, web app, admin console |
| `__TF_WAF_WEB_ACL_ARN__` | `waf_web_acl_arn` | ALB `wafv2-acl-arn`: the same five ingresses |

Outputs that reach the pods through the SEEDER, not through values (so no
placeholder exists for them): `aurora_cluster_endpoint` + `aurora_master_secret_arn`
(one `postgres_dsn` per service), `msk_bootstrap_brokers` + `msk_scram_secret_name`
(`kafka_brokers`, `kafka_sasl_username/password`), `elasticache_primary_endpoint`
+ `elasticache_auth_secret_arn` (`redis_addr`, `redis_password`),
`opensearch_endpoint` + `opensearch_master_secret_arn` (search-service
`opensearch_url/username/password`), `media_cloudfront_key_pair_id` + the key in
`atpost/prod/media/cloudfront-signing` (media-service), `commerce_pii_kms_key_id`
(commerce `commerce_kms_key_id`), `service_secret_names` (the `remoteKey` of
every values file is `atpost/prod/<svc>`).

Deterministic values written literally (not outputs): hosts `api.`, `ws.`,
`app.`, `admin.`, `media.cleestudio.com`; SES configuration set
`atpost-prod-transactional` (= `ses_configuration_set_name`); the Scylla client
Service `atpost-prod-client.scylla.svc.cluster.local` (module scylla
`client_service`); the VPC CIDR `10.30.0.0/16` (envs/prod `module "vpc"`; no
output exists) for the payments NetworkPolicy and identity-auth `TRUSTED_PROXIES`.

**Not used: `waf_psp_webhook_acl_arn`.** The payments webhook Ingress joins the
API load balancer (one ALB per host, and Razorpay calls
`https://api.cleestudio.com/v1/payments/webhook`). An ALB takes ONE WAF ACL and
the load-balancer controller refuses to reconcile a group whose members name
different ACLs, so the webhook carries `waf_web_acl_arn` (render_test.sh
asserts they are equal). Put the PSP rate rule inside the web ACL scoped to
that path, or give the webhook its own host and ALB (`webhookIngress.groupName: ""`).

## 4. Image tags

Every `image.tag` (and `worker.image.tag` for media-service and dating-service)
is `""` with `# set by CI`; the chart refuses to render an empty tag and the
default is never `latest` (ECR repositories are immutable; CI pushes commit
shas). `scripts/ci/bump-service-tags.sh` writes them for services. The web tags
belong in `deploy/web/app/values-prod.yaml` and
`deploy/web/admin-console/values-prod.yaml` (build-web.yml must bump THOSE
paths; there is no `deploy/services/web`).

Web build arguments are baked into the images (see the headers of the two web
values files): postbook-ui `NEXT_PUBLIC_API_BASE_URL=""`,
`NEXT_PUBLIC_WS_BASE_URL=""`, `NEXT_PUBLIC_SITE_URL=https://app.cleestudio.com`,
`NEXT_PUBLIC_ENABLE_STUB_PAYMENTS=false`, `NEXT_PUBLIC_RAZORPAY_KEY_ID`; admin
console `ZONE=admin`, `ADMIN_BASE_PATH` for the host root.

## 5. Render locally

Docker only (Helm runs from `alpine/helm`):

```bash
charts/atpost-service/render_test.sh
```

It renders every `values-prod.yaml` (services + web) and every
`values-staging.yaml` with a test account id and tag, emulates both production
ApplicationSets (token substituted, each matched file rendered with the
parameter the template passes), and checks the refusals (no/fake/templated
account id, empty tags, unfilled placeholders), the optional templates, ports,
the webhook/ALB/WAF wiring and the web hosts. One service by hand:

```bash
MSYS_NO_PATHCONV=1 docker run --rm -v "$(pwd -W 2>/dev/null || pwd):/wk" -w /wk alpine/helm:latest \
  template prod-graph-service ./charts/atpost-service -f deploy/services/graph-service/values-prod.yaml \
  --set-string global.awsAccountId=111122223333 --set-string image.tag=test \
  --set global.allowPlaceholders=true   # local only: lets an unfilled __TF_*__ render
```

## 6. Scylla schema (once, before the Scylla-backed services)

The production CQL lives next to its owners —
`Architecture/docker/scylla/schema.prod.cql` and
`chat-service/services/message-service/scylla/schema.prod.cql`
(NetworkTopologyStrategy, replication 3). A chart cannot read files outside its
directory, so the ConfigMap is built by kubectl and a Job applies it with
cqlsh:

```bash
kubectl -n scylla get scyllacluster                 # Ready after Terraform pass 2
deploy/jobs/scylla-schema/apply.sh --dry-run
deploy/jobs/scylla-schema/apply.sh                  # builds ConfigMap scylla-schema-prod, runs the Job, waits
```

Re-running is safe (`IF NOT EXISTS` everywhere).

## 7. Before the first sync — checklist

1. Terraform pass 2 applied; `release/prod` created from `main`.
2. Account id wired (section 2) and the AppProject owner chosen; repo credential
   secret `atpost/prod/argocd-repo-modernsmapp` filled, then
   `kubectl apply -f deploy/argocd/repo-credentials.yaml`.
3. Seeder run (`scripts/prodsecrets.sh`): every `atpost/prod/<svc>` filled;
   `kubectl -n atpost get externalsecret` all `SecretSynced`. Keys that keep a
   service down when left empty: `food_platform_gstin` (food refuses to boot in
   production without it), `mopedu_platform_gstin` (rider, same),
   `live_egress_s3_access_key_id/secret` (live recordings), Razorpay keys
   (payments refuses to boot without the webhook secret).
4. `deploy/fill-tf-outputs.sh` run and committed on `release/prod`.
5. CI has written real image tags for every service and both web apps.
6. Scylla schema applied (section 6).
7. Sync in the plan's order (runbook section 6): identity-auth, identity-user,
   identity-profile, api-gateway, user, graph, web; then the rest.
8. After the founder registers: set `SUPERADMIN_USER_IDS` (identity-auth) and,
   when chosen, `LIVE_PILOT_USER_IDS` (live-service-v2); sync those two again.

Deliberate launch settings worth knowing (each explained in its file):
commerce's PreSync migration Job is OFF (the image does not contain the
migrator yet; the server migrates at boot); the gateway closes
`/v1/memories,/v1/flags,/v1/admin/flags,/v1/reviewer` plus `/v1/wallet,/v1/billpay`
(no production provider, no gate of their own); calls off, payouts off,
communities/dating/rider/reviewer closed, live pilot-only, OAuth sign-up off,
no `OTP_BYPASS_CODE`, no `PAYMENTS_ALLOW_STUB`.
