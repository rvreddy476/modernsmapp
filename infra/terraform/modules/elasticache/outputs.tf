output "primary_endpoint" {
  value       = aws_elasticache_replication_group.this.primary_endpoint_address
  description = "Write endpoint. Use for all writes + reads-after-write."
}

output "reader_endpoint" {
  value       = aws_elasticache_replication_group.this.reader_endpoint_address
  description = "Read-only endpoint, load-balanced across replicas."
}

output "port" {
  value = 6379
}

output "security_group_id" {
  value = aws_security_group.elasticache.id
}

output "kms_key_arn" {
  value = aws_kms_key.elasticache.arn
}

output "auth_secret_arn" {
  value       = aws_secretsmanager_secret.auth.arn
  description = "Secrets Manager ARN for the Valkey AUTH token + endpoints. External Secrets Operator reads this."
}
