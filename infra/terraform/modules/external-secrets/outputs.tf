output "cluster_secret_store_name" {
  value       = "aws-secrets-manager"
  description = "ClusterSecretStore name. Reference from per-service ExternalSecret resources."
}

output "iam_role_arn" {
  value = aws_iam_role.eso.arn
}

output "namespace" {
  value = kubernetes_namespace.external_secrets.metadata[0].name
}
