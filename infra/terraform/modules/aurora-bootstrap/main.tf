# Aurora bootstrap Job — creates the 5 logical databases.
#
# Aurora only creates the master DB ("postgres") on init. atpost
# convention from docker-compose splits into 5 logical DBs:
#   app, identity_db, chat_db, commerce_db, feed_db.
#
# Run-once kubernetes Job that:
#   1. Reads the Aurora master credentials from a kubernetes Secret
#      that ESO mirrors from atpost/${env}/aurora/master.
#   2. CREATE DATABASE for each missing logical DB.
#
# Idempotent: re-running is a no-op once every DB exists. We trigger
# a re-run by bumping the spec.template hash via the configmap
# checksum annotation, so adding a DB to the list re-fires the Job.

resource "kubernetes_namespace" "bootstrap" {
  metadata {
    name = "aurora-bootstrap"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

# ExternalSecret: pull the Aurora master creds into a kubernetes Secret
# the Job can mount.
resource "kubernetes_manifest" "master_secret" {
  manifest = {
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ExternalSecret"
    metadata = {
      name      = "aurora-master"
      namespace = kubernetes_namespace.bootstrap.metadata[0].name
    }
    spec = {
      refreshInterval = "1h"
      secretStoreRef = {
        name = var.cluster_secret_store_name
        kind = "ClusterSecretStore"
      }
      target = {
        name           = "aurora-master"
        creationPolicy = "Owner"
      }
      data = [
        for k in ["username", "password", "host", "port"] : {
          secretKey = k
          remoteRef = {
            key      = var.master_secret_name
            property = k
          }
        }
      ]
    }
  }
}

# ConfigMap holding the SQL — referenced by the Job so a database
# addition produces a content change → checksum annotation change →
# Job re-runs.
locals {
  bootstrap_sql = <<-SQL
    %{for db in var.databases~}
    SELECT 'CREATE DATABASE ${db}' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '${db}')\gexec
    %{endfor~}
  SQL
}

resource "kubernetes_config_map" "bootstrap_sql" {
  metadata {
    name      = "aurora-bootstrap-sql"
    namespace = kubernetes_namespace.bootstrap.metadata[0].name
  }
  data = {
    "bootstrap.sql" = local.bootstrap_sql
  }
}

# The Job. psql client baked into the postgres:16-alpine image.
resource "kubernetes_job" "bootstrap" {
  metadata {
    name      = "aurora-bootstrap"
    namespace = kubernetes_namespace.bootstrap.metadata[0].name
    annotations = {
      # Re-run on SQL change. ConfigMap content hash → annotation
      # → metadata change → kubernetes treats it as a new Job.
      "config.sha256" = sha256(local.bootstrap_sql)
    }
  }

  spec {
    backoff_limit = 3
    ttl_seconds_after_finished = 86400 # auto-cleanup after a day

    template {
      metadata {
        labels = {
          job = "aurora-bootstrap"
        }
      }
      spec {
        restart_policy = "OnFailure"

        # System node group — platform tooling, not app load.
        node_selector = {
          workload = "system"
        }

        container {
          name  = "psql"
          image = "postgres:16-alpine"

          # Use \gexec to conditionally run CREATE DATABASE (Postgres
          # doesn't have CREATE DATABASE IF NOT EXISTS). The SELECT in
          # the SQL emits the DDL only when the DB is missing; \gexec
          # then executes whatever the previous SELECT returned.
          command = [
            "sh", "-c",
            "PGPASSWORD=\"$DB_PASSWORD\" psql -h \"$DB_HOST\" -p \"$DB_PORT\" -U \"$DB_USERNAME\" -d postgres -v ON_ERROR_STOP=1 -f /sql/bootstrap.sql",
          ]

          env {
            name = "DB_USERNAME"
            value_from {
              secret_key_ref {
                name = "aurora-master"
                key  = "username"
              }
            }
          }
          env {
            name = "DB_PASSWORD"
            value_from {
              secret_key_ref {
                name = "aurora-master"
                key  = "password"
              }
            }
          }
          env {
            name = "DB_HOST"
            value_from {
              secret_key_ref {
                name = "aurora-master"
                key  = "host"
              }
            }
          }
          env {
            name = "DB_PORT"
            value_from {
              secret_key_ref {
                name = "aurora-master"
                key  = "port"
              }
            }
          }

          volume_mount {
            name       = "sql"
            mount_path = "/sql"
            read_only  = true
          }
        }

        volume {
          name = "sql"
          config_map {
            name = kubernetes_config_map.bootstrap_sql.metadata[0].name
          }
        }
      }
    }
  }

  wait_for_completion = true
  timeouts {
    create = "10m"
    update = "10m"
  }

  depends_on = [
    kubernetes_manifest.master_secret,
  ]
}
