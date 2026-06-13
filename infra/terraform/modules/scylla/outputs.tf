output "cluster_name" {
  value       = "atpost-${var.environment}"
  description = "ScyllaCluster CR name. Apps connect via the operator-managed Service: atpost-<env>-client.scylla.svc.cluster.local"
}

output "namespace" {
  value = kubernetes_namespace.scylla.metadata[0].name
}

output "client_service" {
  value       = "atpost-${var.environment}-client.scylla.svc.cluster.local"
  description = "Cluster DNS for the gocql driver. Port 9042 (CQL). The operator load-balances across pods."
}
