# Aurora PostgreSQL Multi-AZ cluster.
#
# Per the Phase-2 decision (aws_phase2_decisions §1): Aurora over RDS
# Multi-AZ for 5x write throughput + ~10s failover. 1 writer + 1 reader
# in prod, single writer in staging. Storage auto-scales; the reader
# carries SELECT load + is the failover target.
#
# Logical DB layout matches the docker-compose convention: 5 logical
# databases ("app", "identity_db", "chat_db", "commerce_db", "feed_db")
# inside one cluster. Aurora doesn't natively create multiple DBs on
# init — only the master DB ("postgres") is created here. The 5 logical
# DBs are created by a kubernetes bootstrap Job applied after the
# cluster comes up (see envs/<env>/k8s.tf — TODO follow-up).
#
# Connection model:
#   service pods → cluster_endpoint (writer)        → PgBouncer sidecar → Aurora
#   read-only    → reader_endpoint  (load-balanced) → PgBouncer sidecar → Aurora replicas
# PgBouncer is needed because Aurora's per-connection memory cost is
# high and we have ~30 services × N pods each — direct connections
# would exhaust the max_connections ceiling.

# Master password — generated, stored in Secrets Manager. Services
# reach it via External Secrets Operator (Phase-2 follow-up).
resource "random_password" "master" {
  length  = 32
  special = true
  # Aurora rejects /@" " characters in master passwords.
  override_special = "!#$%&*()-_=+[]{}<>:?"
}

resource "aws_secretsmanager_secret" "master" {
  name                    = "atpost/${var.environment}/aurora/master"
  description             = "Aurora PostgreSQL master credentials. Read by External Secrets Operator."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.aurora.arn

  tags = {
    Name = "atpost-${var.environment}-aurora-master"
  }
}

resource "aws_secretsmanager_secret_version" "master" {
  secret_id = aws_secretsmanager_secret.master.id
  secret_string = jsonencode({
    username = var.master_username
    password = random_password.master.result
    host     = module.aurora.cluster_endpoint
    port     = module.aurora.cluster_port
  })

  # secret_string references module.aurora.cluster_endpoint → depends_on
  # is implicit. Listed here for clarity.
  depends_on = [module.aurora]
}

# KMS key for the cluster + its logs + the secret. Customer-managed
# (not aws/rds) because PCI evidence requires us to demonstrate key
# control + rotation policy on payment-data-adjacent stores.
resource "aws_kms_key" "aurora" {
  description             = "atpost-${var.environment} Aurora cluster encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-aurora-kms"
  }
}

resource "aws_kms_alias" "aurora" {
  name          = "alias/atpost-${var.environment}-aurora"
  target_key_id = aws_kms_key.aurora.key_id
}

# Subnet group: cluster lives in the ISOLATED tier (no internet route).
# Only services inside the VPC reach it; combined with the security
# group rule below this is the standard "data plane behind the app
# plane" pattern.
resource "aws_db_subnet_group" "aurora" {
  name        = "atpost-${var.environment}-aurora"
  subnet_ids  = var.isolated_subnet_ids
  description = "Aurora cluster subnet group (3 AZs, isolated tier)"

  tags = {
    Name = "atpost-${var.environment}-aurora-subnets"
  }
}

# Security group: ingress 5432 from EKS nodes only. No public ingress.
# Egress: default (none needed; Aurora doesn't initiate outbound).
resource "aws_security_group" "aurora" {
  name        = "atpost-${var.environment}-aurora"
  description = "Aurora PostgreSQL cluster"
  vpc_id      = var.vpc_id

  tags = {
    Name = "atpost-${var.environment}-aurora-sg"
  }
}

resource "aws_security_group_rule" "aurora_from_eks" {
  type                     = "ingress"
  from_port                = 5432
  to_port                  = 5432
  protocol                 = "tcp"
  security_group_id        = aws_security_group.aurora.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "Aurora 5432 from EKS nodes"
}

