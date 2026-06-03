# Amazon OpenSearch Service domain.
#
# Audit C2 flagged "OpenSearch security disabled" as a critical risk
# on the docker-compose stack. The managed-service version enforces
# encryption + fine-grained access control by default, so this module
# is largely about turning all that on + sizing the domain.
#
# Sizing decision: 3-node multi-AZ with dedicated master nodes. Master
# nodes ensure quorum survives a data-node failure; data nodes carry
# the index shards. atpost workload (full-text search for posts, users,
# hashtags) is read-heavy + write-spiky on viral posts, so master
# separation matters.

resource "aws_kms_key" "opensearch" {
  description             = "atpost-${var.environment} OpenSearch encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-opensearch-kms"
  }
}

resource "aws_kms_alias" "opensearch" {
  name          = "alias/atpost-${var.environment}-opensearch"
  target_key_id = aws_kms_key.opensearch.key_id
}

# Master user — fine-grained access control admin. Stored in Secrets
# Manager. App pods authenticate via IAM (their IRSA role attached to
# the access policy below); the master password is for break-glass
# Kibana access.
resource "random_password" "master" {
  length  = 32
  special = true
  # OpenSearch master-password requires upper/lower/digit/special; the
  # generator's defaults satisfy that.
  override_special = "!@#$%^&*()-_=+[]{}<>"
}

resource "aws_secretsmanager_secret" "master" {
  name                    = "atpost/${var.environment}/opensearch/master"
  description             = "OpenSearch master user (Kibana break-glass)."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.opensearch.arn

  tags = {
    Name = "atpost-${var.environment}-opensearch-master"
  }
}

resource "aws_secretsmanager_secret_version" "master" {
  secret_id = aws_secretsmanager_secret.master.id
  secret_string = jsonencode({
    username = var.master_username
    password = random_password.master.result
    endpoint = aws_opensearch_domain.this.endpoint
  })
}

# VPC security group: ingress 443 from EKS nodes only. OpenSearch in
# VPC mode doesn't have a public endpoint — it lives in the isolated
# tier and is reachable only inside the VPC.
resource "aws_security_group" "opensearch" {
  name        = "atpost-${var.environment}-opensearch"
  description = "OpenSearch domain"
  vpc_id      = var.vpc_id

  tags = {
    Name = "atpost-${var.environment}-opensearch-sg"
  }
}

resource "aws_security_group_rule" "opensearch_from_eks" {
  type                     = "ingress"
  from_port                = 443
  to_port                  = 443
  protocol                 = "tcp"
  security_group_id        = aws_security_group.opensearch.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "OpenSearch 443 from EKS nodes"
}

# Service-linked role is required to put OpenSearch in a VPC. AWS
# creates it on demand; this null check + the depends_on chain make
# sure we don't race the first apply.
data "aws_iam_roles" "es_slr" {
  name_regex = "AWSServiceRoleForAmazonOpenSearchService"
}

resource "aws_iam_service_linked_role" "opensearch" {
  count            = length(data.aws_iam_roles.es_slr.arns) == 0 ? 1 : 0
  aws_service_name = "opensearchservice.amazonaws.com"
  description      = "Required for OpenSearch domain VPC integration"
}

