# Self-hosted stateful platform on AKS: Scylla + Redpanda + MinIO.
#
# These are the deliberately-portable stores — the SAME software the dev
# stack already self-hosts (Redpanda + MinIO) and the SAME Scylla the
# AWS cluster runs via the operator. Running them in-cluster (rather than
# Azure-managed equivalents) is what keeps the app code byte-identical
# across clouds: services point at *.svc.cluster.local endpoints and
# read creds from Key Vault via ESO, exactly as on EKS.
#
# Trade-off (accepted for portability): we own the ops burden (upgrades,
# backups, scaling) for these three. Postgres + Redis stay managed
# because they're wire-compatible and cheaper to operate managed.
#
# Persistent volumes use the AKS built-in `managed-csi-premium`
# (Premium SSD) storage class — the Azure analogue of the AWS gp3 class.

# ── Scylla ───────────────────────────────────────────────────────
resource "kubernetes_namespace" "scylla_operator" {
  metadata {
    name   = "scylla-operator"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "kubernetes_namespace" "scylla" {
  metadata {
    name   = "scylla"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "helm_release" "scylla_operator" {
  name       = "scylla-operator"
  repository = "https://scylla-operator-charts.storage.googleapis.com/stable"
  chart      = "scylla-operator"
  version    = var.scylla_operator_chart_version
  namespace  = kubernetes_namespace.scylla_operator.metadata[0].name

  set {
    name  = "nodeSelector.workload"
    value = "system"
  }
  set {
    name  = "replicas"
    value = "2"
  }
}

resource "kubernetes_manifest" "scylla_cluster" {
  manifest = {
    apiVersion = "scylla.scylladb.com/v1"
    kind       = "ScyllaCluster"
    metadata = {
      name      = "atpost-${var.environment}"
      namespace = kubernetes_namespace.scylla.metadata[0].name
    }
    spec = {
      version       = var.scylla_version
      agentVersion  = var.scylla_agent_version
      developerMode = var.scylla_developer_mode
      datacenter = {
        name = var.location
        racks = [
          for z in var.zones : {
            name    = "zone-${z}"
            members = 1
            placement = {
              nodeAffinity = {
                requiredDuringSchedulingIgnoredDuringExecution = {
                  nodeSelectorTerms = [{
                    matchExpressions = [{
                      key      = "workload"
                      operator = "In"
                      values   = ["general"]
                      }, {
                      key      = "topology.kubernetes.io/zone"
                      operator = "In"
                      values   = ["${var.location}-${z}"]
                    }]
                  }]
                }
              }
            }
            resources = {
              limits = {
                cpu    = var.scylla_cpu_per_replica
                memory = var.scylla_memory_per_replica
              }
              requests = {
                cpu    = var.scylla_cpu_per_replica
                memory = var.scylla_memory_per_replica
              }
            }
            storage = {
              capacity         = var.scylla_storage_per_replica
              storageClassName = "managed-csi-premium"
            }
          }
        ]
      }
    }
  }

  depends_on = [helm_release.scylla_operator]
}

# Scylla connection secret (no auth by default, matching dev) — hosts
# point at the operator-managed Service.
resource "azurerm_key_vault_secret" "scylla" {
  name         = var.scylla_secret_name
  key_vault_id = var.key_vault_id
  value = jsonencode({
    hosts = "atpost-${var.environment}-client.scylla.svc.cluster.local"
    port  = 9042
  })
  tags = { environment = var.environment }
}

# ── Redpanda (Kafka-compatible) ──────────────────────────────────
resource "kubernetes_namespace" "redpanda" {
  metadata {
    name   = "redpanda"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "helm_release" "redpanda" {
  name       = "redpanda"
  repository = "https://charts.redpanda.com"
  chart      = "redpanda"
  version    = var.redpanda_chart_version
  namespace  = kubernetes_namespace.redpanda.metadata[0].name

  # In-cluster only, TLS off, no external listener — same posture as the
  # dev self-host. Services reach it over the internal Kafka API.
  values = [yamlencode({
    statefulset = {
      replicas = var.redpanda_replicas
      nodeSelector = {
        workload = "general"
      }
    }
    storage = {
      persistentVolume = {
        enabled      = true
        size         = var.redpanda_storage
        storageClass = "managed-csi-premium"
      }
    }
    tls = {
      enabled = false
    }
    external = {
      enabled = false
    }
    resources = {
      cpu = { cores = var.redpanda_cpu }
      memory = {
        container = { max = var.redpanda_memory }
      }
    }
  })]
}

# Kafka brokers endpoint for services (KAFKA_BROKERS).
resource "azurerm_key_vault_secret" "redpanda" {
  name         = var.redpanda_secret_name
  key_vault_id = var.key_vault_id
  value = jsonencode({
    brokers = "redpanda.redpanda.svc.cluster.local:9093"
  })
  tags = { environment = var.environment }
}

# ── MinIO (S3-compatible object store) ───────────────────────────
resource "kubernetes_namespace" "minio" {
  metadata {
    name   = "minio"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "random_password" "minio_root" {
  length  = 32
  special = false
}

resource "helm_release" "minio" {
  name       = "minio"
  repository = "https://charts.min.io/"
  chart      = "minio"
  version    = var.minio_chart_version
  namespace  = kubernetes_namespace.minio.metadata[0].name

  values = [yamlencode({
    mode         = var.minio_mode
    replicas     = var.minio_replicas
    rootUser     = var.minio_root_user
    rootPassword = random_password.minio_root.result
    persistence = {
      enabled      = true
      size         = var.minio_storage
      storageClass = "managed-csi-premium"
    }
    nodeSelector = {
      workload = "general"
    }
    resources = {
      requests = {
        memory = var.minio_memory
      }
    }
    buckets = [
      for b in var.minio_buckets : {
        name   = b
        policy = "none"
      }
    ]
  })]
}

# S3 connection secret — endpoint + creds matching the env services
# already expect (MINIO_ENDPOINT / MINIO_ACCESS_KEY / MINIO_SECRET_KEY).
resource "azurerm_key_vault_secret" "minio" {
  name         = var.minio_secret_name
  key_vault_id = var.key_vault_id
  value = jsonencode({
    endpoint   = "minio.minio.svc.cluster.local:9000"
    access_key = var.minio_root_user
    secret_key = random_password.minio_root.result
    use_ssl    = "false"
  })
  tags = { environment = var.environment }
}
