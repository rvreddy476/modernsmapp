# Staging environment — composes the Phase-0 modules.
#
# Staging deliberately mirrors prod's SHAPE (3-AZ, full module set) so
# a change that's safe here is safe there. The differences are:
#  - single NAT gateway (cost; one less HA layer that breaks in dev)
#  - DNS zone is staging.aws.cleestudio.com (cleanly separable)
#  - smaller everything (instance sizes, replica counts) — those
#    knobs land in Phase 2 when EKS / RDS / etc. arrive.

module "vpc" {
  source = "../../modules/vpc"

  environment        = "staging"
  aws_region         = var.aws_region
  vpc_cidr           = "10.20.0.0/16"
  single_nat_gateway = true # cost; flip to false to match prod HA shape
}

module "ecr" {
  source = "../../modules/ecr"

  environment  = "staging"
  repositories = var.service_names
}

module "dns" {
  source = "../../modules/dns"

  environment = "staging"
  zone_name   = "staging.aws.cleestudio.com"
}

module "iam" {
  source = "../../modules/iam"

  environment            = "staging"
  github_repos           = var.github_repos
  ecr_repository_arns    = values(module.ecr.repository_arns)
  tfstate_bucket_arn     = var.tfstate_bucket_arn
  tfstate_lock_table_arn = var.tfstate_lock_table_arn
}

module "eks" {
  source = "../../modules/eks"

  environment        = "staging"
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids

  # Staging API endpoint open — no real user data lives here. Prod
  # narrows to office/VPN CIDRs.
  cluster_endpoint_public_access_cidrs = ["0.0.0.0/0"]
  cluster_admin_arns                   = var.cluster_admin_arns

  # Smaller node groups for staging (cost). Memory tier stays at 3
  # nodes so Scylla RF=3 still works; just smaller instance type.
  general_node_min          = 2
  general_node_max          = 6
  general_node_desired      = 3
  memory_node_instance_type = "r7g.large"
  memory_node_min           = 3
  memory_node_max           = 3
  memory_node_desired       = 3

  log_retention_days = 14
}

# Convenience outputs — surface the bits CI + ops need without
# requiring them to peer into module internals.
output "vpc_id" { value = module.vpc.vpc_id }
output "private_subnet_ids" { value = module.vpc.private_subnet_ids }
output "isolated_subnet_ids" { value = module.vpc.isolated_subnet_ids }
output "ecr_repository_urls" { value = module.ecr.repository_urls }
output "ci_role_arn" { value = module.iam.ci_role_arn }
output "dns_name_servers" { value = module.dns.name_servers }
output "wildcard_cert_arn" { value = module.dns.wildcard_cert_arn }
module "aurora" {
  source = "../../modules/aurora"

  environment                = "staging"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  instance_class        = "db.t4g.medium" # cheap dev tier
  create_reader         = false           # staging: single writer
  backup_retention_days = 7
  deletion_protection   = false # allow tear-down in staging
  apply_immediately     = true  # iterate fast in staging
}

module "msk" {
  source = "../../modules/msk"

  environment                = "staging"
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id
}

module "elasticache" {
  source = "../../modules/elasticache"

  environment                = "staging"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  node_type               = "cache.t4g.medium" # cheap burst tier
  num_replicas            = 1                  # staging: primary + 1 replica
  snapshot_retention_days = 1
  apply_immediately       = true
}

module "opensearch" {
  source = "../../modules/opensearch"

  environment                = "staging"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  data_instance_type       = "t3.small.search"
  data_instance_count      = 1
  dedicated_master_enabled = false
  ebs_volume_size_gb       = 30
}

module "media" {
  source = "../../modules/media"

  environment = "staging"
  cors_allowed_origins = [
    "https://staging.cleestudio.com",
    "http://localhost:3000", # dev composer
  ]
  cloudfront_price_class = "PriceClass_100" # cheap; staging traffic is dev-only
}

# ─── In-cluster tooling (helm + kubernetes providers) ───────────────
# These wait on the EKS cluster — see the two-apply bootstrap note in
# the README. On a fresh apply they fail; re-applying after EKS is up
# installs cleanly.

