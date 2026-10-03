# QA environment — the new AWS account (contract 3 Oct 2026: this account is
# QA for good; production gets a separate account and envs/prod). Same module
# composition as envs/prod, at the QA size: single copies, no high
# availability, 2 AZs, 1 NAT gateway. The other *.tf files here hold the
# certificates, SES, buckets, secret shells, IRSA roles and ops baseline.
#
# Every size is a variable (variables.tf, qa.tfvars.example). A difference
# from prod should be a variable value, not a structural divergence.

locals {
  public_hostnames = [for s in var.public_subdomains : "${s}.${var.domain}"]
  media_domain     = "${var.media_subdomain}.${var.domain}"

  # Scylla racks and the memory node group: the first N AZs / subnets.
  scylla_azs     = slice(module.vpc.availability_zones, 0, var.scylla_node_count)
  scylla_subnets = slice(module.vpc.private_subnet_ids, 0, var.scylla_node_count)

  ci_github_subjects = length(var.ci_github_subjects) > 0 ? var.ci_github_subjects : concat(
    [for b in var.ci_github_branches : "repo:${var.github_repository}:ref:refs/heads/${b}"],
    ["repo:${var.github_repository}:pull_request"],
  )
  terraform_apply_github_subjects = length(var.terraform_apply_github_subjects) > 0 ? var.terraform_apply_github_subjects : [
    "repo:${var.github_repository}:environment:${var.terraform_apply_github_environment}",
  ]
}

module "vpc" {
  source = "../../modules/vpc"

  environment        = var.environment
  aws_region         = var.aws_region
  vpc_cidr           = var.vpc_cidr
  az_count           = var.az_count
  single_nat_gateway = true # one NAT gateway (QA size)
}

module "ecr" {
  source = "../../modules/ecr"

  environment  = var.environment
  repositories = concat(var.service_names, var.worker_image_names)
}

module "ecr_web" {
  source = "../../modules/ecr"

  environment  = var.environment
  repositories = var.web_image_names
}

# Internal zone for ArgoCD / Grafana (internal ALBs) + its wildcard
# certificate. The certificate is validated inside this zone, so the
# apply waits until Cloudflare delegates the zone: README.md step 4 adds
# the NS records BEFORE the full pass 1.
module "dns" {
  source = "../../modules/dns"

  environment = var.environment
  zone_name   = var.internal_zone_name
}

module "iam" {
  source = "../../modules/iam"

  environment            = var.environment
  github_repos           = [var.github_repository] # unused: ci_github_subjects is always set
  ci_github_subjects     = local.ci_github_subjects
  ecr_repository_arns    = concat(values(module.ecr.repository_arns), values(module.ecr_web.repository_arns))
  tfstate_bucket_arn     = var.tfstate_bucket_arn
  tfstate_lock_table_arn = var.tfstate_lock_table_arn

  create_terraform_apply_role = var.create_terraform_apply_role
  apply_github_subjects       = local.terraform_apply_github_subjects
}

module "eks" {
  source = "../../modules/eks"

  environment        = var.environment
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids

  cluster_version = var.eks_cluster_version

  cluster_endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs
  cluster_admin_arns                   = var.cluster_admin_arns

  general_node_instance_type = var.general_node_instance_type
  general_node_min           = var.general_node_min
  general_node_max           = var.general_node_max
  general_node_desired       = var.general_node_desired
  system_node_instance_type  = var.system_node_instance_type
  system_node_min            = var.system_node_min
  system_node_max            = var.system_node_max
  system_node_desired        = var.system_node_desired

  # One memory node per Scylla rack, pinned to the racks' subnets.
  memory_node_instance_type = var.memory_node_instance_type
  memory_node_min           = var.scylla_node_count
  memory_node_max           = var.scylla_node_count
  memory_node_desired       = var.scylla_node_count
  memory_node_subnet_ids    = local.scylla_subnets

  log_retention_days = 30
}

