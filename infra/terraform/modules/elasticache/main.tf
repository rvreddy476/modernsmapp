# ElastiCache Valkey 7.2 — Redis-compatible drop-in.
#
# Valkey is the open-source fork after Redis re-licensed in March 2024.
# Identical wire protocol + commands; the go-redis client we use already
# speaks it without changes. We pick Valkey over Redis-on-ElastiCache
# because AWS prices Valkey ~20% cheaper and the BUSL/SSPL story stays
# clean.
#
# Mode: cluster-mode-disabled replication group. One primary + 2 replicas
# across 3 AZs. The atpost services use Valkey for:
#   - cache (read-through, TTL'd)
#   - rate-limit counters (INCR + EXPIRE)
#   - session revocation flags
#   - Redis Streams for the real-time event fanout (chat / notifications)
# None of those need cross-shard ops, so cluster-mode-disabled is
# simpler + cheaper. If a hot key ever shows up, partition above the
# cluster (key-namespace sharding in the app) before reaching for
# cluster mode.

resource "aws_security_group" "elasticache" {
  name        = "atpost-${var.environment}-elasticache"
  description = "ElastiCache Valkey replication group"
  vpc_id      = var.vpc_id

  tags = {
    Name = "atpost-${var.environment}-elasticache-sg"
  }
}

resource "aws_security_group_rule" "elasticache_from_eks" {
  type                     = "ingress"
  from_port                = 6379
  to_port                  = 6379
  protocol                 = "tcp"
  security_group_id        = aws_security_group.elasticache.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "Valkey 6379 from EKS nodes"
}

resource "aws_elasticache_subnet_group" "this" {
  name        = "atpost-${var.environment}-elasticache"
  subnet_ids  = var.isolated_subnet_ids
  description = "Valkey subnet group (3 AZs, isolated tier)"
}

# AUTH token — stored in Secrets Manager. go-redis reads it via
# External Secrets Operator (Phase-2 follow-up).
resource "random_password" "auth" {
  length  = 64
  special = false # Valkey AUTH rejects most punctuation
}

resource "aws_secretsmanager_secret" "auth" {
  name                    = "atpost/${var.environment}/elasticache/auth"
  description             = "Valkey AUTH token. Read by External Secrets Operator."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.elasticache.arn

  tags = {
    Name = "atpost-${var.environment}-elasticache-auth"
  }
}

resource "aws_secretsmanager_secret_version" "auth" {
  secret_id = aws_secretsmanager_secret.auth.id
  secret_string = jsonencode({
    auth_token       = random_password.auth.result
    primary_endpoint = aws_elasticache_replication_group.this.primary_endpoint_address
    reader_endpoint  = aws_elasticache_replication_group.this.reader_endpoint_address
    port             = 6379
  })
}

resource "aws_kms_key" "elasticache" {
  description             = "atpost-${var.environment} ElastiCache encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-elasticache-kms"
  }
}

resource "aws_kms_alias" "elasticache" {
  name          = "alias/atpost-${var.environment}-elasticache"
  target_key_id = aws_kms_key.elasticache.key_id
}

resource "aws_elasticache_parameter_group" "valkey" {
  name        = "atpost-${var.environment}-valkey"
  family      = "valkey7" # corresponds to Valkey 7.2
  description = "atpost-${var.environment} Valkey parameters"

  # maxmemory-policy: allkeys-lru. The cache is opportunistic — we don't
  # want OOM-kill behaviour on a hot moment. allkeys-lru evicts the
  # least-recently-used key regardless of TTL, which is the right shape
  # for a cache + counter mix.
  parameter {
    name  = "maxmemory-policy"
    value = "allkeys-lru"
  }
}

resource "aws_elasticache_replication_group" "this" {
  replication_group_id = "atpost-${var.environment}-valkey"
  description          = "atpost ${var.environment} Valkey"

  engine               = "valkey"
  engine_version       = "7.2"
  node_type            = var.node_type
  num_cache_clusters   = var.num_replicas + 1 # primary + N replicas
  parameter_group_name = aws_elasticache_parameter_group.valkey.name
  port                 = 6379

  subnet_group_name  = aws_elasticache_subnet_group.this.name
  security_group_ids = [aws_security_group.elasticache.id]

  # Multi-AZ with automatic failover: replicas are spread across the
  # subnet group's AZs (the isolated tier subnets), and on primary
  # failure one replica gets promoted automatically.
  automatic_failover_enabled = true
  multi_az_enabled           = true

  # Encryption everywhere — at rest (KMS), in transit (TLS), and
  # authenticated (AUTH token). PCI evidence requires all three for
  # any store that holds session tokens.
  at_rest_encryption_enabled = true
  kms_key_id                 = aws_kms_key.elasticache.arn
  transit_encryption_enabled = true
  auth_token                 = random_password.auth.result

  # Backups: daily snapshot, 5 day retention. Useful as a safety net
  # for the counter / dedup data (caches are disposable but the
  # rate-limit + session-revocation Streams aren't).
  snapshot_retention_limit = var.snapshot_retention_days
  snapshot_window          = "18:00-19:00" # 23:30-00:30 IST

  maintenance_window = "sun:19:00-sun:20:30"

  # Apply changes at the next maintenance window in prod (no surprise
  # restarts); immediately in staging.
  apply_immediately = var.apply_immediately

  tags = {
    Name = "atpost-${var.environment}-valkey"
  }

  lifecycle {
    # auth_token rotations need to flow through a manual procedure
    # (set new token, deploy services, retire old). Block accidental
    # in-place rewrites until we wire the rotation runbook.
    ignore_changes = [auth_token]
  }
}
