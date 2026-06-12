# AWS staging deploy runbook

End-to-end procedure to stand up the atpost staging environment on
AWS for the first time. Estimated wall-clock: **4–6 hours** if
everything works first try (most of that is AWS provisioning, not
your time). Most steps idempotent — safe to re-run.

Prod uses the same procedure with a different env directory; see
"Going to prod" at the end.

## 0. Prerequisites

### Local tools

```bash
# macOS via Homebrew (Linux/Windows: adapt)
brew install terraform        # ~> 1.6 (check infra/terraform/envs/staging/versions.tf)
brew install awscli           # v2
brew install kubectl helm jq  # for post-apply diagnostics
brew install gh               # GitHub CLI, for setting Actions secrets

# Required SDKs / runtimes for the build-push workflow to work locally
brew install go               # 1.25
brew install --cask docker    # for ad-hoc image builds
```

### AWS accounts (3 sub-accounts under one organization)

1. Master account (billing + AWS Organizations + IAM Identity Center)
2. `shared-services` sub-account (ECR + Terraform state bucket)
3. `staging` sub-account (this runbook's target)
4. `prod` sub-account (later)

Why three: cost separation, blast-radius isolation, easier compliance
attestation. Create them via the Organizations console; budgets +
SCPs (Service Control Policies) are out of scope here.

### Cloudflare DNS access

cleestudio.com lives on Cloudflare. You'll add an NS record delegating
`staging.aws.cleestudio.com` (4 entries) to Route 53. Have the
Cloudflare dashboard open at the right zone.

### Tofu / OpenTofu note

If you're using OpenTofu instead of Terraform, swap `terraform` for
`tofu` in every command below. Scaffold is compatible.

---

## 1. Replace placeholders (one-time per env)

The IaC and Helm values carry `CHANGEME` / `123456789012` /
`rvreddy476/modernsmapp` placeholders because the right values
depend on your account IDs. Do this BEFORE the first apply or the
plan will lie about what it's building.

### 1.1 AWS account ID

Get the staging account ID from `aws sts get-caller-identity`
(assume the staging role first):

```bash
STAGING_ACCT=$(aws sts get-caller-identity --query Account --output text)
echo $STAGING_ACCT
```

Replace `123456789012` across the per-service values files:

```bash
cd /path/to/atpost
# Backup, then sed in place
git checkout -b deploy/staging-bootstrap
grep -rl '123456789012' deploy/services/ \
  | xargs sed -i.bak "s/123456789012/${STAGING_ACCT}/g"
find deploy/services -name '*.bak' -delete
```

### 1.2 GitHub repo

Replace `rvreddy476/modernsmapp` in the ApplicationSet with your
GitHub `org/repo`:

```bash
sed -i.bak "s|rvreddy476/modernsmapp|YOUR_ORG/YOUR_REPO|g" \
  deploy/argocd/applicationset.yaml
rm deploy/argocd/applicationset.yaml.bak
```

### 1.3 Verify GitHub OIDC thumbprint

The IAM module hardcodes `6938fd4d98bab03faadb97b34396831e3780aea1`
(current as of 2023-06). If GitHub rotated the cert since (rare but
not impossible), pull the current thumbprint from the AWS docs and
update `infra/terraform/modules/iam/main.tf`. If your `terraform apply`
fails on the OIDC provider, this is the cause.

### 1.4 Commit + push

```bash
git add deploy/services deploy/argocd
git commit -m "deploy(staging): fill in account ID + GitHub repo"
git push -u origin deploy/staging-bootstrap
# open a PR + merge to main after review
```

---

## 2. Terraform state bootstrap (one-time per account)

```bash
cd infra/terraform/bootstrap
terraform init
terraform apply \
  -var "state_bucket_name=atpost-tfstate-${STAGING_ACCT}"
```

Outputs:

```
state_bucket = "atpost-tfstate-..."
lock_table   = "atpost-tfstate-locks"
```

### 2.1 Wire the backend in envs/staging

Edit `infra/terraform/envs/staging/backend.tf`:

```hcl
terraform {
  backend "s3" {
    bucket         = "atpost-tfstate-<staging-account-id>"
    dynamodb_table = "atpost-tfstate-locks"
    key            = "envs/staging/terraform.tfstate"
    region         = "ap-south-1"
    encrypt        = true
  }
}
```

### 2.2 Create `staging.tfvars` (gitignored)

```hcl
# infra/terraform/envs/staging/staging.tfvars
tfstate_bucket_arn     = "arn:aws:s3:::atpost-tfstate-<staging-account-id>"
tfstate_lock_table_arn = "arn:aws:dynamodb:ap-south-1:<staging-account-id>:table/atpost-tfstate-locks"

github_repos = [
  "YOUR_ORG/YOUR_REPO",
]

# Optional — your own IAM role/user ARN for cluster-admin kubectl access
cluster_admin_arns = [
  "arn:aws:iam::<staging-account-id>:role/AdminAssumeRole",
]
```

---

## 3. First `terraform apply` — AWS data plane

**Expect this to FAIL on the helm_release / kubernetes_manifest
resources.** That's the two-apply bootstrap dance — the kubernetes +
helm providers can't authenticate against an EKS cluster that
doesn't exist yet. Documented in `infra/terraform/README.md`.

```bash
cd infra/terraform/envs/staging
terraform init   # downloads providers + AWS module deps (5–10 min)
terraform plan -var-file=staging.tfvars  # ~120 resource adds expected
terraform apply -var-file=staging.tfvars
```

Wait ~25 minutes. AWS provisions in order:

1. VPC + 9 subnets + NAT + IGW (~2 min)
2. KMS keys (instant)
3. S3 buckets (instant)
4. ECR repos (~30s — one per service)
5. IAM roles + Identity Center scaffolding (~1 min)
6. **EKS control plane** (slow: ~12 min)
7. EKS node groups (~5 min)
8. Aurora cluster + writer + reader (~10 min)
9. ElastiCache replication group (~7 min)
10. OpenSearch domain (~15 min — runs partially in parallel)
11. MSK Serverless cluster (~5 min)
12. CloudFront distribution (~3 min)

Expect ~30 errors at the end: `kubernetes_manifest.*` and
`helm_release.*` fail with `connection refused` / `Unauthorized`.
**That's fine. Move to step 4.**

### 3.1 Wire up kubectl

```bash
aws eks update-kubeconfig --name atpost-staging --region ap-south-1
kubectl get nodes  # confirm the EKS nodes show Ready
```

---

## 4. Second `terraform apply` — cluster tooling

Same command. The providers authenticate now that EKS responds.

```bash
terraform apply -var-file=staging.tfvars
```

This installs (~8 min):

- External Secrets Operator (kube-system tier)
- AWS Load Balancer Controller
- Scylla Operator + ScyllaCluster (3 replicas across AZs)
- ArgoCD HA + AppProject "atpost"
- ApplicationSet (will fail-loop initially — needs the secrets)
- Aurora bootstrap Job (creates `app`, `identity_db`, `chat_db`,
  `commerce_db`, `feed_db` databases)
- kube-prometheus-stack
- Tempo + Loki + Grafana Alloy
- Karpenter

### 4.1 Verify the cluster tooling

```bash
kubectl get pods -n external-secrets
kubectl get pods -n kube-system | grep aws-load-balancer-controller
kubectl get pods -n scylla
kubectl get pods -n argocd
kubectl get pods -n observability
kubectl get pods -n karpenter
```

All should be `Running`. If anything is `CrashLoopBackOff`,
`kubectl logs` it — most issues at this stage are IAM (missing IRSA
trust) or a stale Helm chart version pin.

### 4.2 Verify Aurora logical DBs exist

```bash
kubectl logs -n aurora-bootstrap job/aurora-bootstrap
# Expect: "CREATE DATABASE" lines for app, identity_db, chat_db,
# commerce_db, feed_db (skipped if they already exist).
```

---

## 5. DNS delegation

Get the Route 53 NS records from Terraform output:

```bash
terraform output -json dns_name_servers
# ["ns-...awsdns-..", "ns-...awsdns-..", "ns-...awsdns-..", "ns-...awsdns-..."]
```

In Cloudflare:

1. Open the `cleestudio.com` zone.
2. Add 4 NS records, each name `staging.aws`, value one of the four
   Route 53 names above.
3. Save.

ACM certificate validation happens automatically once DNS resolves.
Watch progress:

```bash
aws acm describe-certificate \
  --certificate-arn $(terraform output -raw wildcard_cert_arn) \
  --query 'Certificate.Status'
# PENDING_VALIDATION → ISSUED (typically 5–15 minutes after DNS lands)
```

---

## 6. Seed Secrets Manager values

Every service consumes secrets via External Secrets Operator. ESO
reads `atpost/staging/*` from Secrets Manager and projects each into
a kubernetes Secret. Until those entries exist, every service pod
ExternalSecret reports `SecretSyncedError`.

### 6.1 Generate the shared secrets

```bash
# Strong random values
export JWT_SECRET=$(openssl rand -hex 32)
export INTERNAL_SERVICE_KEY=$(openssl rand -hex 32)
export JWT_KID="v1"

# Optional: previous JWT secret for rotation
# export JWT_SECRET_PREVIOUS="...prior value..."
# export JWT_KID_PREVIOUS="v0"
```

### 6.2 Seed per service

The repo's `deploy/services/<svc>/values-staging.yaml` already
defines each ExternalSecret's `remoteKey`. Iterate over them:

```bash
SHARED_VALUE=$(jq -n \
  --arg jwt "$JWT_SECRET" \
  --arg kid "$JWT_KID" \
  --arg key "$INTERNAL_SERVICE_KEY" \
  '{jwt_secret:$jwt, jwt_kid:$kid, internal_service_key:$key}')

for svc in deploy/services/*/; do
  name=$(basename "$svc")
  aws secretsmanager create-secret \
    --name "atpost/staging/${name}" \
    --secret-string "$SHARED_VALUE" \
    --description "atpost staging ${name} bootstrap" \
    2>/dev/null || \
  aws secretsmanager put-secret-value \
    --secret-id "atpost/staging/${name}" \
    --secret-string "$SHARED_VALUE"
done
```

### 6.3 Service-specific secrets (one-off; add per service)

For services that need their own credentials beyond the shared three
(database URLs, third-party API keys), update each individually.
Example for `post-service`:

```bash
aws secretsmanager update-secret \
  --secret-id atpost/staging/post-service \
  --secret-string "$(jq -n \
    --arg jwt "$JWT_SECRET" --arg kid "$JWT_KID" \
    --arg ik "$INTERNAL_SERVICE_KEY" \
    --arg pg "postgres://postgres:$(terraform output -raw aurora_master_password)@$(terraform output -raw aurora_writer_endpoint):5432/app?sslmode=require" \
    --arg redis "$(terraform output -raw elasticache_endpoint):6379" \
    --arg kafka "$(terraform output -raw msk_bootstrap_brokers)" \
    --arg scylla "$(terraform output -raw scylla_endpoint)" \
    '{
       jwt_secret:$jwt, jwt_kid:$kid, internal_service_key:$ik,
       postgres_dsn:$pg, redis_addr:$redis, kafka_brokers:$kafka,
       scylla_hosts:$scylla
     }')"
```

(The aurora_master_password output exists; the other URL outputs
are similarly available — see `infra/terraform/envs/staging/main.tf`.)

Repeat for every service that has additional secrets. Use
`docs/deploy/secret_shapes.md` as the source of truth for each
service's Secrets Manager JSON shape and matching
`externalSecret.data` mappings.

---

## 7. GitHub Actions secrets

The build-push workflow uses GitHub OIDC → assumes the CI role.

```bash
gh secret set AWS_CI_ROLE_ARN \
  --body "$(terraform output -raw ci_role_arn)" \
  --repo YOUR_ORG/YOUR_REPO
```

Optional, if the default `GITHUB_TOKEN` can't push (branch protection):

```bash
gh secret set VALUES_BUMP_TOKEN \
  --body "ghp_..." \  # PAT with contents: write on the repo
  --repo YOUR_ORG/YOUR_REPO
```

---

## 8. First image push

Trigger the build-push workflow against `main`:

```bash
# Easiest: just push a no-op change
echo "$(date)" >> docs/deploy/last_push.txt
git add docs/deploy/last_push.txt
git commit -m "ci: kick off first image push"
git push origin main
```

Watch the workflow at `https://github.com/YOUR_ORG/YOUR_REPO/actions`.
Expected output:

- `detect-changes`: emits comma-separated service list.
- `build-push`: matrix job, ~30 services in parallel,
  Docker buildx + linux/arm64. ~10 min per service for the cold
  layer cache; subsequent runs use the gha cache.
- `bump-tag`: commits `image.tag: "<sha>"` to every
  `deploy/services/<svc>/values-staging.yaml` and pushes back.

That commit triggers ArgoCD reconcile → first deploy.

---

## 9. Verify ArgoCD picked them up

```bash
# Port-forward (or use the ALB hostname from `kubectl get ingress -n argocd`)
kubectl port-forward -n argocd svc/argocd-server 8080:80

# In another shell
ARGOCD_ADMIN=$(aws secretsmanager get-secret-value \
  --secret-id atpost/staging/argocd/admin \
  --query SecretString --output text | jq -r .password)
echo "ArgoCD admin: $ARGOCD_ADMIN"
```

Open https://localhost:8080 in a browser, sign in as `admin` with
that password. You should see ~30 `staging-*` Applications, all
syncing.

If you see `SecretSyncedError` on a service, the corresponding
`atpost/staging/<svc>` secret was missing or malformed at sync time.
Fix the secret + click "Sync" on the affected Application.

---

## 10. Smoke test against staging

Once ArgoCD shows everything Healthy:

```bash
# Resolve the api-gateway URL — should be served via Cloudflare CNAME
# pointing at the ALB hostname.
kubectl get ingress -n atpost api-gateway -o jsonpath='{.status.loadBalancer.ingress[0].hostname}'

# Health
curl -sS https://api.staging.cleestudio.com/livez
# {"status":"alive"}

# Auth — should reject missing token with 401
curl -sS https://api.staging.cleestudio.com/v1/posts -i | head -1

# Liveness on a downstream
kubectl exec -n atpost deploy/post-service -- wget -qO- http://localhost:8084/livez
```

### 10.1 In-cluster diagnostics

```bash
# Tempo traces
# Grafana → Explore → Tempo datasource → "Service Map"
#   Should show service-to-service edges (post-service → user-service etc.)

# Loki logs
# Grafana → Explore → Loki → query {namespace="atpost"} |= "ERROR"
#   First-deploy errors usually fall into 3 buckets:
#   - "MONETIZATION_URL_UNSET" — env not set; check secret
#   - "POSTGRES_DSN missing"   — secret shape wrong
#   - dial tcp: i/o timeout    — SG rule missing

# Prometheus metrics
# Grafana → Explore → Prometheus → query up{namespace="atpost"}
#   Every service should report 1
```

---

## 11. Going to prod

Same flow, different env directory + manual sync gate. Before
`terraform apply` in `envs/prod`:

1. **MUST set `cluster_endpoint_public_access_cidrs`** in
   `prod.tfvars` to office/VPN CIDRs. The variable has no default
   in prod (intentional — leaving it open is a flagged security
   risk). The apply will refuse without it.
2. Narrow the IAM trust to `ref:refs/heads/main` on the prod CI
   role (the staging default allows any branch).
3. ApplicationSet's prod tracks `release/prod`, NOT `main`. Push
   the initial `release/prod` branch first:
   ```bash
   git checkout main && git pull
   git branch release/prod
   git push origin release/prod
   ```
4. Use the `promote.yml` workflow to ship staging tags to prod
   instead of fresh builds. Single source of truth for prod
   image content.

---

## Troubleshooting cheatsheet

| Symptom | Likely cause | Fix |
|---|---|---|
| First terraform apply fails on `helm_release` | Two-apply dance — expected | Run apply again |
| `kubernetes_manifest` 401 / 403 after EKS exists | kube config didn't pick up new auth | `aws eks update-kubeconfig` |
| ACM cert stuck PENDING_VALIDATION | DNS delegation not live | Check Cloudflare NS records, wait 5–10 min |
| All service pods CrashLoopBackOff after first deploy | Secrets Manager entries missing | Step 6 |
| ArgoCD ApplicationSet not generating apps | Wrong `repoURL` in applicationset.yaml | Re-apply step 1.2 |
| Aurora connection refused | SG rule missing OR private subnet routing | Check `aws_security_group_rule.aurora_from_eks` in module |
| Karpenter not provisioning nodes | NodePool limits hit or workload= label missing | `kubectl describe pod` to see Pending reasons |
| OpenSearch domain creation timing out | Account-level quota on OpenSearch | Request quota increase in the AWS console |
| Build-push workflow 401 to AWS | `AWS_CI_ROLE_ARN` secret not set | Step 7 |
| Tag-bump commit fails to push back | Branch protection blocks GITHUB_TOKEN | Set `VALUES_BUMP_TOKEN` PAT (step 7) |
| Image pull errors on the pods | ECR repo doesn't exist OR IRSA wrong | Check ECR repo names match values' `image.repository`; check IRSA role ARN on the ServiceAccount |

---

## Known follow-ups

These are scoped intentionally — capture them for the operator who
takes the next step:

- Keep `docs/deploy/secret_shapes.md` current as services add or remove
  environment variables. The doc now exists, but it must stay tied to
  each service's `internal/config/config.go` and Helm values.
- AWS Backup plan for Aurora + Scylla EBS volumes. Module ships the
  cluster + snapshots but no automated DR drill.
- WAF web ACL on the public ALB (Phase 5 hardening).
- Per-env terraform output → shell-friendly env file generator so
  step 6.3 doesn't have to source-dive.
- A pre-flight `make staging-doctor` target that runs every
  external-dependency check (DNS, ACM, ECR pushable, secrets seeded)
  before kicking off ArgoCD sync.