# Parameter group — tunes the cluster's defaults. Picked the
# performance-and-observability set that pg_stat_statements + slow-
# query analysis depend on.
resource "aws_rds_cluster_parameter_group" "aurora" {
  name        = "atpost-${var.environment}-aurora"
  family      = "aurora-postgresql16"
  description = "atpost-${var.environment} Aurora PG cluster parameters"

  # Capture queries > 1s in the postgresql log → CloudWatch.
  parameter {
    name  = "log_min_duration_statement"
    value = "1000"
  }
  # I/O timing in pg_stat_statements. Cheap, useful for hot-path
  # analysis; the Aurora-side overhead is sub-1%.
  parameter {
    name  = "track_io_timing"
    value = "1"
  }
  parameter {
    name         = "shared_preload_libraries"
    value        = "pg_stat_statements"
    apply_method = "pending-reboot"
  }
  # Lock waits > 5s logged — surfaces deadlock-adjacent issues that
  # don't quite trip the deadlock detector.
  parameter {
    name  = "log_lock_waits"
    value = "1"
  }
  parameter {
    name  = "log_temp_files"
    value = "10240" # log temp files > 10MB
  }
  parameter {
    name  = "log_min_error_statement"
    value = "error"
  }

  tags = {
    Name = "atpost-${var.environment}-aurora-pg"
  }
}

# The Aurora cluster itself. Module pin matches what we vet in this
# repo; bumping it requires re-reading the module CHANGELOG for breaking
# changes (the v8→v9 jump moved several knobs around).
module "aurora" {
  source  = "terraform-aws-modules/rds-aurora/aws"
  version = "~> 9.10"

  name                   = "atpost-${var.environment}-aurora"
  engine                 = "aurora-postgresql"
  engine_version         = "16.4"
  database_name          = "postgres"
  master_username        = var.master_username
  master_password        = random_password.master.result
  manage_master_user_password = false # we manage it in Secrets Manager ourselves

  vpc_id                 = var.vpc_id
  db_subnet_group_name   = aws_db_subnet_group.aurora.name
  vpc_security_group_ids = [aws_security_group.aurora.id]
  create_security_group  = false

  storage_encrypted = true
  kms_key_id        = aws_kms_key.aurora.arn

  # IAM database auth — lets services authenticate with IAM tokens
  # instead of passwords. Combined with IRSA, a pod can connect with
  # an IAM token signed by its service-account role; password rotation
  # becomes a no-op for IAM-auth users. Master still uses password.
  iam_database_authentication_enabled = true

  apply_immediately   = var.apply_immediately
  deletion_protection = var.deletion_protection
  skip_final_snapshot = !var.deletion_protection

  # Backups
  backup_retention_period      = var.backup_retention_days
  preferred_backup_window      = "18:00-19:00" # 23:30-00:30 IST (low traffic)
  preferred_maintenance_window = "sun:19:00-sun:20:00"
  copy_tags_to_snapshot        = true

  # CloudWatch logs export — postgresql for slow-query / lock-wait,
  # upgrade for major-version upgrade traces.
  enabled_cloudwatch_logs_exports = ["postgresql"]

  # Performance Insights — 7-day retention free, useful for hot-spot
  # analysis. Longer retention costs.
  performance_insights_enabled          = true
  performance_insights_retention_period = 7

  monitoring_interval = 30 # enhanced metrics every 30s

  # Cluster parameter group + instance parameter group. Module wants
  # both — instance-level can stay default for now.
  db_cluster_parameter_group_name = aws_rds_cluster_parameter_group.aurora.name
  create_db_cluster_parameter_group = false

  # Writer + (optional) reader. The map shape is the v9+ contract;
  # earlier major versions used a list.
  instances = var.create_reader ? {
    writer = {
      instance_class = var.instance_class
    }
    reader = {
      instance_class      = var.instance_class
      promotion_tier      = 1 # first to be promoted on writer failure
    }
  } : {
    writer = {
      instance_class = var.instance_class
    }
  }

  tags = {
    Name = "atpost-${var.environment}-aurora"
  }
}
