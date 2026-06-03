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

- **EKS cluster + node groups** — landed (Phase 2, see `modules/eks/`).
  Cluster 1.31, IRSA enabled, three node groups (general / memory /
  system), EBS CSI + VPC CNI add-ons via IRSA.
- **Aurora PostgreSQL Multi-AZ** — landed (Phase 2, see
  `modules/aurora/`). Aurora PG 16.4, writer + reader in prod, single
  writer in staging. KMS-encrypted, IAM database auth enabled,
  Performance Insights on, master in Secrets Manager. Logical DBs
  (`app`, `identity_db`, `chat_db`, `commerce_db`, `feed_db`) are
  created by a post-create kubernetes Job — pending follow-up.
- **MSK Serverless** — landed (Phase 2, see `modules/msk/`). IAM-auth
  only (port 9098). Shared client IAM policy exported as
  `msk_client_iam_policy_arn` — attach to every service's IRSA role
  to grant produce/consume. Topics are managed explicitly per service
  (not auto-create) so partitions + retention are reviewable in PR.
- **OpenSearch Service** — Phase 2.
- **S3 buckets + CloudFront** — Phase 2.
- **WAF + Shield** — Phase 5 (hardening).
- **Helm umbrella chart + ArgoCD** — Phase 3.
- **Secrets Manager + External Secrets Operator** — Phase 2, after EKS.
- **AWS Backup + Config + Security Hub + GuardDuty** — Phase 5.

See `~/.claude/plans/dazzling-weaving-hartmanis.md` §4 for the phasing.

## Open execution decisions before extending

Five items from the plan need answers before Phase 2:

1. Aurora PostgreSQL **Multi-AZ** vs RDS Postgres Multi-AZ.
2. MSK **Provisioned (3 brokers)** vs MSK Serverless.
3. **Scylla on EKS** vs DynamoDB hot-path migration (recommendation: stay).
4. **ArgoCD** vs Flux CD.
5. CDN tier: **CloudFront only** vs CloudFront-behind-Cloudflare.

Don't extend Phase 2 modules without picking these.
