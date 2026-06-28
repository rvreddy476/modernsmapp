# Staging platform layer (Phase 2): Key Vault + ESO, ingress-nginx,
# ArgoCD, managed Postgres + Redis, and the self-hosted data platform
# (Scylla + Redpanda + MinIO). AWS infra/terraform/ stays untouched.
#
# Apply order note: the kubernetes_manifest / helm_release resources need
# the AKS API reachable, so on a green-field cluster apply in two passes —
# first the cluster (module.aks), then the platform. See docs/DEPLOY-azure.md.

module "keyvault" {
  source = "../../modules/keyvault"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  key_vault_name      = var.key_vault_name
}

module "external_secrets" {
  source = "../../modules/external-secrets"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  aks_oidc_issuer_url = module.aks.oidc_issuer_url
  key_vault_id        = module.keyvault.id
  key_vault_uri       = module.keyvault.uri
}

module "ingress_nginx" {
  source = "../../modules/ingress-nginx"

  environment = var.environment
}

module "argocd" {
  source = "../../modules/argocd"

  environment     = var.environment
  key_vault_id    = module.keyvault.id
  argocd_hostname = var.argocd_hostname
}

module "postgres" {
  source = "../../modules/postgres"

  environment         = var.environment
  resource_group_name = module.resource_group.name
  location            = module.resource_group.location
  vnet_id             = module.network.vnet_id
  data_subnet_id      = module.network.data_subnet_id
  key_vault_id        = module.keyvault.id
  secret_name         = "atpost-${var.environment}-postgres"
  databases           = var.postgres_databases
  ha_enabled          = false
  sku_name            = "GP_Standard_D2ds_v5"
}

module "redis" {
  source = "../../modules/redis"

  environment  = var.environment
  key_vault_id = module.keyvault.id
  secret_name  = "atpost-${var.environment}-redis"
}

module "data_platform" {
  source = "../../modules/data-platform"

  environment  = var.environment
  location     = module.resource_group.location
  key_vault_id = module.keyvault.id
  # Lean staging footprint to fit a small vCPU quota: single-node Scylla
  # (dev mode), single Redpanda, standalone MinIO. Scale up via these vars.
  scylla_developer_mode      = true
  zones                      = ["1"]
  scylla_cpu_per_replica     = "1"
  scylla_memory_per_replica  = "4Gi"
  scylla_storage_per_replica = "20Gi"
  redpanda_replicas          = 1
  redpanda_cpu               = 1
  redpanda_storage           = "20Gi"
  scylla_secret_name         = "atpost-${var.environment}-scylla"
  redpanda_secret_name       = "atpost-${var.environment}-redpanda"
  minio_secret_name          = "atpost-${var.environment}-minio"
  minio_mode                 = "standalone"
  minio_replicas             = 1
  minio_storage              = "20Gi"
}

output "key_vault_uri" { value = module.keyvault.uri }
output "eso_client_id" { value = module.external_secrets.identity_client_id }
output "postgres_fqdn" { value = module.postgres.fqdn }
output "redis_hostname" { value = module.redis.hostname }
output "kafka_brokers" { value = module.data_platform.kafka_brokers }