module "external_secrets" {
  source = "../../modules/external-secrets"

  environment       = "staging"
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url

  kms_key_arns = [
    module.aurora.kms_key_arn,
    module.elasticache.kms_key_arn,
    module.opensearch.kms_key_arn,
    module.media.kms_key_arn,
  ]
}

module "aws_lb_controller" {
  source = "../../modules/aws-lb-controller"

  environment       = "staging"
  aws_region        = var.aws_region
  vpc_id            = module.vpc.vpc_id
  cluster_name      = module.eks.cluster_name
  oidc_provider_arn = module.eks.oidc_provider_arn
}

module "scylla" {
  source = "../../modules/scylla"

  environment        = "staging"
  availability_zones = module.vpc.availability_zones

  # Staging sizing — matches the staging memory node group
  # (r7g.large = 2 vCPU, 16 GB).
  cpu_per_replica     = "1500m"
  memory_per_replica  = "12Gi"
  storage_per_replica = "30Gi"
}

module "argocd" {
  source = "../../modules/argocd"

  environment         = "staging"
  ingress_scheme      = "internet-facing" # staging UI is public; ACM + admin password gate it
  argocd_hostname     = "argocd.staging.aws.cleestudio.com"
  acm_certificate_arn = module.dns.wildcard_cert_arn

  # Apply the multi-doc ApplicationSet so per-service Applications
  # land on the first reconcile. Relative path from this dir.
  applicationset_manifest_path = "${path.root}/../../../deploy/argocd/applicationset.yaml"
}

module "aurora_bootstrap" {
  source = "../../modules/aurora-bootstrap"

  environment               = "staging"
  master_secret_name        = "atpost/staging/aurora/master"
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name

  depends_on = [module.aurora, module.external_secrets]
}

module "observability" {
  source = "../../modules/observability"

  environment            = "staging"
  grafana_hostname       = "grafana.staging.aws.cleestudio.com"
  grafana_ingress_scheme = "internet-facing"
  acm_certificate_arn    = module.dns.wildcard_cert_arn

  # Smaller storage in staging.
  prometheus_storage_size   = "30Gi"
  prometheus_retention_size = "20GB"
}

# Stable bucket suffix for Tempo + Loki — generated once, reused on
# every plan. Each gets its own to keep teardown surgical.
resource "random_id" "tempo_suffix" {
  byte_length = 4
}

resource "random_id" "loki_suffix" {
  byte_length = 4
}

module "tempo" {
  source = "../../modules/tempo"

  environment       = "staging"
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.tempo_suffix.hex
  retention_days    = 3 # staging traces are cheap; short retention saves S3 cost

  depends_on = [module.observability]
}

module "loki" {
  source = "../../modules/loki"

  environment       = "staging"
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.loki_suffix.hex
  retention_days    = 7

  depends_on = [module.observability]
}

output "eks_cluster_name" { value = module.eks.cluster_name }
output "eks_cluster_endpoint" { value = module.eks.cluster_endpoint }
output "eks_oidc_provider_arn" { value = module.eks.oidc_provider_arn }
output "aurora_cluster_endpoint" { value = module.aurora.cluster_endpoint }
output "aurora_reader_endpoint" { value = module.aurora.reader_endpoint }
output "aurora_master_secret_arn" { value = module.aurora.master_secret_arn }
output "msk_bootstrap_brokers" { value = module.msk.bootstrap_brokers_sasl_iam }
output "msk_client_iam_policy_arn" { value = module.msk.client_iam_policy_arn }
output "elasticache_primary_endpoint" { value = module.elasticache.primary_endpoint }
output "elasticache_reader_endpoint" { value = module.elasticache.reader_endpoint }
output "elasticache_auth_secret_arn" { value = module.elasticache.auth_secret_arn }
output "opensearch_endpoint" { value = module.opensearch.endpoint }
output "opensearch_master_secret_arn" { value = module.opensearch.master_secret_arn }
output "media_bucket_name" { value = module.media.bucket_name }
output "media_cloudfront_domain" { value = module.media.cloudfront_domain_name }
output "media_client_iam_policy_arn" { value = module.media.client_iam_policy_arn }
