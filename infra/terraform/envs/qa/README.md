# envs/qa — the QA account, step by step

The new AWS account hosts the **QA environment `qa`** (contract 3 Oct 2026).
It is QA for good: real production later gets a separate account and
`envs/prod`, which stays in the repo untouched. Nothing here has been applied
yet; follow the steps in order the first time.

What it builds (single copies, no high availability): a 2-AZ VPC with one
NAT gateway; EKS 1.36 with 2 system nodes, 2 general nodes (up to 4) and 1
Scylla node, plus Karpenter (Graviton on-demand, capped at 16 vCPU); Aurora
PostgreSQL Serverless v2 (one writer, 0.5–2 ACU); Valkey (one
cache.t4g.small); OpenSearch 2.19 (one t3.small.search); MSK (2 ×
kafka.t3.small, SCRAM + TLS); S3 + CloudFront media on
`media-qa.cleestudio.com`; three private buckets; certificates for
`qa.`, `api-qa.`, `ws-qa.`, `admin-qa.cleestudio.com`; SES for
`qa.cleestudio.com`; secret shells `atpost/qa/*`; GuardDuty, CloudTrail,
AWS Backup and a $1,000/month budget alert; ArgoCD, External Secrets, the
ALB controller, metrics-server and the observability stack.

Run every command in **Git Bash** from the repository root unless a step says
otherwise. Tools: Terraform 1.9.x, AWS CLI v2, kubectl, jq.

## 1. Sign in to the QA account

1. Create the profile once (pick the QA account and the administrator
   permission set when asked):
   ```bash
   aws configure sso --profile atpost-qa
   ```
2. Sign in and use the profile for everything below:
   ```bash
   aws sso login --profile atpost-qa
   export AWS_PROFILE=atpost-qa
   aws sts get-caller-identity --query Account --output text
   ```
   The last line prints the QA account id. Keep it for step 3.

## 2. Console requests (do these first; they take hours to days)

1. **EC2 vCPU quota**: Service Quotas → Amazon EC2 → *Running On-Demand
   Standard (A, C, D, H, I, M, R, T, Z) instances* → request **48 vCPUs** in
   ap-south-1. QA uses 12 at rest (2 × m7g.medium + 2 × m7g.xlarge + 1 ×
   m7g.large) and up to 38 when the general group and Karpenter are at their
   ceilings; a new account usually has 5–8.
2. **SES production access**: SES (ap-south-1) → *Account dashboard* →
   *Request production access*. Use case: transactional account email for a
   QA environment (sign-up codes, password resets), bounce handling = SNS →
   email. Until approved SES only sends to addresses verified one by one.
3. **Cost Explorer**: Billing → *Cost Explorer* → *Enable*. The budget's
   forecast alert needs it.

## 3. State bucket and lock table (once per account)

`bootstrap/` keeps its own state on disk. Use a `qa` workspace so the QA
account's bootstrap state never mixes with another account's:

```bash
cd infra/terraform/bootstrap
terraform init
terraform workspace new qa
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
terraform apply -var state_bucket_name=atpost-tfstate-$ACCOUNT_ID
terraform output
cd ../envs/qa
```

Keep `infra/terraform/bootstrap/terraform.tfstate.d/qa/terraform.tfstate`
(gitignored) somewhere safe; it is the only record of these two resources.

## 4. Variables file

1. ```bash
   cp qa.tfvars.example qa.tfvars
   ```
2. Open `qa.tfvars` and replace the four `CHANGEME` values in the REQUIRED
   block: the bucket ARN (`arn:aws:s3:::atpost-tfstate-<account-id>`), the
   lock table ARN (with the account id), your email address, and your public
   IP as `x.x.x.x/32` (`curl -s https://checkip.amazonaws.com`).
3. The same text later goes into the GitHub environment secret
   `TF_QA_TFVARS` (step 12).

## 5. Initialise against the QA state bucket

```bash
ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
terraform init \
  -backend-config="bucket=atpost-tfstate-$ACCOUNT_ID" \
  -backend-config="dynamodb_table=atpost-tfstate-locks" \
  -backend-config="key=envs/qa/terraform.tfstate" \
  -backend-config="region=ap-south-1" \
  -backend-config="encrypt=true"
terraform validate
```

## 6. Pass 0 — the internal zone, then delegate it at Cloudflare

The ArgoCD/Grafana certificate is validated inside the Route 53 zone
`aws-qa.cleestudio.com`, and pass 1 waits for that validation. So the zone
comes first and Cloudflare must delegate it before pass 1.

