output "cluster_arn" {
  value = aws_msk_serverless_cluster.this.arn
}

output "cluster_name" {
  value = aws_msk_serverless_cluster.this.cluster_name
}

output "bootstrap_brokers_sasl_iam" {
  value       = aws_msk_serverless_cluster.this.bootstrap_brokers_sasl_iam
  description = "Comma-separated bootstrap-broker list for IAM auth (port 9098). Feed to KAFKA_BROKERS env in every service."
}

output "security_group_id" {
  value       = aws_security_group.msk.id
  description = "MSK SG. Add ingress rules from other SGs (e.g. MSK Connect, schema registry workers) by attaching to this."
}

output "client_iam_policy_arn" {
  value       = aws_iam_policy.msk_client.arn
  description = "Standard MSK client IAM policy. Attach to every IRSA role that produces or consumes."
}