output "vpc_id" { value = module.vpc.vpc_id }
output "private_subnet_ids" { value = module.vpc.private_subnet_ids }
output "isolated_subnet_ids" { value = module.vpc.isolated_subnet_ids }
output "ecr_repository_urls" { value = merge(module.ecr.repository_urls, module.ecr_web.repository_urls) }
output "ci_role_arn" { value = module.iam.ci_role_arn }
output "terraform_apply_role_arn" { value = module.iam.terraform_apply_role_arn }
output "dns_name_servers" { value = module.dns.name_servers }
output "wildcard_cert_arn" { value = module.dns.wildcard_cert_arn }

module "aurora" {
  source = "../../modules/aurora"

  environment                = var.environment
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version        = var.aurora_engine_version
  serverless_enabled    = true
  serverless_min_acu    = var.aurora_min_acu
  serverless_max_acu    = var.aurora_max_acu
  create_reader         = false # one writer, no reader (QA size)
  backup_retention_days = var.aurora_backup_retention_days
  deletion_protection   = true # QA data is still precious to testers
  apply_immediately     = true # QA: no waiting for the maintenance window
}

module "msk" {
  source = "../../modules/msk"

  environment                = var.environment
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  kafka_version              = var.msk_kafka_version
  broker_instance_type       = var.msk_broker_instance_type
  number_of_broker_nodes     = var.msk_number_of_broker_nodes
  broker_ebs_volume_size_gb  = var.msk_broker_ebs_volume_size_gb
  default_partitions         = var.msk_topic_partitions
  default_replication_factor = var.msk_topic_replication_factor
  min_insync_replicas        = var.msk_min_insync_replicas
  enable_iam_auth            = false # SCRAM only — the services' client speaks SCRAM
}

module "elasticache" {
  source = "../../modules/elasticache"

  environment                = var.environment
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version          = var.elasticache_engine_version
  node_type               = var.elasticache_node_type
  num_replicas            = var.elasticache_num_replicas # 0: single node, TLS + AUTH still on
  snapshot_retention_days = 1
  apply_immediately       = true
}

module "opensearch" {
  source = "../../modules/opensearch"

  environment                = var.environment
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version           = var.opensearch_engine_version
  data_instance_type       = var.opensearch_data_instance_type
  data_instance_count      = 1 # single node, single AZ
  dedicated_master_enabled = false
  ebs_volume_size_gb       = var.opensearch_ebs_volume_size_gb
  ebs_throughput_mibps     = 125 # gp3 baseline
}

module "media" {
  source = "../../modules/media"

  environment = var.environment
  cors_allowed_origins = [
    "https://${local.public_hostnames[0]}", # https://qa.cleestudio.com
  ]
  cloudfront_price_class = "PriceClass_200" # incl. India + Asia POPs

  # Custom domain only after the us-east-1 certificate is ISSUED
  # (variables.tf media_custom_domain_enabled, certificates.tf).
  custom_domain                 = var.media_custom_domain_enabled ? local.media_domain : null
  acm_certificate_arn_us_east_1 = var.media_custom_domain_enabled ? module.media_cdn_cert.certificate_arn : null
}

# One web ACL for every public ingress (api-gateway, chat-ws-gateway, the
# payments webhook, web, admin console) — the load-balancer controller
# refuses an ingress group whose members name different ACLs, and QA has no
# separate webhook ALB. waf_psp_webhook_acl_arn (output, same name as prod)
# therefore points at this ACL too.
module "waf" {
  source = "../../modules/waf"

  environment = var.environment
  name        = "edge"
}

module "codeartifact" {
  source = "../../modules/codeartifact"

  environment = var.environment
  aws_region  = var.aws_region
}

resource "aws_iam_role_policy_attachment" "ci_codeartifact" {
  role       = module.iam.ci_role_name
  policy_arn = module.codeartifact.policy_arn
}

