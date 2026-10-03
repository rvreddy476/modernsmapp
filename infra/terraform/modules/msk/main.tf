# MSK Provisioned cluster with SASL/SCRAM + TLS.
#
# History: the first cut of this module was MSK Serverless (IAM auth only).
# The Go Kafka client the services share (segmentio/kafka-go wired in
# shared/kafka) only speaks PLAIN and SCRAM-SHA-256/512, and MSK Serverless
# accepts nothing but AWS_MSK_IAM — so no service could have connected. The
# first-production plan (docs/runbooks/aws-first-production-deployment.md
# §1 item 1, §2.1) moves to MSK Provisioned: two small brokers, SCRAM login,
# TLS on the wire, every topic created explicitly.
#
# Auth model: SASL/SCRAM-SHA-512 over TLS (port 9096). The one application
# user lives in a Secrets Manager secret that MUST be named `AmazonMSK_*`
# and MUST be encrypted with a customer-managed KMS key — both are hard AWS
# requirements for aws_msk_scram_secret_association. IAM auth (port 9098)
# can be switched on beside SCRAM with `enable_iam_auth = true`; the
# services do not use it, so prod leaves it off and the old client IAM
# policy is not created.
#
# Topics: the AWS provider (5.x) has no resource that creates Kafka topics
# inside a cluster, and the cluster is reachable only from inside the VPC.
# `auto.create.topics.enable` is false here on purpose (a typo'd topic name
# would otherwise silently create a 1-partition topic), so topics come from
# the one-shot Kubernetes Job in ./topics-job — apply it in the second
# (in-cluster) pass. The topic list is a variable on THAT module so it stays
# reviewable in PR.
#
# Sizing (lean profile): 2 × kafka.t3.small in two AZs, replication
# factor 2, min.insync.replicas 1. Growing is `broker_instance_type` plus
# `number_of_broker_nodes` (must stay a multiple of the subnet count).

resource "aws_security_group" "msk" {
  name        = "atpost-${var.environment}-msk"
  description = "MSK cluster"
  vpc_id      = var.vpc_id

  tags = {
    Name = "atpost-${var.environment}-msk-sg"
  }
}

# SASL/SCRAM over TLS is 9096. Only EKS nodes can reach it.
resource "aws_security_group_rule" "msk_scram_from_eks" {
  type                     = "ingress"
  from_port                = 9096
  to_port                  = 9096
  protocol                 = "tcp"
  security_group_id        = aws_security_group.msk.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "MSK 9096 (SASL/SCRAM over TLS) from EKS nodes"
}

# IAM auth is 9098; only opened when enabled.
resource "aws_security_group_rule" "msk_iam_from_eks" {
  count = var.enable_iam_auth ? 1 : 0

  type                     = "ingress"
  from_port                = 9098
  to_port                  = 9098
  protocol                 = "tcp"
  security_group_id        = aws_security_group.msk.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "MSK 9098 (IAM SASL) from EKS nodes"
}

# ─── Encryption keys ────────────────────────────────────────────────
#
# One CMK for broker EBS volumes and the SCRAM secret. AWS refuses a SCRAM
# secret that is encrypted with the AWS-managed aws/secretsmanager key.
resource "aws_kms_key" "msk" {
  description             = "atpost-${var.environment} MSK broker storage and SCRAM secret"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-msk-kms"
  }
}

resource "aws_kms_alias" "msk" {
  name          = "alias/atpost-${var.environment}-msk"
  target_key_id = aws_kms_key.msk.key_id
}

# ─── SCRAM application user ─────────────────────────────────────────
#
# The password is generated here and therefore lands in Terraform state,
# exactly like the Aurora master, Valkey AUTH and OpenSearch master
# passwords already do (state is the encrypted, access-restricted S3
# backend — treat it as a secret). Rotation: put a new password in the
# secret by hand, roll the services, and Terraform will not fight it
# (ignore_changes below).
resource "random_password" "scram" {
  length  = 48
  special = false
}

resource "aws_secretsmanager_secret" "scram" {
  # The AmazonMSK_ prefix is mandatory for SCRAM secret association.
  name                    = "AmazonMSK_atpost-${var.environment}-${var.scram_username}"
  description             = "MSK SASL/SCRAM credentials for the atpost services. Copied into each service secret by the seeder."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.msk.arn

  tags = {
    Name = "atpost-${var.environment}-msk-scram"
  }
}

