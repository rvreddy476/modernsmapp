output "scylla_secret_name" {
  value = azurerm_key_vault_secret.scylla.name
}

output "redpanda_secret_name" {
  value = azurerm_key_vault_secret.redpanda.name
}

output "minio_secret_name" {
  value = azurerm_key_vault_secret.minio.name
}

output "kafka_brokers" {
  value = "redpanda.redpanda.svc.cluster.local:9093"
}