module "auth_keys" {
  source = "../../modules/auth-keys"

  environment = var.environment # atpost/qa/platform-auth, generated
}

module "commerce_pii_kms" {
  source = "../../modules/commerce-pii-kms"

  env                       = var.environment
  cluster_oidc_provider_arn = module.eks.oidc_provider_arn
  cluster_oidc_issuer       = replace(module.eks.oidc_provider_url, "https://", "")
  create_irsa_role          = false
  principal_role_arns       = [module.service_irsa["commerce-service"].role_arn]
}

# ─── In-cluster tooling (pass 2) ─────────────────────────────────────

module "external_secrets" {
  source = "../../modules/external-secrets"

  environment       = var.environment
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url

  kms_key_arns = [
    module.aurora.kms_key_arn,
    module.elasticache.kms_key_arn,
    module.opensearch.kms_key_arn,
    module.media.kms_key_arn,
    module.auth_keys.kms_key_arn,
    module.msk.kms_key_arn,
    module.service_secrets.kms_key_arn,
  ]
}

module "aws_lb_controller" {
  source = "../../modules/aws-lb-controller"

  environment       = var.environment
  aws_region        = var.aws_region
  vpc_id            = module.vpc.vpc_id
  cluster_name      = module.eks.cluster_name
  oidc_provider_arn = module.eks.oidc_provider_arn
}

module "metrics_server" {
  source = "../../modules/metrics-server"

  depends_on = [module.eks]
}

module "scylla" {
  source = "../../modules/scylla"

  environment        = var.environment
  availability_zones = local.scylla_azs # one rack per AZ → 1 node in QA

  operator_replicas   = 1
  cpu_per_replica     = var.scylla_cpu_per_replica
  memory_per_replica  = var.scylla_memory_per_replica
  storage_per_replica = var.scylla_storage_per_replica
}

module "argocd" {
  source = "../../modules/argocd"

  environment         = var.environment
  replicas            = 1
  ingress_scheme      = "internal" # UI through `kubectl port-forward` (README.md)
  argocd_hostname     = "argocd.${var.internal_zone_name}"
  acm_certificate_arn = module.dns.wildcard_cert_arn

  # Written by the values lane; tracks branch `qa`, sync manual at first.
  applicationset_manifest_path = "${path.root}/../../../deploy/argocd/applicationset-qa.yaml"
  aws_account_id               = data.aws_caller_identity.current.account_id
  allowed_source_repos         = ["https://github.com/rvreddy476/modernsmapp.git"]
}

module "aurora_bootstrap" {
  source = "../../modules/aurora-bootstrap"

  environment               = var.environment
  master_secret_name        = "atpost/${var.environment}/aurora/master"
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name
  databases                 = var.aurora_databases
  extensions                = var.aurora_extensions

  depends_on = [module.aurora, module.external_secrets]
}

# Kafka topics — a one-shot Job (the AWS provider cannot create topics).
module "msk_topics" {
  source = "../../modules/msk/topics-job"

  environment               = var.environment
  bootstrap_brokers         = module.msk.bootstrap_brokers_sasl_scram
  scram_secret_name         = module.msk.scram_secret_name
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name
  partitions                = var.msk_topic_partitions
  replication_factor        = min(var.msk_topic_replication_factor, var.msk_number_of_broker_nodes)
  topic_configs = {
    "retention.ms"        = "604800000" # 7 days
    "min.insync.replicas" = tostring(var.msk_min_insync_replicas)
  }

  depends_on = [module.msk, module.external_secrets]
}

module "observability" {
  source = "../../modules/observability"

  environment            = var.environment
  grafana_hostname       = "grafana.${var.internal_zone_name}"
  grafana_ingress_scheme = "internal"
  acm_certificate_arn    = module.dns.wildcard_cert_arn

  prometheus_replicas       = 1
  prometheus_retention      = "7d"
  prometheus_retention_size = "16GB"
  prometheus_storage_size   = "20Gi"
  alertmanager_replicas     = 1
}

