# Prod environment — same module composition as staging by design, plus
# the pieces the first production deployment needs (certificates, SES,
# buckets, secret shells, ops baseline: see the other *.tf files here).
#
# Sizes come from variables.tf with the LEAN defaults (plan §2.1). Growing
# to the full design is a tfvars change, not an edit here.
#
# Anything else that differs from staging should be a per-resource
# variable, not a structural divergence. Drift = regret.

locals {
  all_hostnames = concat([var.domain], [for s in var.public_subdomains : "${s}.${var.domain}"])
  media_domain  = "${var.media_subdomain}.${var.domain}"
}

module "vpc" {
  source = "../../modules/vpc"

  environment        = "prod"
  aws_region         = var.aws_region
  vpc_cidr           = "10.30.0.0/16"
  single_nat_gateway = var.single_nat_gateway # lean: one NAT; false = one per AZ
}

module "ecr" {
  source = "../../modules/ecr"

  environment = "prod"
  # Server images plus the second images some services ship (media-worker):
  # same repo settings, lifecycle policy and CI push grant for all of them.
  repositories = concat(var.service_names, var.worker_image_names)
}

# Container images for the web (atpost/web, atpost/admin-console) and any
# retained Multi-Zone apps.
module "ecr_web" {
  source = "../../modules/ecr"

  environment  = "prod"
  repositories = concat(var.web_image_names, var.web_zone_names)
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
  ci_github_subjects     = var.ci_github_subjects
  ecr_repository_arns    = concat(values(module.ecr.repository_arns), values(module.ecr_web.repository_arns))
  tfstate_bucket_arn     = var.tfstate_bucket_arn
  tfstate_lock_table_arn = var.tfstate_lock_table_arn

  create_terraform_apply_role = var.create_terraform_apply_role
  apply_github_subjects       = var.terraform_apply_github_subjects
}

module "eks" {
  source = "../../modules/eks"

  environment        = "prod"
  vpc_id             = module.vpc.vpc_id
  private_subnet_ids = module.vpc.private_subnet_ids

  cluster_version = var.eks_cluster_version

  # Prod API endpoint narrowed to operator CIDRs. cluster_endpoint_public_
  # access_cidrs MUST be set in prod.tfvars before first apply — leaving
  # it default 0.0.0.0/0 lights up the API to the internet, IAM-gated
  # but still surface.
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
  memory_node_instance_type  = var.memory_node_instance_type
  memory_node_min            = var.memory_node_min
  memory_node_max            = var.memory_node_max
  memory_node_desired        = var.memory_node_desired

  log_retention_days = 90 # PCI / DPDP audit window
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

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version        = var.aurora_engine_version
  serverless_enabled    = var.aurora_serverless_enabled
  serverless_min_acu    = var.aurora_min_acu
  serverless_max_acu    = var.aurora_max_acu
  instance_class        = var.aurora_instance_class
  create_reader         = var.aurora_create_reader
  backup_retention_days = var.aurora_backup_retention_days
  deletion_protection   = true  # belt-and-braces against `terraform destroy`
  apply_immediately     = false # size changes wait for the maintenance window
}

module "msk" {
  source = "../../modules/msk"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  private_subnet_ids         = module.vpc.private_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  kafka_version             = var.msk_kafka_version
  broker_instance_type      = var.msk_broker_instance_type
  number_of_broker_nodes    = var.msk_number_of_broker_nodes
  broker_ebs_volume_size_gb = var.msk_broker_ebs_volume_size_gb
  default_partitions        = var.msk_topic_partitions
  enable_iam_auth           = false # SCRAM only — the services' client speaks SCRAM, not AWS_MSK_IAM
}

module "elasticache" {
  source = "../../modules/elasticache"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version          = var.elasticache_engine_version
  node_type               = var.elasticache_node_type
  num_replicas            = var.elasticache_num_replicas # lean: primary + 1 replica
  snapshot_retention_days = 5
  apply_immediately       = false
}

module "opensearch" {
  source = "../../modules/opensearch"

  environment                = "prod"
  vpc_id                     = module.vpc.vpc_id
  isolated_subnet_ids        = module.vpc.isolated_subnet_ids
  eks_node_security_group_id = module.eks.node_security_group_id

  engine_version           = var.opensearch_engine_version
  data_instance_type       = var.opensearch_data_instance_type
  data_instance_count      = var.opensearch_data_instance_count
  dedicated_master_enabled = var.opensearch_dedicated_master_enabled
  ebs_volume_size_gb       = var.opensearch_ebs_volume_size_gb
  ebs_throughput_mibps     = 125 # gp3 baseline; raise with the node size
}

module "media" {
  source = "../../modules/media"

  environment = "prod"
  cors_allowed_origins = [
    "https://${var.domain}",
    "https://app.${var.domain}",
  ]
  cloudfront_price_class = "PriceClass_200" # incl. India + Asia POPs

  # Custom domain only after the us-east-1 certificate is ISSUED (see
  # variables.tf media_custom_domain_enabled and certificates.tf).
  custom_domain                 = var.media_custom_domain_enabled ? local.media_domain : null
  acm_certificate_arn_us_east_1 = var.media_custom_domain_enabled ? module.media_cdn_cert.certificate_arn : null
}

# Edge WAF for the api-gateway / chat-ws / web ALBs. Set the Ingress
# wafv2-acl-arn annotation in deploy/services/api-gateway/values-prod.yaml
# to waf_web_acl_arn (output) after the first apply.
module "waf" {
  source = "../../modules/waf"

