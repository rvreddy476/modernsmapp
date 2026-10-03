# AtPost AWS Infrastructure (Terraform)

Phase-0 scaffold for the AWS migration described in
`~/.claude/plans/dazzling-weaving-hartmanis.md`. This directory is the
canonical source of truth for AWS infrastructure — no console clicks.

## What's in scope right now

This scaffold provisions the **foundations only**. EKS, RDS, MSK, OpenSearch,
ElastiCache, S3/CloudFront etc. land in Phase 2 (see the roadmap). Phase 0
is what you need before any of that is useful:

- **Remote state** — S3 bucket + DynamoDB lock table for Terraform state.
- **VPC** — 3-AZ multi-tier network (public / private / isolated) in
  ap-south-1, with NAT gateways and VPC endpoints for S3 + ECR.
- **ECR repos** — one private repo per Architecture/ + identity-platform /
  chat-service component. Immutable tags. CRITICAL/HIGH CVE scan on push.
- **IAM Identity Center (SSO) + GitHub OIDC** — human and CI access without
  long-lived access keys.
- **Route 53 zone + ACM certificates** — for the `cleestudio.com` zone the
  product already owns at Cloudflare. Plan: Cloudflare stays as the public
  DNS edge; Route 53 holds AWS-specific records (`*.aws.cleestudio.com`).

## Directory layout

```
infra/terraform/
  README.md             this file
  versions.tf           Terraform + AWS provider pins + backend stub
  modules/
    vpc/                3-AZ VPC + NAT + VPC endpoints
    ecr/                one private repo per service
    iam/                Identity Center + GitHub OIDC + base roles
    dns/                Route 53 zone + ACM cert(s)
  envs/
    staging/            composes modules with staging values
    prod/               composes modules with prod values
  bootstrap/
    main.tf             standalone — provisions the S3+DynamoDB backend.
                        Run once per account, manually, before the rest.
```

## First-time bootstrap

Terraform's state bucket is a chicken-and-egg problem: you need a bucket
before you can store state remotely, but creating that bucket via
Terraform itself would write its own state to the very bucket it's
creating. Standard pattern: a tiny `bootstrap/` module is run once,
locally, with state stored on disk. Output is the bucket/lock table
that every other workspace references.

```bash
cd bootstrap
terraform init
terraform apply
```

Then update each `envs/*/main.tf` to reference the outputs.

## Per-environment apply

Each env is a separate Terraform workspace with isolated state:

```bash
cd envs/staging
terraform init        # reads backend.tf -> S3 state, DynamoDB lock
terraform plan -var-file=staging.tfvars
terraform apply -var-file=staging.tfvars
```

`prod` is intentionally identical so a misconfigured staging change is
hard to ship blindly — copy the diff, don't divergent-edit.

### Two-apply bootstrap

The first `terraform apply` against a new environment will FAIL on the
helm_release / kubernetes_manifest resources because the
`kubernetes` + `helm` providers can't authenticate against an EKS
cluster that doesn't exist yet. This is expected:

1. First `apply`: creates AWS infra (VPC, EKS, Aurora, MSK,
   ElastiCache, OpenSearch, S3/CloudFront). Cluster-tooling resources
   fail with `connection refused` / `Unauthorized` — ignore.
2. Second `apply` (re-run the same command): the EKS cluster now
   responds, so kubernetes + helm authenticate and the cluster
   tooling (External Secrets Operator, ALB Controller, ArgoCD,
   Scylla Operator) installs cleanly.

This is the standard pattern for "Terraform manages both AWS and the
kubernetes resources inside it". Targeted applies are an alternative
(`terraform apply -target=module.eks` then full apply) but the
two-pass approach is simpler and harder to misuse.

## Conventions

- **Region**: `ap-south-1` (Mumbai) — India DPDP compliance, decision
  recorded in the plan §1. Don't add multi-region until the data
  residency story is re-examined.
- **AZs**: `ap-south-1a`, `ap-south-1b`, `ap-south-1c`. Three because
  ElastiCache Multi-AZ + MSK require three to tolerate one AZ loss.
- **Naming**: `atpost-<env>-<resource>-<purpose>` (e.g.
  `atpost-prod-vpc-main`, `atpost-prod-ecr-post-service`).
- **Tags**: every taggable resource carries `Environment`, `Service`,
  `ManagedBy=terraform`, and `Project=atpost`. Cost allocation depends
  on these — don't skip them.

## What's NOT in this scaffold (yet)

> 3 Oct 2026 — the prod environment was reshaped for the first production
> deployment (docs/runbooks/aws-first-production-deployment.md, workstreams
> W1 + W2). `envs/prod/README.md` has the exact first-apply sequence; the
> bullets below describe the modules after that change.

- **EKS cluster + node groups** — landed (Phase 2, see `modules/eks/`).
  Version is a variable (staging default 1.31; prod 1.36 — the newest
  version every pinned add-on supports; Karpenter 1.14.1 LTS in prod),
  IRSA enabled, three node groups (general / memory / system) with
  instance types and counts as variables, EBS CSI + VPC CNI add-ons via
  IRSA.
- **Aurora PostgreSQL** — landed (Phase 2, see `modules/aurora/`).
  Aurora PG 16.15 (16.4 was deprecated by AWS). Prod lean: Serverless v2
  0.5–4 ACU, single instance; `serverless_enabled=false` gives the
  provisioned writer(+reader) shape. KMS-encrypted, IAM database auth
  enabled, Performance Insights on, master in Secrets Manager. Logical DBs
  and extensions are created by `modules/aurora-bootstrap/` (prod: `app`,
  `identity_db`, `chat_db`, `call_db`, `commerce_db` with postgis,
  pg_trgm, pgcrypto).
