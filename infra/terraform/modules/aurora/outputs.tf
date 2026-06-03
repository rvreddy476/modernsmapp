output "cluster_endpoint" {
  value       = module.aurora.cluster_endpoint
  description = "Writer endpoint. Use for all writes; PgBouncer sidecar in front."
}

output "reader_endpoint" {
  value       = module.aurora.cluster_reader_endpoint
  description = "Reader endpoint. Load-balances across replicas; returns the writer if no readers exist."
}

output "cluster_port" {
  value = module.aurora.cluster_port
}

output "cluster_arn" {
  value = module.aurora.cluster_arn
}

output "cluster_resource_id" {
  value       = module.aurora.cluster_resource_id
  description = "Stable ID for IAM database auth — referenced in policy ARNs like rds-db:<region>:<account>:dbuser:<resource_id>/<dbuser>."
}

output "security_group_id" {
  value       = aws_security_group.aurora.id
  description = "Aurora SG. Add ingress rules from other SGs (PgBouncer-elsewhere, bastion) by attaching to this."
}

output "kms_key_arn" {
  value = aws_kms_key.aurora.arn
}

output "master_secret_arn" {
  value       = aws_secretsmanager_secret.master.arn
  description = "Secrets Manager ARN for the master credentials. External Secrets Operator reads this to mint the master kubernetes Secret."
}

output "database_name" {
  value       = "postgres"
  description = "Initial database. The 5 logical DBs (app, identity_db, chat_db, commerce_db, feed_db) are created by a bootstrap Job — see follow-up."
}
