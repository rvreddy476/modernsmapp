output "cluster_arn" {
  value = aws_msk_cluster.this.arn
}

output "cluster_name" {
  value = aws_msk_cluster.this.cluster_name
}

output "bootstrap_brokers_sasl_scram" {
  value       = aws_msk_cluster.this.bootstrap_brokers_sasl_scram
  description = "Comma-separated bootstrap-broker list for SASL/SCRAM over TLS (port 9096). Feed to KAFKA_BROKERS in every service with KAFKA_SASL_MECHANISM=SCRAM-SHA-512 and KAFKA_TLS_ENABLED=true."
}

output "bootstrap_brokers_sasl_iam" {
  value       = var.enable_iam_auth ? aws_msk_cluster.this.bootstrap_brokers_sasl_iam : null
  description = "Bootstrap-broker list for IAM auth (port 9098). Null unless enable_iam_auth is true."
}

output "zookeeper_connect_string" {
  value       = aws_msk_cluster.this.zookeeper_connect_string
  description = "Empty on KRaft versions (3.7.x+). Kept for the topic Job on older versions."
}

output "security_group_id" {
  value       = aws_security_group.msk.id
  description = "MSK SG. Add ingress rules from other SGs by attaching to this."
}

output "client_iam_policy_arn" {
  value       = var.enable_iam_auth ? aws_iam_policy.msk_client[0].arn : null
  description = "IAM-auth client policy ARN. Null when IAM auth is off (SCRAM only) — nothing needs attaching to IRSA roles for Kafka."
}

output "scram_secret_arn" {
  value       = aws_secretsmanager_secret.scram.arn
  description = "Secrets Manager ARN of the AmazonMSK_ SCRAM credentials (JSON username/password). The seeder copies these into every service secret."
}

output "scram_secret_name" {
  value       = aws_secretsmanager_secret.scram.name
  description = "Secret name, for ExternalSecret remoteRefs and the topics Job."
}

output "scram_username" {
  value = var.scram_username
}

output "kms_key_arn" {
  value       = aws_kms_key.msk.arn
  description = "CMK that encrypts the SCRAM secret — add to the External Secrets Operator decrypt list."
}
