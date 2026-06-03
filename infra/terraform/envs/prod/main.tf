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

output "vpc_id" { value = module.vpc.vpc_id }
output "private_subnet_ids" { value = module.vpc.private_subnet_ids }
output "isolated_subnet_ids" { value = module.vpc.isolated_subnet_ids }
output "ecr_repository_urls" { value = module.ecr.repository_urls }
output "ci_role_arn" { value = module.iam.ci_role_arn }
output "dns_name_servers" { value = module.dns.name_servers }
output "wildcard_cert_arn" { value = module.dns.wildcard_cert_arn }
