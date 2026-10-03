# envs/prod — first apply, step by step

Lean profile (plan §2.1, contract 3 Oct 2026). Nothing here has been applied
yet; the sequence below is the one to follow the first time. Read
`../../README.md` for the module layout and the two-pass explanation.

## 0. Before anything

1. Sign in with the Identity Center deployment user:
   `aws configure sso` then `aws sso login --profile atpost-prod`; export
   `AWS_PROFILE=atpost-prod` and check `aws sts get-caller-identity`.
2. Console-only requests that take time (do them first, then continue):
   - **EC2 quota**: Service Quotas → Amazon EC2 → *Running On-Demand Standard
     (A, C, D, H, I, M, R, T, Z) instances* → request **64 vCPUs** in
     ap-south-1. The lean profile needs 28 (3×m7g.xlarge + 2×m7g.medium +
     3×m7g.large + Karpenter headroom); a new account usually has 5–8.
   - **SES production access**: SES → *Account dashboard* → *Request
     production access* (use case: transactional account email, expected
     volume, bounce handling = SNS → email). Until approved SES is sandboxed:
     200 mails/day and only to addresses verified by hand.
3. `cp prod.tfvars.example prod.tfvars` and fill every `CHANGEME`
   (`prod.tfvars` is gitignored).
4. Terraform ≥ 1.6 and `kubectl` on the machine. `terraform init -backend=false
   && terraform validate` here must pass before touching the account.

## 1. Bootstrap (state bucket + lock table, once per account)

```bash
cd ../../bootstrap
terraform init
terraform apply -var state_bucket_name=atpost-tfstate-<account-id>
terraform output            # state_bucket, lock_table
```

Put the two ARNs into `prod.tfvars` (`tfstate_bucket_arn`,
`tfstate_lock_table_arn`). `backend.tf` here is intentionally partial — pass
the backend at init:

```bash
cd ../envs/prod
terraform init \
  -backend-config="bucket=atpost-tfstate-<account-id>" \
  -backend-config="dynamodb_table=atpost-tfstate-locks" \
  -backend-config="key=envs/prod/terraform.tfstate" \
  -backend-config="region=ap-south-1" \
  -backend-config="encrypt=true"
```

## 2. Pass 1 — AWS resources only (no Kubernetes/Helm)

The kubernetes and helm providers cannot authenticate against a cluster that
does not exist, so the first apply targets the AWS-side modules. Takes
30–45 minutes (EKS ≈ 15 min, MSK ≈ 25 min, OpenSearch ≈ 15 min, Aurora ≈ 10
min, all in parallel).

```bash
terraform plan -var-file=prod.tfvars -out=pass1.plan \
  -target=module.vpc -target=module.ecr -target=module.ecr_web \
  -target=module.dns -target=module.iam -target=module.eks \
  -target=module.aurora -target=module.msk -target=module.elasticache \
  -target=module.opensearch -target=module.media \
  -target=module.waf -target=module.waf_psp_webhook \
  -target=module.codeartifact -target=module.auth_keys \
  -target=module.service_irsa -target=module.commerce_pii_kms \
  -target=module.public_edge_cert -target=module.media_cdn_cert \
  -target=module.bucket_live_recordings -target=module.bucket_commerce_invoices \
  -target=module.bucket_food_files -target=module.service_secrets \
  -target=module.ses -target=module.ops \
  -target=aws_iam_policy.rekognition -target=aws_iam_policy.livekit_egress \
  -target=aws_iam_role_policy_attachment.ci_codeartifact \
  -target=aws_iam_role_policy_attachment.commerce_pii_kms
terraform apply pass1.plan
```

Expected: roughly 350–400 resources added, 0 changed, 0 destroyed (the exact
number depends on the service list; EKS alone is ~60).

Right after pass 1:

1. **Confirm the SNS subscription** — the ops address gets an email
   "AWS Notification - Subscription Confirmation"; click it.
2. **Add the Cloudflare records**:
   ```bash
   terraform output -json cloudflare_dns_records \
     | jq -r '.[] | [.purpose, .name, .type, .value] | @tsv'
   ```
   All DNS-only (grey cloud). The NS row lists four servers — one NS record
   each. Skip the `media CDN host` row for now (step 4).
3. **Wait for both certificates to say ISSUED** (usually < 10 min after the
   CNAMEs resolve):
   ```bash
   aws acm describe-certificate --certificate-arn "$(terraform output -raw public_edge_certificate_arn)" --query Certificate.Status
   aws acm describe-certificate --region us-east-1 --certificate-arn "$(terraform output -raw media_cdn_certificate_arn)" --query Certificate.Status
   ```
4. Set `media_custom_domain_enabled = true` in `prod.tfvars`, re-run the
   pass-1 plan/apply (only the distribution changes), then add the
   `media CDN host` CNAME at Cloudflare.
5. `aws eks update-kubeconfig --name atpost-prod --region ap-south-1` — the
   principal that ran pass 1 is cluster admin (access entry).

