output "secret_arn" {
  description = "Secrets Manager ARN of the shared platform-auth secret."
  value       = aws_secretsmanager_secret.auth.arn
}

output "secret_name" {
  description = "Secret name — reference from a service ExternalSecret remoteKey."
  value       = aws_secretsmanager_secret.auth.name
}

output "kms_key_arn" {
  description = "CMK ARN — add to the External Secrets Operator decrypt policy."
  value       = aws_kms_key.auth.arn
}
