output "endpoint" {
  value       = aws_opensearch_domain.this.endpoint
  description = "HTTPS endpoint. Use as OPENSEARCH_URL in service env. Resolves to a VPC ENI; only reachable from inside the VPC."
}

output "kibana_endpoint" {
  value = "${aws_opensearch_domain.this.endpoint}/_dashboards"
}

output "domain_arn" {
  value = aws_opensearch_domain.this.arn
}

output "security_group_id" {
  value = aws_security_group.opensearch.id
}

output "kms_key_arn" {
  value = aws_kms_key.opensearch.arn
}

output "master_secret_arn" {
  value       = aws_secretsmanager_secret.master.arn
  description = "Secrets Manager ARN for the master credentials. External Secrets Operator reads this for the kibana-master k8s Secret."
}
