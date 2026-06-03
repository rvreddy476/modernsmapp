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

output "eks_cluster_name" { value = module.eks.cluster_name }
output "eks_cluster_endpoint" { value = module.eks.cluster_endpoint }
output "eks_oidc_provider_arn" { value = module.eks.oidc_provider_arn }
output "aurora_cluster_endpoint" { value = module.aurora.cluster_endpoint }
output "aurora_reader_endpoint" { value = module.aurora.reader_endpoint }
output "aurora_master_secret_arn" { value = module.aurora.master_secret_arn }
output "msk_bootstrap_brokers" { value = module.msk.bootstrap_brokers_sasl_iam }
output "msk_client_iam_policy_arn" { value = module.msk.client_iam_policy_arn }
