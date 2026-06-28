# Redis — self-hosted on AKS.
#
# Azure Cache for Redis (azurerm_redis_cache) is retired for new creates, and
# Azure Managed Redis (Enterprise) is comparatively expensive/heavy. Redis here
# is cache/rebuildable (not on the data-migration critical path), so we run the
# official redis image in-cluster — cheaper, and cloud-agnostic like the other
# self-hosted stores. Services read it via REDIS_ADDR (+ password) from the
# Key Vault secret, exactly as before; only the host moves in-cluster.
#
# Single instance by default (fine for cache). For HA later, swap to a Redis
# chart with replication/Sentinel; the KV secret contract stays the same.

resource "kubernetes_namespace" "redis" {
  metadata {
    name   = "redis"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "random_password" "redis" {
  length  = 24
  special = false
}

resource "kubernetes_secret" "redis_auth" {
  metadata {
    name      = "redis-auth"
    namespace = kubernetes_namespace.redis.metadata[0].name
  }
  data = {
    "redis-password" = random_password.redis.result
  }
}

resource "kubernetes_deployment" "redis" {
  metadata {
    name      = "redis-master"
    namespace = kubernetes_namespace.redis.metadata[0].name
    labels    = { app = "redis" }
  }
  spec {
    replicas = 1
    selector {
      match_labels = { app = "redis" }
    }
    template {
      metadata {
        labels = { app = "redis" }
      }
      spec {
        node_selector = { workload = "general" }
        container {
          name  = "redis"
          image = var.image
          args  = ["--requirepass", "$(REDIS_PASSWORD)", "--maxmemory-policy", "allkeys-lru"]
          env {
            name = "REDIS_PASSWORD"
            value_from {
              secret_key_ref {
                name = kubernetes_secret.redis_auth.metadata[0].name
                key  = "redis-password"
              }
            }
          }
          port {
            container_port = 6379
          }
          resources {
            requests = {
              cpu    = "100m"
              memory = "128Mi"
            }
            limits = {
              memory = var.memory_limit
            }
          }
          readiness_probe {
            tcp_socket {
              port = 6379
            }
            initial_delay_seconds = 5
            period_seconds        = 10
          }
        }
      }
    }
  }
}

resource "kubernetes_service" "redis" {
  metadata {
    name      = "redis-master"
    namespace = kubernetes_namespace.redis.metadata[0].name
  }
  spec {
    selector = { app = "redis" }
    port {
      port        = 6379
      target_port = 6379
    }
  }
}

# Connection secret in Key Vault — same JSON shape ESO maps for the services.
resource "azurerm_key_vault_secret" "connection" {
  name         = var.secret_name
  key_vault_id = var.key_vault_id

  value = jsonencode({
    host     = "redis-master.redis.svc.cluster.local"
    port     = 6379
    password = random_password.redis.result
    addr     = "redis-master.redis.svc.cluster.local:6379"
  })

  tags = {
    environment = var.environment
  }
}
