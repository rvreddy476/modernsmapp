output "hostname" {
  value = "redis-master.redis.svc.cluster.local"
}

output "port" {
  value = 6379
}

output "secret_name" {
  value = azurerm_key_vault_secret.connection.name
}