  environment = "prod"
  name        = "edge"
}

# Separate ACL for the payments webhook ingress (payments values reference
# atpost-prod-psp-webhook by name). Lower rate ceiling: the PSP sends a few
# requests per order, not a browser's worth.
module "waf_psp_webhook" {
  source = "../../modules/waf"

  environment         = "prod"
  name                = "psp-webhook"
  rate_limit_per_5min = 1000
}

module "codeartifact" {
  source = "../../modules/codeartifact"

  environment = "prod"
  aws_region  = var.aws_region
}

# Let the GitHub-OIDC CI role read/publish @atpost/* to CodeArtifact.
resource "aws_iam_role_policy_attachment" "ci_codeartifact" {
  role       = module.iam.ci_role_name
  policy_arn = module.codeartifact.policy_arn
}

module "auth_keys" {
  source = "../../modules/auth-keys"

  environment = "prod"
  # manage_values=true generates the RS256 keypair + JWT/internal secrets into
  # Secrets Manager (values land in encrypted TF state — see module docs). Flip
  # to false to keep the signing key out of state and populate by hand.
}

# Commerce address-PII key. The principal is commerce-service's IRSA role
# from services-irsa.tf (one role per ServiceAccount); the module's own role
# is not created.
module "commerce_pii_kms" {
  source = "../../modules/commerce-pii-kms"

  env                       = "prod"
  cluster_oidc_provider_arn = module.eks.oidc_provider_arn
  cluster_oidc_issuer       = replace(module.eks.oidc_provider_url, "https://", "")
  create_irsa_role          = false
  principal_role_arns       = [module.service_irsa["commerce-service"].role_arn]
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
    module.auth_keys.kms_key_arn,
    module.msk.kms_key_arn,
    module.service_secrets.kms_key_arn,
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

module "metrics_server" {
  source = "../../modules/metrics-server"

  depends_on = [module.eks]
}

module "scylla" {
  source = "../../modules/scylla"

  environment        = "prod"
  availability_zones = module.vpc.availability_zones

  # Lean sizing — matches the memory node group (m7g.large = 2 vCPU, 8 GB).
  cpu_per_replica     = var.scylla_cpu_per_replica
  memory_per_replica  = var.scylla_memory_per_replica
  storage_per_replica = var.scylla_storage_per_replica
}

module "argocd" {
  source = "../../modules/argocd"

  environment         = "prod"
  ingress_scheme      = "internal" # prod UI is VPN-gated
  argocd_hostname     = "argocd.aws.cleestudio.com"
  acm_certificate_arn = module.dns.wildcard_cert_arn

  applicationset_manifest_path = "${path.root}/../../../deploy/argocd/applicationset.yaml"
  aws_account_id               = data.aws_caller_identity.current.account_id
  # Terraform owns AppProject `atpost`; deploy/argocd/project.yaml was removed so there is one owner.
  allowed_source_repos = ["https://github.com/rvreddy476/modernsmapp.git"]
}

module "aurora_bootstrap" {
  source = "../../modules/aurora-bootstrap"

  environment               = "prod"
  master_secret_name        = "atpost/prod/aurora/master"
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name
  databases                 = var.aurora_databases
  extensions                = var.aurora_extensions

  depends_on = [module.aurora, module.external_secrets]
}

# Kafka topics — a one-shot Job (the AWS provider cannot create topics).
module "msk_topics" {
  source = "../../modules/msk/topics-job"

  environment               = "prod"
  bootstrap_brokers         = module.msk.bootstrap_brokers_sasl_scram
  scram_secret_name         = module.msk.scram_secret_name
  cluster_secret_store_name = module.external_secrets.cluster_secret_store_name
  partitions                = var.msk_topic_partitions
  replication_factor        = min(2, var.msk_number_of_broker_nodes)

  depends_on = [module.msk, module.external_secrets]
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

  chart_version = var.karpenter_chart_version

  # Prod: on-demand-only by default to keep availability strong.
  # Override to ["spot","on-demand"] later when SLO data shows the
  # interruption tolerance is fine.
  capacity_types = var.karpenter_capacity_types
  cpu_limit      = "200" # lean ceiling (≈ 50 × m7g.xlarge); raise with traffic
  memory_limit   = "800Gi"
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
  description = "S3_BUCKET for media-service and media-worker values (the audit's naming mismatch: values said atpost-prod-media, the bucket has a random suffix)."
}
output "media_cloudfront_domain" { value = module.media.cloudfront_domain_name }
output "media_cdn_base_url" { value = module.media.cdn_base_url }
output "media_cloudfront_key_pair_id" { value = module.media.cloudfront_key_pair_id }
output "media_cloudfront_signing_secret_arn" { value = module.media.cloudfront_signing_secret_arn }
output "media_client_iam_policy_arn" { value = module.media.client_iam_policy_arn }
output "waf_web_acl_arn" { value = module.waf.web_acl_arn }
output "waf_psp_webhook_acl_arn" { value = module.waf_psp_webhook.web_acl_arn }
output "auth_keys_secret_name" { value = module.auth_keys.secret_name }
output "auth_keys_secret_arn" { value = module.auth_keys.secret_arn }
output "commerce_pii_kms_key_id" { value = module.commerce_pii_kms.key_id }
output "commerce_pii_kms_key_arn" { value = module.commerce_pii_kms.key_arn }
output "codeartifact_npm_endpoint" { value = module.codeartifact.npm_endpoint }
output "codeartifact_domain" { value = module.codeartifact.domain }
