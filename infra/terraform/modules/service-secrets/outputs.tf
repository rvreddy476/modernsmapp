output "secret_arns" {
  value       = { for k, s in aws_secretsmanager_secret.shell : k => s.arn }
  description = "name → secret ARN."
}

output "secret_names" {
  value       = { for k, s in aws_secretsmanager_secret.shell : k => s.name }
  description = "name → full secret name (atpost/<env>/<name>) for ExternalSecret remoteKeys and the seeder."
}

output "kms_key_arn" {
  value       = aws_kms_key.secrets.arn
  description = "Add to the External Secrets Operator decrypt list."
}
