output "namespace" {
  value = kubernetes_namespace.observability.metadata[0].name
}

output "grafana_admin_secret_arn" {
  value       = aws_secretsmanager_secret.grafana_admin.arn
  description = "Secrets Manager ARN with the Grafana admin credentials. Username: admin."
}

output "prometheus_service_url" {
  value       = "http://prometheus-operated.observability.svc.cluster.local:9090"
  description = "Cluster-DNS URL for Prometheus. Add as a datasource in additional Grafana instances or as the remoteRead source for an AMP migration."
}

output "alertmanager_service_url" {
  value = "http://alertmanager-operated.observability.svc.cluster.local:9093"
}