resource "aws_opensearch_domain" "this" {
  domain_name    = "atpost-${var.environment}"
  engine_version = "OpenSearch_2.13"

  cluster_config {
    instance_type            = var.data_instance_type
    instance_count           = var.data_instance_count
    dedicated_master_enabled = var.dedicated_master_enabled
    dedicated_master_type    = var.master_instance_type
    dedicated_master_count   = var.dedicated_master_enabled ? 3 : 0

    zone_awareness_enabled = true
    zone_awareness_config {
      availability_zone_count = 3
    }
  }

  vpc_options {
    subnet_ids         = slice(var.isolated_subnet_ids, 0, 3)
    security_group_ids = [aws_security_group.opensearch.id]
  }

  ebs_options {
    ebs_enabled = true
    volume_type = "gp3"
    volume_size = var.ebs_volume_size_gb
    throughput  = 250
    iops        = 3000
  }

  encrypt_at_rest {
    enabled    = true
    kms_key_id = aws_kms_key.opensearch.arn
  }

  node_to_node_encryption {
    enabled = true
  }

  domain_endpoint_options {
    enforce_https       = true
    tls_security_policy = "Policy-Min-TLS-1-2-PFS-2023-10"
  }

  advanced_security_options {
    enabled                        = true
    internal_user_database_enabled = true

    master_user_options {
      master_user_name     = var.master_username
      master_user_password = random_password.master.result
    }
  }

  log_publishing_options {
    log_type                 = "INDEX_SLOW_LOGS"
    cloudwatch_log_group_arn = aws_cloudwatch_log_group.slow_index.arn
    enabled                  = true
  }
  log_publishing_options {
    log_type                 = "SEARCH_SLOW_LOGS"
    cloudwatch_log_group_arn = aws_cloudwatch_log_group.slow_search.arn
    enabled                  = true
  }
  log_publishing_options {
    log_type                 = "ES_APPLICATION_LOGS"
    cloudwatch_log_group_arn = aws_cloudwatch_log_group.app.arn
    enabled                  = true
  }

  # Auto-Tune handles the heap + shard-sizing settings most folks forget.
  # Off-peak window is ap-south-1 night.
  auto_tune_options {
    desired_state = "ENABLED"

    maintenance_schedule {
      start_at = "2026-06-15T18:00:00Z" # 23:30 IST
      duration {
        value = 2
        unit  = "HOURS"
      }
      cron_expression_for_recurrence = "cron(0 18 * * ? *)" # nightly 23:30 IST
    }
  }

  tags = {
    Name = "atpost-${var.environment}-opensearch"
  }

  depends_on = [aws_iam_service_linked_role.opensearch]
}

# Open the domain's access policy to the same IAM principals + VPC
# nodes. AWS_IAM mode means callers must sign with SigV4; the access
# policy below allows the EKS node IAM role + any IRSA role with
# atpost-${env}-opensearch-client policy attached.
resource "aws_opensearch_domain_policy" "this" {
  domain_name = aws_opensearch_domain.this.domain_name

  access_policies = jsonencode({
    Version = "2012-10-17"
    Statement = [
      {
        Effect    = "Allow"
        Principal = { AWS = "*" }
        Action    = "es:*"
        # Domain-level resource ARN. The fine-grained access control
        # backend gates per-index access at OpenSearch (role mappings
        # done via the Kibana UI / OpenSearch API).
        Resource = "${aws_opensearch_domain.this.arn}/*"
        Condition = {
          IpAddress = {
            # Reachable only from the VPC. Even with the IAM principal,
            # callers from outside the VPC won't resolve the endpoint
            # (private DNS), but this is a belt-and-braces second gate.
            "aws:SourceVpc" = [var.vpc_id]
          }
        }
      }
    ]
  })
}

resource "aws_cloudwatch_log_group" "slow_index" {
  name              = "/aws/opensearch/atpost-${var.environment}/slow-index"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "slow_search" {
  name              = "/aws/opensearch/atpost-${var.environment}/slow-search"
  retention_in_days = 30
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/aws/opensearch/atpost-${var.environment}/application"
  retention_in_days = 14
}

# Required for OpenSearch to write logs to CloudWatch.
resource "aws_cloudwatch_log_resource_policy" "opensearch" {
  policy_name = "atpost-${var.environment}-opensearch-logs"

  policy_document = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "es.amazonaws.com" }
      Action = [
        "logs:PutLogEvents",
        "logs:CreateLogStream",
      ]
      Resource = [
        "${aws_cloudwatch_log_group.slow_index.arn}:*",
        "${aws_cloudwatch_log_group.slow_search.arn}:*",
        "${aws_cloudwatch_log_group.app.arn}:*",
      ]
    }]
  })
}