resource "random_id" "tempo_suffix" {
  byte_length = 4
}

resource "random_id" "loki_suffix" {
  byte_length = 4
}

module "tempo" {
  source = "../../modules/tempo"

  environment       = var.environment
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.tempo_suffix.hex
  retention_days    = 7

  depends_on = [module.observability]
}

module "loki" {
  source = "../../modules/loki"

  environment       = var.environment
  aws_region        = var.aws_region
  oidc_provider_arn = module.eks.oidc_provider_arn
  oidc_provider_url = module.eks.oidc_provider_url
  random_suffix     = random_id.loki_suffix.hex
  retention_days    = 14

  depends_on = [module.observability]
}

module "karpenter" {
  source = "../../modules/karpenter"

  environment        = var.environment
  cluster_name       = module.eks.cluster_name
  oidc_provider_arn  = module.eks.oidc_provider_arn
  private_subnet_ids = module.vpc.private_subnet_ids

  chart_version  = var.karpenter_chart_version
  capacity_types = var.karpenter_capacity_types # Graviton on-demand
  cpu_limit      = var.karpenter_cpu_limit
  memory_limit   = var.karpenter_memory_limit
}

output "eks_cluster_name" { value = module.eks.cluster_name }
output "eks_cluster_endpoint" { value = module.eks.cluster_endpoint }
output "eks_oidc_provider_arn" { value = module.eks.oidc_provider_arn }
output "aurora_cluster_endpoint" { value = module.aurora.cluster_endpoint }
output "aurora_reader_endpoint" { value = module.aurora.reader_endpoint }
output "aurora_master_secret_arn" { value = module.aurora.master_secret_arn }
output "aurora_databases" { value = var.aurora_databases }
output "msk_bootstrap_brokers" { value = module.msk.bootstrap_brokers_sasl_scram }
output "msk_scram_secret_arn" { value = module.msk.scram_secret_arn }
output "msk_scram_secret_name" { value = module.msk.scram_secret_name }
output "msk_topics" { value = module.msk_topics.topics }
output "elasticache_primary_endpoint" { value = module.elasticache.primary_endpoint }
output "elasticache_reader_endpoint" { value = module.elasticache.reader_endpoint }
output "elasticache_auth_secret_arn" { value = module.elasticache.auth_secret_arn }
output "opensearch_endpoint" { value = module.opensearch.endpoint }
output "opensearch_master_secret_arn" { value = module.opensearch.master_secret_arn }
output "media_bucket_name" {
  value       = module.media.bucket_name
  description = "S3_BUCKET for media-service and media-worker values-qa (the bucket name has a random suffix)."
}
output "media_cloudfront_domain" { value = module.media.cloudfront_domain_name }
output "media_cdn_base_url" { value = module.media.cdn_base_url }
output "media_cloudfront_key_pair_id" { value = module.media.cloudfront_key_pair_id }
output "media_cloudfront_signing_secret_arn" { value = module.media.cloudfront_signing_secret_arn }
output "media_client_iam_policy_arn" { value = module.media.client_iam_policy_arn }
output "waf_web_acl_arn" { value = module.waf.web_acl_arn }
output "waf_psp_webhook_acl_arn" {
  value       = module.waf.web_acl_arn
  description = "QA has one web ACL; the payments webhook ingress uses it too (same name as the prod output so scripts stay uniform)."
}
output "auth_keys_secret_name" { value = module.auth_keys.secret_name }
output "auth_keys_secret_arn" { value = module.auth_keys.secret_arn }
output "commerce_pii_kms_key_id" { value = module.commerce_pii_kms.key_id }
output "commerce_pii_kms_key_arn" { value = module.commerce_pii_kms.key_arn }
output "codeartifact_npm_endpoint" { value = module.codeartifact.npm_endpoint }
output "codeartifact_domain" { value = module.codeartifact.domain }
