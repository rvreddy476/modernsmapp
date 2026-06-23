# Prod environment — same module composition as staging by design.
#
# Differences from staging:
#  - 3 NAT gateways (one per AZ) — HA over cost
#  - DNS zone is aws.cleestudio.com (the apex of the AWS-side subtree)
#  - github_repos default narrows to the apply-from-main case (set in
#    tfvars; this file doesn't hardcode it)
#
# Anything else that differs from staging should be a per-resource
# variable, not a structural divergence. Drift = regret.

module "vpc" {
  source = "../../modules/vpc"

  environment        = "prod"
  aws_region         = var.aws_region
  vpc_cidr           = "10.30.0.0/16"
  single_nat_gateway = false # HA: one NAT per AZ
}

module "ecr" {
  source = "../../modules/ecr"

  environment  = "prod"
  repositories = var.service_names
}

module "dns" {
  source = "../../modules/dns"

  environment = "prod"
  zone_name   = "aws.cleestudio.com"
}

module "iam" {
  source = "../../modules/iam"

  environment            = "prod"
  github_repos           = var.github_repos
  ecr_repository_arns    = values(module.ecr.repository_arns)
  tfstate_bucket_arn     = var.tfstate_bucket_arn
  tfstate_lock_table_arn = var.tfstate_lock_table_arn
}

module "eks" {
  source = "../../modules/eks"

  environment        = "prod"
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids

  # Prod API endpoint narrowed to operator CIDRs. cluster_endpoint_public_
  # access_cidrs MUST be set in prod.tfvars before first apply — leaving
  # it default 0.0.0.0/0 lights up the API to the internet, IAM-gated
  # but still surface.
  cluster_endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs
  cluster_admin_arns                   = var.cluster_admin_arns

  # Prod sizes — Graviton r7g.xlarge for Scylla, general pool sized for
  # ~30 services + headroom.
  general_node_min          = 6
  general_node_max          = 24
  general_node_desired      = 9
  memory_node_instance_type = "r7g.xlarge"
  memory_node_min           = 3
  memory_node_max           = 6
  memory_node_desired       = 3

  log_retention_days = 90 # PCI / DPDP audit window
}

output "vpc_id" { value = module.vpc.vpc_id }
output "private_subnet_ids" { value = module.vpc.private_subnet_ids }
output "isolated_subnet_ids" { value = module.vpc.isolated_subnet_ids }
output "ecr_repository_urls" { value = module.ecr.repository_urls }
output "ci_role_arn" { value = module.iam.ci_role_arn }
output "dns_name_servers" { value = module.dns.name_servers }
output "wildcard_cert_arn" { value = module.dns.wildcard_cert_arn }
module "aurora" {
  source = "../../modules/aurora"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  instance_class        = "db.r7g.large" # bump to xlarge under sustained write load
  create_reader         = true
  backup_retention_days = 30   # DPDP + PCI evidence window
  deletion_protection   = true # belt-and-braces against `terraform destroy`
  apply_immediately     = false
}

module "msk" {
  source = "../../modules/msk"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id
}

module "elasticache" {
  source = "../../modules/elasticache"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  node_type               = "cache.r7g.large" # bump to xlarge if cache pressure grows
  num_replicas            = 2                  # primary + 2 replicas across 3 AZs
  snapshot_retention_days = 5
  apply_immediately       = false
}

module "opensearch" {
  source = "../../modules/opensearch"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  # Defaults already match prod sizing (r6g.large data × 3,
  # m6g.large masters × 3, 100GB gp3).
}

module "media" {
  source = "../../modules/media"

  environment = "prod"
  cors_allowed_origins = [
    "https://cleestudio.com",
    "https://app.cleestudio.com",
  ]
  cloudfront_price_class = "PriceClass_200" # incl. India + Asia POPs
}

module "waf" {
  source = "../../modules/waf"

  environment = "prod"
  # Set the gateway Ingress wafv2-acl-arn annotation in
  # deploy/services/api-gateway/values-prod.yaml to module.waf.web_acl_arn
  # (output below) after the first apply.
}

# ─── In-cluster tooling — see staging/main.tf for the two-apply note ─

module "external_secrets" {
  source = "../../modules/external-secrets"

  environment       = "prod"
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

  environment       = "prod"
  aws_region        = var.aws_region
  vpc_id            = module.vpc.vpc_id
  cluster_name      = module.eks.cluster_name
  oidc_provider_arn = module.eks.oidc_provider_arn
}

module "scylla" {
  source = "../../modules/scylla"

  environment        = "prod"
  availability_zones = module.vpc.availability_zones

  # Prod defaults already match r7g.xlarge sizing — see
  # modules/scylla/variables.tf (3 vCPU req/limit, 28 Gi RAM, 100 GB).
}

module "argocd" {
  source = "../../modules/argocd"

  environment         = "prod"
  ingress_scheme      = "internal" # prod UI is VPN-gated
  argocd_hostname     = "argocd.aws.cleestudio.com"
  acm_certificate_arn = module.dns.wildcard_cert_arn

  applicationset_manifest_path = "${path.root}/../../../deploy/argocd/applicationset.yaml"
}

module "aurora_bootstrap" {
  source = "../../modules/aurora-bootstrap"

  environment               = "prod"
  master_secret_name        = "atpost/prod/aurora/master"
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name

  depends_on = [module.aurora, module.external_secrets]
}

module "observability" {
  source = "../../modules/observability"

  environment            = "prod"
  grafana_hostname       = "grafana.aws.cleestudio.com"
  grafana_ingress_scheme = "internal" # VPN-gated like ArgoCD
  acm_certificate_arn    = module.dns.wildcard_cert_arn

  # Prod defaults already match (100Gi PVC, 70GB soft retention, 15d).
}

resource "random_id" "tempo_suffix" {
  byte_length = 4
}

resource "random_id" "loki_suffix" {
  byte_length = 4
}

module "tempo" {
  source = "../../modules/tempo"

  environment       = "prod"
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.tempo_suffix.hex
  retention_days    = 14 # prod traces — 2-week incident-postmortem window

  depends_on = [module.observability]
}

module "loki" {
  source = "../../modules/loki"

  environment       = "prod"
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.loki_suffix.hex
  retention_days    = 30 # prod logs — DPDP audit window

  depends_on = [module.observability]
}

module "karpenter" {
  source = "../../modules/karpenter"

  environment        = "prod"
  cluster_name       = module.eks.cluster_name
  oidc_provider_arn  = module.eks.oidc_provider_arn
  private_subnet_ids = module.vpc.private_subnet_ids

  # Prod: on-demand-only by default to keep availability strong.
  # Override to ["spot","on-demand"] later when SLO data shows the
  # interruption tolerance is fine.
  capacity_types = ["on-demand"]
  cpu_limit      = "2000"
  memory_limit   = "4000Gi"
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
output "waf_web_acl_arn" { value = module.waf.web_acl_arn }
