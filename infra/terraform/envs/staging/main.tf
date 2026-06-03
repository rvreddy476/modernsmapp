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

# Convenience outputs — surface the bits CI + ops need without
# requiring them to peer into module internals.
output "vpc_id" { value = module.vpc.vpc_id }
output "private_subnet_ids" { value = module.vpc.private_subnet_ids }
output "isolated_subnet_ids" { value = module.vpc.isolated_subnet_ids }
output "ecr_repository_urls" { value = module.ecr.repository_urls }
output "ci_role_arn" { value = module.iam.ci_role_arn }
output "dns_name_servers" { value = module.dns.name_servers }
output "wildcard_cert_arn" { value = module.dns.wildcard_cert_arn }