1. Create only the zone:
   ```bash
   terraform apply -var-file=qa.tfvars -target=module.dns.aws_route53_zone.aws_subdomain
   ```
   Type `yes` when asked.
2. Print its four name servers:
   ```bash
   ZONE_ID=$(aws route53 list-hosted-zones-by-name --dns-name aws-qa.cleestudio.com --query 'HostedZones[0].Id' --output text)
   aws route53 get-hosted-zone --id "$ZONE_ID" --query 'DelegationSet.NameServers' --output text
   ```
3. Cloudflare → cleestudio.com → DNS → add **four** records: type `NS`, name
   `aws-qa`, one name server each (DNS only).
4. Check the delegation answers (repeat until it lists the four servers):
   ```bash
   nslookup -type=NS aws-qa.cleestudio.com 1.1.1.1
   ```

## 7. Pass 1 — AWS resources only

The kubernetes and helm providers cannot reach a cluster that does not exist
yet, so pass 1 targets the AWS-side modules. Takes 30–45 minutes (MSK is the
slowest).

```bash
terraform plan -var-file=qa.tfvars -out=pass1.plan \
  -target=module.vpc -target=module.ecr -target=module.ecr_web \
  -target=module.dns -target=module.iam -target=module.eks \
  -target=module.aurora -target=module.msk -target=module.elasticache \
  -target=module.opensearch -target=module.media -target=module.waf \
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

Expected: roughly 380 resources added, 0 changed, 0 destroyed.

## 8. Right after pass 1

1. **Confirm the alert subscription**: your inbox has "AWS Notification -
   Subscription Confirmation"; click the link. Budget, GuardDuty and SES
   bounce mail arrive only after this.
2. **Add the Cloudflare records**:
   ```bash
   terraform output -json cloudflare_dns_records \
     | jq -r '.[] | [.purpose, .name, .type, .value] | @tsv'
   ```
   Add every row as DNS only (grey cloud), EXCEPT: the NS row (done in step
   6), the `media CDN host` row (step 8.4) and the four `QA public host` rows
   (step 10).
3. **Wait for both certificates to say ISSUED** (minutes after the CNAMEs
   resolve):
   ```bash
   aws acm describe-certificate --certificate-arn "$(terraform output -raw public_edge_certificate_arn)" --query Certificate.Status
   aws acm describe-certificate --region us-east-1 --certificate-arn "$(terraform output -raw media_cdn_certificate_arn)" --query Certificate.Status
   ```
4. In `qa.tfvars` set `media_custom_domain_enabled = true`, run the step 7
   plan/apply again (only the CloudFront distribution changes), then add the
   `media CDN host` CNAME (`media-qa` → the cloudfront.net name) at
   Cloudflare, DNS only.
5. Point kubectl at the cluster (the principal that ran pass 1 is admin):
   ```bash
   aws eks update-kubeconfig --name atpost-qa --region ap-south-1
   kubectl get nodes
   ```
   Five nodes: 2 system, 2 general, 1 memory.

## 9. Pass 2 — everything (in-cluster tooling, databases, topics)

Needs `deploy/argocd/applicationset-qa.yaml` in the checkout (the values lane
writes it; Terraform applies it).

```bash
terraform plan -var-file=qa.tfvars -out=pass2.plan
terraform apply pass2.plan
```

Installs External Secrets, the ALB controller, metrics-server, the Scylla
operator and a 1-node ScyllaCluster, ArgoCD (internal), the observability
stack and Karpenter, and runs two one-shot Jobs: `aurora-bootstrap` (the five
databases with postgis, pg_trgm, pgcrypto) and `msk-topics` (every topic, 3
partitions, replication factor 2, min.insync.replicas 1). If `msk-topics`
times out: `kubectl -n msk-bootstrap logs job/msk-topics` (almost always
External Secrets had not mirrored the SCRAM secret yet — apply again).

Run pass 2 with the SAME principal as pass 1, or one listed in
`cluster_admin_arns`; anyone else gets 401 from the cluster.

Then the lead runs the seeder (`scripts/prodsecrets.sh --env qa ...`, order in
its header), applies `deploy/argocd/repo-credentials-qa.yaml`, and syncs the
QA Applications.

## 10. Public host names at Cloudflare (after the first ArgoCD sync)

1. ```bash
   kubectl get ingress -A   # api/ws in namespace atpost, web/admin in atpost-web
   ```
   The ADDRESS column holds the ALB host names.
2. Put them into `qa.tfvars` (`ingress_alb_hostnames`, keys `qa`, `api-qa`,
   `ws-qa`, `admin-qa`), then:
   ```bash
   terraform apply -var-file=qa.tfvars
   terraform output -json cloudflare_dns_records \
     | jq -r '.[] | select(.purpose | startswith("QA public host")) | [.name, .type, .value] | @tsv'
   ```
3. Add the four CNAMEs at Cloudflare (proxy may be ON for these four).

## 11. Outputs and where they go

Output names are the same as prod, so `scripts/prodsecrets.sh --env qa` and
the values fill script read them unchanged (`TF_DIR=infra/terraform/envs/qa`).

| Output | Goes to |
|---|---|
| `service_irsa_role_arns` | `serviceAccount.irsaRoleArn` in each values-qa.yaml (`atpost-qa-<service>-irsa`) |
| `media_bucket_name` | media-service + worker `S3_BUCKET` |
| `media_cloudfront_key_pair_id`, `media_cloudfront_signing_secret_arn` | seeder → `atpost/qa/media-service` |
| `media_cdn_base_url` | a literal `MEDIA_CDN_BASE_URL` in `deploy/services/media-service/values-qa.yaml` (works once `media_custom_domain_enabled = true` is applied) |
| `live_recordings_bucket_name`, `commerce_invoices_bucket_name`, `food_files_bucket_name` | live-service-v2 / commerce / food values-qa |
| `msk_bootstrap_brokers`, `msk_scram_secret_name` | seeder → `KAFKA_BROKERS` + SCRAM user/password in every service secret |
| `aurora_cluster_endpoint`, `aurora_master_secret_arn` | seeder builds one DSN per service |
| `elasticache_primary_endpoint`, `elasticache_auth_secret_arn` | `REDIS_ADDR` / `REDIS_PASSWORD`, `REDIS_TLS_ENABLED=true` |
| `opensearch_endpoint`, `opensearch_master_secret_arn` | search-service `OPENSEARCH_URL` + basic auth |
| `public_edge_certificate_arn` | `alb.ingress.kubernetes.io/certificate-arn` on api-gateway, chat-ws-gateway, web, admin |
| `waf_web_acl_arn` (= `waf_psp_webhook_acl_arn` in QA) | every public ingress, payments webhook included |
| `commerce_pii_kms_key_id` | commerce `COMMERCE_KMS_KEY_ID` |
| `ses_configuration_set_name` | identity-auth `SES_CONFIGURATION_SET` (`atpost-qa-transactional`) |
| `ci_role_arn`, `terraform_apply_role_arn` | GitHub secrets `AWS_CI_ROLE_ARN`, `AWS_TERRAFORM_ROLE_ARN` (step 12) |
| `ecr_repository_urls` | CI image names |

## 12. Steps Terraform does not do

1. **GitHub environment `qa`**: repository Settings → Environments → New
   environment `qa` → *Required reviewers*: you; *Deployment branches*:
   `main` and `qa`. Add environment secrets `AWS_TERRAFORM_ROLE_ARN`
   (`terraform output -raw terraform_apply_role_arn`) and `TF_QA_TFVARS`
   (the whole text of your `qa.tfvars`). The apply role trusts ONLY
   `repo:rvreddy476/modernsmapp:environment:qa`; the CI role trusts refs
   `main` and `qa` and pull requests.
2. **LiveKit egress credentials** (keys never enter Terraform state):
   ```bash
   aws iam create-user --user-name atpost-qa-livekit-egress
   aws iam attach-user-policy --user-name atpost-qa-livekit-egress \
     --policy-arn "$(terraform output -raw livekit_egress_policy_arn)"
   aws iam create-access-key --user-name atpost-qa-livekit-egress
   ```
   Paste the key pair into the LiveKit Cloud QA project's egress settings
   only — never into chat or a file in the repository.
3. **SES**: once the three DKIM CNAMEs are in, SES → *Identities* →
   `qa.cleestudio.com` shows DKIM *Successful*.
4. **CloudFront** may ask for account verification on first use — open the
   support case it points to.
5. **ArgoCD UI** (internal load balancer, so through kubectl):
   ```bash
   kubectl -n argocd port-forward svc/argocd-server 8080:80
   ```
   Open http://localhost:8080, user `admin`; the password is in Secrets
   Manager `atpost/qa/argocd/admin` (console → Secrets Manager → *Retrieve
   secret value*).

## 13. Cost

Roughly $1,000–1,150/month on-demand at rest (estimate; see the Terraform
lane report for the line items). The biggest levers, all in `qa.tfvars`:
`general_node_min/desired = 1` (−$125), `system_node_instance_type =
"t4g.medium"` (−$10), `aurora_max_acu = 1`. AWS Budgets emails at $500 and
$1,000 actual and at 100% forecast.