- **MSK Provisioned** — see `modules/msk/`. The services' Kafka client
  speaks PLAIN/SCRAM only, so the Serverless (IAM-only) cluster was
  replaced by Provisioned brokers (prod lean: 2 × kafka.t3.small, Kafka
  3.9.x) with SASL/SCRAM-SHA-512 over TLS on 9096; the SCRAM user lives in
  the AWS-mandated `AmazonMSK_*` secret. `auto.create.topics.enable` is
  off; `modules/msk/topics-job/` is the one-shot Kubernetes Job that
  creates every topic (the AWS provider has no topic resource). IAM auth
  (and the old client IAM policy) is behind `enable_iam_auth` — on in
  staging, off in prod.
- **ElastiCache Valkey** — landed (Phase 2, see `modules/elasticache/`).
  Engine version is a variable (staging 7.2, prod 8.1); prod lean is
  1 primary + 1 replica `cache.t4g.small`. Multi-AZ failover on,
  encryption at rest (KMS) + in transit (TLS) + AUTH token in Secrets
  Manager. Cluster-mode-disabled.
- **OpenSearch Service** — landed (Phase 2, see `modules/opensearch/`).
  OpenSearch 2.19 (variable). Prod lean: one `t3.small.search` node,
  single AZ, no dedicated masters (zone awareness and Auto-Tune switch
  themselves off for 1-node / burstable shapes). KMS-encrypted,
  node-to-node encryption on, fine-grained access control with an
  internal master user (search-service uses basic auth), VPC-only.
- **S3 media bucket + CloudFront** — landed (Phase 2, see
  `modules/media/`). KMS-encrypted bucket (key policy lets the
  distribution's OAC decrypt), versioning + lifecycle, OAC-only access.
  `public/*` is served unsigned; every other path requires a CloudFront
  signed URL from the key group the module creates (signing key pair in
  `atpost/<env>/media/cloudfront-signing`). Custom domain
  `media.cleestudio.com` + us-east-1 certificate are wired in prod behind
  `media_custom_domain_enabled`.
- **Service buckets** — `modules/private-bucket/`: live-recordings,
  commerce-invoices, food-files (prod), each with read/write and
  read-only client policies.
- **Certificates, SES, secrets, ops** (prod only) — `modules/public-cert/`
  (ACM, Cloudflare-validated; outputs the records), `modules/ses/`
  (domain identity + DKIM + MAIL FROM + configuration set + bounce events
  to SNS), `modules/service-secrets/` (empty `atpost/prod/<name>` shells
  the seeder fills), `modules/ops-baseline/` (ops SNS topic → email, AWS
  Backup for Aurora, CloudTrail, GuardDuty, Budgets),
  `modules/metrics-server/`.
- **WAF** — `modules/waf/`, regional ACL per name: `edge` for the API/web
  ALBs and `psp-webhook` for the payments webhook ingress. Shield
  Advanced not enabled.
- **ArgoCD** — landed (Phase 2, see `modules/argocd/`). HA install
  (2 replicas of every component), ALB Ingress with cert from the
  dns module, admin password mirrored to Secrets Manager, AppProject
  "atpost" defined. Per-service ApplicationSet pending the umbrella
  Helm chart.
- **External Secrets Operator** — landed (Phase 2, see
  `modules/external-secrets/`). ClusterSecretStore
  "aws-secrets-manager" exposed; IRSA-bound IAM role permits read
  on atpost/${env}/* + decrypt on the four data-plane KMS keys.
- **AWS Load Balancer Controller** — landed (Phase 2, see
  `modules/aws-lb-controller/`). Required for any Ingress to
  resolve to an ALB.
- **Scylla on EKS** — landed (Phase 2, see `modules/scylla/`).
  Scylla Operator + ScyllaCluster CR, 3 replicas across AZs on the
  memory node group with the workload=scylla:NoSchedule taint.
  gp3 made the cluster-default StorageClass.
- **Aurora bootstrap Job** — landed (Phase 2, see
  `modules/aurora-bootstrap/`). One-shot kubernetes Job that
  CREATEs the logical databases (prod: app, identity_db, chat_db,
  call_db, commerce_db + postgis, pg_trgm, pgcrypto) once Aurora + ESO are ready. Idempotent;
  re-runs on database-list changes.
- **Helm umbrella chart + per-service Applications** — Phase 3.
- **Secrets Manager + External Secrets Operator** — Phase 2, after EKS.
- **AWS Backup + CloudTrail + GuardDuty + Budgets** — prod via `modules/ops-baseline/` (3 Oct 2026); Config + Security Hub still Phase 5.

See `~/.claude/plans/dazzling-weaving-hartmanis.md` §4 for the phasing.

## Open execution decisions before extending

Five items from the plan need answers before Phase 2:

1. Aurora PostgreSQL **Multi-AZ** vs RDS Postgres Multi-AZ.
2. ~~MSK Provisioned vs Serverless~~ — decided 3 Oct 2026: Provisioned with SCRAM (the client cannot do IAM auth).
3. **Scylla on EKS** vs DynamoDB hot-path migration (recommendation: stay).
4. **ArgoCD** vs Flux CD.
5. CDN tier: **CloudFront only** vs CloudFront-behind-Cloudflare.

Don't extend Phase 2 modules without picking these.
