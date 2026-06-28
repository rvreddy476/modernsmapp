# Deploying atPost to Azure (portable / multi-cloud)

This is the Azure deploy runbook. It is **additive** to the AWS setup —
nothing under `infra/terraform/`, the AWS Helm values, or the AWS GitHub
workflows changes. The same container images and the same
`charts/atpost-service` chart run on both clouds; only the per-cloud
**infra** and **values** differ. Azure for ~3 months, then switch back to
AWS by re-pointing DNS (see [Switching clouds](#switching-clouds-azure--aws)).

## Portability model

| Layer | Shared (unchanged) | Azure-specific (additive) | AWS equivalent |
|---|---|---|---|
| App code + images | ✅ all services + web | — | — |
| Helm chart | ✅ `charts/atpost-service` | — | — |
| Per-cloud values | — | `deploy/**/values-azure-*.yaml` | `values-*.yaml` |
| Cluster | — | AKS | EKS |
| Registry | — | ACR | ECR |
| Managed DB / cache | — | PostgreSQL Flexible Server / Azure Cache for Redis | Aurora / ElastiCache |
| Self-hosted stores | ✅ Scylla + Redpanda + MinIO | (run on AKS) | (run on EKS) |
| Secrets | ✅ ESO + per-service ExternalSecret | Key Vault + `azure-key-vault` store | Secrets Manager + `aws-secrets-manager` |
| Workload identity | — | Entra Workload Identity | IRSA |
| Edge | — | Front Door + WAF + nginx | CloudFront + WAFv2 + ALB |
| CI auth | — | GitHub OIDC → Entra | GitHub OIDC → IAM |

The **only** reason app code needs zero changes: services read infra via
env (`POSTGRES_DSN`, `REDIS_ADDR`, `KAFKA_BROKERS`, MinIO, Scylla) and all
creds arrive as ESO-synced Secrets. App pods need **no** Azure identity —
only ESO authenticates to Key Vault (via Workload Identity).

## Prerequisites

- Azure subscription + `az login`; an Entra tenant.
- `terraform` ≥ 1.6, `kubectl`, `helm`, `az`.
- Pick a region (default `centralindia`).
- Decide globally-unique names: Key Vault (`atpost-<env>-kv`), ACR
  (`atpost<env>`), tfstate Storage Account (`atposttfstate`). Override the
  `key_vault_name` var if the default is taken.

## 0. Bootstrap remote state

```bash
cd infra/azure/bootstrap
terraform init && terraform apply
# note the outputs: resource_group_name / storage_account_name / container_name
```

Fill the `backend "azurerm"` block (or pass `-backend-config`) in
`envs/<env>/backend.tf` with those values. CI passes them via the
`AZ_TFSTATE_RG/SA/CONTAINER` repo vars.

## 1. Cluster + registry + CI identity (Phase 1)

`kubernetes_manifest` / `helm_release` resources need the AKS API
reachable, so on a **green-field** cluster apply in **two passes**:

```bash
cd infra/azure/envs/staging
terraform init   # with the backend-config from step 0
# Pass 1 — cluster + registry + identities only:
terraform apply -var subscription_id=<SUB> \
  -target=module.resource_group -target=module.network \
  -target=module.aks -target=module.acr -target=module.identity
az aks get-credentials -g $(terraform output -raw resource_group) -n $(terraform output -raw aks_name)
```

Wire CI from the outputs (GitHub repo **Variables**, not secrets — they're
not sensitive): `AZURE_CLIENT_ID` = `ci_client_id`, plus `AZURE_TENANT_ID`,
`AZURE_SUBSCRIPTION_ID`, `ACR_NAME_STAGING`/`ACR_NAME_PROD`,
`AZ_TFSTATE_RG/SA/CONTAINER`. CI then runs `terraform-azure.yml`
(validate on PR, manual `plan`/`apply`) and `build-push-acr.yml`
(build/push images to ACR + bump `values-azure-<env>.yaml`).

## 2. Platform layer (Phase 2)

```bash
# Pass 2 — platform (cluster API is now reachable):
terraform apply -var subscription_id=<SUB>   # full apply, no -target
```

This installs ESO (+ `azure-key-vault` ClusterSecretStore), ingress-nginx,
ArgoCD, the managed Postgres + Redis, and the self-hosted Scylla +
Redpanda + MinIO. The managed/self-hosted stores write their connection
details into Key Vault as `atpost-<env>-{postgres,redis,scylla,redpanda,minio}`.

### Seed the per-service secrets

Each service's ExternalSecret reads **one** Key Vault secret named
`atpost-<env>-<svc>` whose JSON properties match its `externalSecret.data`
remoteRefs (e.g. `internal_service_key`, `jwt_secret`, `POSTGRES_DSN`).
This is the same model as AWS (one per-service blob) — seed it once per
service, composing the managed-store creds you need from the reference
secrets above:

```bash
KV=atpost-staging-kv
az keyvault secret set --vault-name $KV --name atpost-staging-user-service \
  --value '{"internal_service_key":"...","jwt_secret":"...","jwt_kid":"rsa-1",
            "POSTGRES_DSN":"postgres://atpostadmin:<pw>@<pg-fqdn>:5432/user_service?sslmode=require",
            "REDIS_ADDR":"<redis-host>:6380","KAFKA_BROKERS":"redpanda.redpanda.svc.cluster.local:9093"}'
```

> Note: Key Vault secret names allow only `[0-9a-zA-Z-]`, which is why the
> generator rewrites `atpost/<env>/<svc>` → `atpost-<env>-<svc>`.

## 3. Deploy services + web (Phase 3)

```bash
# Regenerate the Azure values whenever the AWS values change:
scripts/gen-azure-values.sh all
git add deploy/**/values-azure-*.yaml && git commit && git push

# Hand the ApplicationSets to the AKS-side ArgoCD:
kubectl apply -f deploy/azure-applicationset.yaml
kubectl apply -f deploy/web-azure-applicationset.yaml
```

ArgoCD reconciles every `deploy/services/*` and `deploy/web/*` against the
shared chart + its `values-azure-<env>.yaml`. Staging auto-syncs; prod is
manual (`argocd app sync azure-prod-<svc>`).

## 4. Edge: Front Door + WAF + DNS

Once `ingress-nginx` has a public LB IP:

```bash
kubectl -n ingress-nginx get svc ingress-nginx-controller \
  -o jsonpath='{.status.loadBalancer.ingress[0].ip}'
```

Set the edge vars and re-apply:

```hcl
# envs/staging/terraform.tfvars
enable_edge           = true
edge_origin_host_name = "<nginx LB IP or DNS>"
edge_zone_name        = "azure.cleestudio.com"
edge_cname_records    = { api = "<frontdoor_endpoint output>", app = "<frontdoor_endpoint output>" }
```

```bash
terraform apply -var subscription_id=<SUB>
# delegate dns_name_servers from your registrar, then point the app
# hostnames at frontdoor_endpoint.
```

## Verification

- `kubectl get clustersecretstore azure-key-vault` → `Valid`.
- A service's ExternalSecret → `SecretSynced`; its pod is `Running`.
- `helm template charts/atpost-service -f deploy/services/api-gateway/values-azure-staging.yaml`
  renders nginx ingress + the `azure-key-vault` store.
- Hit `https://api.<zone>/...` through Front Door; backend E2E suite
  (pointed at the AKS gateway) passes.
- **Switch test:** the *same* image tag deploys + runs on both AKS and EKS.

## Switching clouds (Azure ⇄ AWS)

Same images + same chart run on both clusters. To switch:

1. Make sure the target cluster is deployed and healthy (its ArgoCD green).
2. **Migrate data** (below) — the one non-trivial step.
3. Re-point DNS: Front Door ⇄ CloudFront/ALB. Lower TTLs beforehand.
4. Watch error rates; keep the old cluster warm until cut-over is proven.

### Data-migration runbook

The stateful stores are per-cloud, so a switch must move data. This is the
deliberate cost of independent clouds.

| Store | Strategy | Notes |
|---|---|---|
| **Postgres** | `pg_dump` → `pg_restore` per database | Schedule a short write-freeze or use logical replication for near-zero downtime. The DSN is the only thing that changes (in the per-service KV/SM secret). |
| **Redis** | Rebuild | Cache only — no migration; cold cache warms after cut-over. |
| **Scylla** | `nodetool snapshot` → `sstableloader` into the target, or dual-write during transition | RF=3 on both sides; verify counts before cut-over. |
| **MinIO** | `mc mirror src/ dst/` | S3-compatible both ends; mirror buckets, then a final delta sync at cut-over. |
| **Redpanda / Kafka** | Drain or `mirror-maker`-style copy | Topics are mostly transient; prefer draining consumers, then re-create topics on the target. |

Each per-service secret (`atpost-<env>-<svc>`) carries the store endpoints,
so post-migration you update those secrets on the **target** cloud and the
ESO sync flips every pod to the new endpoints — no image rebuild.

## What is NOT touched

`infra/terraform/` (AWS), the AWS `deploy/**/values-{staging,prod}.yaml`,
`deploy/argocd/applicationset.yaml`, `deploy/web-applicationset.yaml`, and
`.github/workflows/{terraform,build-push}.yml` are all left exactly as they
are. The AWS cluster keeps running off them unchanged.
