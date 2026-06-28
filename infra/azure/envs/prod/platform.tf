# Prod platform layer (Phase 2) — same modules as staging, production
# sizing (HA Postgres, distributed MinIO, multi-replica Scylla/Redpanda).
# AWS infra/terraform/ stays untouched.
#
# Apply order note: helm_release / kubernetes_manifest need the AKS API
# reachable — apply module.aks first, then the platform. See
# docs/DEPLOY-azure.md.

module "keyvault" {
  source = "../../modules/keyvault"

  environment              = var.environment
  resource_group_name      = module.resource_group.name
  location                 = module.resource_group.location
  key_vault_name           = var.key_vault_name
  purge_protection_enabled = true
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

  environment           = var.environment
  resource_group_name   = module.resource_group.name
  location              = module.resource_group.location
  vnet_id               = module.network.vnet_id
  data_subnet_id        = module.network.data_subnet_id
  key_vault_id          = module.keyvault.id
  secret_name           = "atpost-${var.environment}-postgres"
  databases             = var.postgres_databases
  ha_enabled            = true
  sku_name              = "GP_Standard_D4ds_v5"
  storage_mb            = 262144
  backup_retention_days = 14
}

module "redis" {
  source = "../../modules/redis"

  environment  = var.environment
  key_vault_id = module.keyvault.id
  secret_name  = "atpost-${var.environment}-redis"
  memory_limit = "2Gi"
}

module "data_platform" {
  source = "../../modules/data-platform"

  environment           = var.environment
  location              = module.resource_group.location
  key_vault_id          = module.keyvault.id
  scylla_developer_mode = false
  scylla_secret_name    = "atpost-${var.environment}-scylla"
  redpanda_secret_name  = "atpost-${var.environment}-redpanda"
  minio_secret_name     = "atpost-${var.environment}-minio"
  minio_mode            = "distributed"
  minio_replicas        = 4
  redpanda_replicas     = 3
}

output "key_vault_uri" { value = module.keyvault.uri }
output "eso_client_id" { value = module.external_secrets.identity_client_id }
output "postgres_fqdn" { value = module.postgres.fqdn }
output "redis_hostname" { value = module.redis.hostname }
output "kafka_brokers" { value = module.data_platform.kafka_brokers }