resource "aws_secretsmanager_secret_version" "scram" {
  secret_id = aws_secretsmanager_secret.scram.id
  secret_string = jsonencode({
    username = var.scram_username
    password = random_password.scram.result
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

# MSK reads the secret when associating it; the resource policy is what
# lets the service principal do that.
data "aws_iam_policy_document" "scram_secret" {
  statement {
    sid     = "AWSKafkaResourcePolicy"
    effect  = "Allow"
    actions = ["secretsmanager:GetSecretValue"]
    principals {
      type        = "Service"
      identifiers = ["kafka.amazonaws.com"]
    }
    resources = [aws_secretsmanager_secret.scram.arn]
  }
}

resource "aws_secretsmanager_secret_policy" "scram" {
  secret_arn = aws_secretsmanager_secret.scram.arn
  policy     = data.aws_iam_policy_document.scram_secret.json
}

# ─── Broker configuration ───────────────────────────────────────────
resource "aws_msk_configuration" "this" {
  name              = "atpost-${var.environment}-${replace(var.kafka_version, ".", "-")}"
  kafka_versions    = [var.kafka_version]
  description       = "atpost ${var.environment} broker settings"
  server_properties = <<-PROPERTIES
    auto.create.topics.enable=false
    default.replication.factor=${var.default_replication_factor}
    min.insync.replicas=${var.min_insync_replicas}
    num.partitions=${var.default_partitions}
    log.retention.hours=${var.log_retention_hours}
    unclean.leader.election.enable=false
    delete.topic.enable=true
  PROPERTIES

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_cloudwatch_log_group" "broker" {
  name              = "/aws/msk/atpost-${var.environment}/broker"
  retention_in_days = var.broker_log_retention_days
}

resource "aws_msk_cluster" "this" {
  cluster_name           = "atpost-${var.environment}"
  kafka_version          = var.kafka_version
  number_of_broker_nodes = var.number_of_broker_nodes

  broker_node_group_info {
    instance_type = var.broker_instance_type
    # Broker count must be a multiple of the subnet count; two brokers →
    # the first two private subnets (two AZs).
    client_subnets  = slice(var.private_subnet_ids, 0, var.number_of_broker_nodes)
    security_groups = [aws_security_group.msk.id]

    storage_info {
      ebs_storage_info {
        volume_size = var.broker_ebs_volume_size_gb
      }
    }
  }

  configuration_info {
    arn      = aws_msk_configuration.this.arn
    revision = aws_msk_configuration.this.latest_revision
  }

  client_authentication {
    sasl {
      scram = true
      iam   = var.enable_iam_auth
    }
  }

  encryption_info {
    encryption_at_rest_kms_key_arn = aws_kms_key.msk.arn
    encryption_in_transit {
      client_broker = "TLS"
      in_cluster    = true
    }
  }

  # Prometheus exporters are free; the Grafana stack scrapes them.
  open_monitoring {
    prometheus {
      jmx_exporter {
        enabled_in_broker = true
      }
      node_exporter {
        enabled_in_broker = true
      }
    }
  }

  logging_info {
    broker_logs {
      cloudwatch_logs {
        enabled   = true
        log_group = aws_cloudwatch_log_group.broker.name
      }
    }
  }

  tags = {
    Name = "atpost-${var.environment}-msk"
  }
}

resource "aws_msk_scram_secret_association" "this" {
  cluster_arn     = aws_msk_cluster.this.arn
  secret_arn_list = [aws_secretsmanager_secret.scram.arn]

  depends_on = [aws_secretsmanager_secret_policy.scram, aws_secretsmanager_secret_version.scram]
}

# ─── Optional IAM client policy ─────────────────────────────────────
#
# Only meaningful when IAM auth is enabled. With SCRAM the Kafka ACL model
# applies (the SCRAM user has full access on a cluster without ACLs), so
# nothing on the IRSA roles is needed for Kafka.
data "aws_iam_policy_document" "msk_client" {
  count = var.enable_iam_auth ? 1 : 0

  statement {
    sid    = "Connect"
    effect = "Allow"
    actions = [
      "kafka-cluster:Connect",
      "kafka-cluster:DescribeCluster",
    ]
    resources = [aws_msk_cluster.this.arn]
  }

  statement {
    sid    = "TopicReadWrite"
    effect = "Allow"
    actions = [
      "kafka-cluster:DescribeTopic",
      "kafka-cluster:WriteData",
      "kafka-cluster:ReadData",
    ]
    resources = ["${replace(aws_msk_cluster.this.arn, ":cluster/", ":topic/")}/*"]
  }

  statement {
    sid    = "ConsumerGroup"
    effect = "Allow"
    actions = [
      "kafka-cluster:AlterGroup",
      "kafka-cluster:DescribeGroup",
    ]
    resources = ["${replace(aws_msk_cluster.this.arn, ":cluster/", ":group/")}/*"]
  }
}

resource "aws_iam_policy" "msk_client" {
  count = var.enable_iam_auth ? 1 : 0

  name        = "atpost-${var.environment}-msk-client"
  description = "MSK IAM-auth client policy. Only needed when enable_iam_auth is true."
  policy      = data.aws_iam_policy_document.msk_client[0].json

  tags = {
    Name = "atpost-${var.environment}-msk-client"
  }
}
