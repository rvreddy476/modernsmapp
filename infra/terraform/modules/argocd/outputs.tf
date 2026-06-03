output "namespace" {
  value = kubernetes_namespace.argocd.metadata[0].name
}

output "admin_secret_arn" {
  value       = aws_secretsmanager_secret.admin.arn
  description = "Secrets Manager ARN with admin credentials. Username `admin`."
}

output "project_name" {
  value       = "atpost"
  description = "AppProject all per-service ArgoCD Applications should land in."
}