## 3. Pass 2 — everything (in-cluster tooling, databases, topics)

```bash
terraform plan -var-file=prod.tfvars -out=pass2.plan
terraform apply pass2.plan
```

Installs External Secrets, the ALB controller, metrics-server, Scylla
operator + cluster, ArgoCD, the observability stack, Karpenter, and runs two
one-shot Jobs: `aurora-bootstrap` (the five databases with postgis, pg_trgm,
pgcrypto) and `msk-topics` (every Kafka topic, 6 partitions, RF 2). Both
Jobs wait for completion; if `msk-topics` times out, `kubectl -n
msk-bootstrap logs job/msk-topics` says why (almost always: External
Secrets had not mirrored `msk-scram` yet — re-apply).

Run pass 2 with the SAME principal as pass 1 (or one listed in
`cluster_admin_arns`), otherwise the kubernetes provider gets 401.

## 4. Outputs to copy

| Output | Goes to |
|---|---|
| `service_irsa_role_arns` | `serviceAccount.irsaRoleArn` per service values (W4) |
| `media_bucket_name` | media-service + worker `S3_BUCKET` (replaces the hard-coded `atpost-prod-media`) |
| `media_cdn_base_url`, `media_cloudfront_key_pair_id`, `media_cloudfront_signing_secret_arn` | the seeder copies key pair id + private key into `atpost/prod/media-service` |
| `live_recordings_bucket_name`, `commerce_invoices_bucket_name`, `food_files_bucket_name` | live-service-v2 / commerce / food values |
| `msk_bootstrap_brokers`, `msk_scram_secret_arn` | `KAFKA_BROKERS` + SCRAM user/password into every service secret (seeder); services set `KAFKA_SASL_MECHANISM=SCRAM-SHA-512`, `KAFKA_TLS_ENABLED=true` |
| `aurora_cluster_endpoint`, `aurora_master_secret_arn` | the seeder builds one DSN per service |
| `elasticache_primary_endpoint`, `elasticache_auth_secret_arn` | `REDIS_ADDR` / `REDIS_PASSWORD`, `REDIS_TLS_ENABLED=true` |
| `opensearch_endpoint`, `opensearch_master_secret_arn` | search-service `OPENSEARCH_URL` + basic auth |
| `public_edge_certificate_arn` | `alb.ingress.kubernetes.io/certificate-arn` on api-gateway, chat-ws-gateway, web, admin |
| `waf_web_acl_arn`, `waf_psp_webhook_acl_arn` | api-gateway ingress annotation; payments `webhookIngress.wafAclArn` (note: a WAF ARN ends in `/<name>/<id>` — the values template that builds the ARN from the account id alone must take the output instead) |
| `commerce_pii_kms_key_id` | commerce `COMMERCE_KMS_KEY_ID` |
| `ses_configuration_set_name` | identity-auth `SES_CONFIGURATION_SET` (already `atpost-prod-transactional`) |
| `ci_role_arn`, `terraform_apply_role_arn` | GitHub repository secrets `AWS_CI_ROLE_ARN`, `AWS_TERRAFORM_ROLE_ARN` (+ state bucket / lock table names) |
| `ecr_repository_urls` | CI image names |

After the Ingresses exist (first ArgoCD sync): `kubectl -n atpost get ingress`
gives the ALB host names → Cloudflare CNAMEs for `api`, `ws`, `app`, `admin`
and the apex (proxy can be ON for these; the web ingress must accept the
bare domain and redirect to `app.`).

## 5. Console / CLI steps Terraform does not do

- **SES production access** (step 0) — and verify the DKIM status shows
  *Successful* on the identity once the three CNAMEs are in.
- **LiveKit egress credentials** (W8 needs them; keys never enter Terraform
  state, and the CI apply role may not create users):
  ```bash
  aws iam create-user --user-name atpost-prod-livekit-egress
  aws iam attach-user-policy --user-name atpost-prod-livekit-egress \
    --policy-arn "$(terraform output -raw livekit_egress_policy_arn)"
  aws iam create-access-key --user-name atpost-prod-livekit-egress   # paste into the LiveKit project, never into chat or a file in the repo
  ```
- **GitHub**: environment `prod` with *Deployment branches: main* and a
  required reviewer; the repository secrets above.
- **CloudFront** may ask for account verification on first use — open the
  support case it points to.
- **Budgets / Cost Explorer**: Cost Explorer must be enabled once in the
  console for the forecast alert to work.

## 6. Growing beyond lean

Every size is a variable (see `prod.tfvars.example`). Typical first moves:
`aurora_max_acu = 16`, `aurora_create_reader = true`, `msk_broker_instance_type
= "kafka.m7g.large"`, `opensearch_data_instance_count = 3` (+ dedicated
masters), `elasticache_node_type = "cache.r7g.large"` + `num_replicas = 2`,
`single_nat_gateway = false`. Aurora/ElastiCache changes wait for the
maintenance window (`apply_immediately = false`).
