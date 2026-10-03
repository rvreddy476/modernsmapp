# MSK topics bootstrap Job — creates every Kafka topic the services use.
#
# The AWS provider cannot create topics inside an MSK cluster (there is no
# aws_msk_topic resource in 5.x), and the brokers are reachable only from
# the VPC. So, like modules/aurora-bootstrap, this is a run-once Kubernetes
# Job: it pulls the SCRAM credentials through External Secrets, writes a
# client.properties for SASL_SSL/SCRAM-SHA-512, and runs
# `kafka-topics.sh --create --if-not-exists` for each topic. Idempotent;
# re-runs when the topic list or the partition count changes (checksum
# annotation). It never deletes or shrinks a topic.
#
# Apply in the second (in-cluster) pass, after External Secrets is up.

locals {
  topics_sorted = sort(var.topics)

  create_script = <<-SCRIPT
    #!/bin/sh
    set -eu
    cat > /tmp/client.properties <<EOF
    security.protocol=SASL_SSL
    sasl.mechanism=SCRAM-SHA-512
    sasl.jaas.config=org.apache.kafka.common.security.scram.ScramLoginModule required username="$${KAFKA_SASL_USERNAME}" password="$${KAFKA_SASL_PASSWORD}";
    EOF
    echo "bootstrap: $${KAFKA_BROKERS}"
    i=0
    until /opt/kafka/bin/kafka-topics.sh --bootstrap-server "$${KAFKA_BROKERS}" --command-config /tmp/client.properties --list >/dev/null 2>&1; do
      i=$$((i+1)); [ "$$i" -ge 30 ] && { echo "brokers not reachable"; exit 1; }
      sleep 10
    done
    while read -r topic; do
      [ -z "$$topic" ] && continue
      /opt/kafka/bin/kafka-topics.sh --bootstrap-server "$${KAFKA_BROKERS}" --command-config /tmp/client.properties \
        --create --if-not-exists --topic "$$topic" \
        --partitions ${var.partitions} --replication-factor ${var.replication_factor} \
        $${TOPIC_CONFIGS}
    done < /topics/topics.txt
    echo "topics present:"
    /opt/kafka/bin/kafka-topics.sh --bootstrap-server "$${KAFKA_BROKERS}" --command-config /tmp/client.properties --list
  SCRIPT

  topic_configs = join(" ", [for k, v in var.topic_configs : "--config ${k}=${v}"])
  content_hash  = sha256(join("\n", concat(local.topics_sorted, [tostring(var.partitions), tostring(var.replication_factor), local.topic_configs, var.kafka_image])))
}

resource "kubernetes_namespace" "msk_bootstrap" {
  metadata {
    name = "msk-bootstrap"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "kubernetes_manifest" "scram_secret" {
  manifest = {
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ExternalSecret"
    metadata = {
      name      = "msk-scram"
      namespace = kubernetes_namespace.msk_bootstrap.metadata[0].name
    }
    spec = {
      refreshInterval = "1h"
      secretStoreRef = {
        name = var.cluster_secret_store_name
        kind = "ClusterSecretStore"
      }
      target = {
        name           = "msk-scram"
        creationPolicy = "Owner"
      }
      data = [
        for k in ["username", "password"] : {
          secretKey = k
          remoteRef = {
            key      = var.scram_secret_name
            property = k
          }
        }
      ]
    }
  }
}

resource "kubernetes_config_map" "topics" {
  metadata {
    name      = "msk-topics"
    namespace = kubernetes_namespace.msk_bootstrap.metadata[0].name
  }
  data = {
    "topics.txt" = join("\n", local.topics_sorted)
    "create.sh"  = local.create_script
  }
}

resource "kubernetes_job" "topics" {
  metadata {
    name      = "msk-topics"
    namespace = kubernetes_namespace.msk_bootstrap.metadata[0].name
    annotations = {
      "config.sha256" = local.content_hash
    }
  }

  spec {
    backoff_limit              = 3
    ttl_seconds_after_finished = 86400

    template {
      metadata {
        labels = {
          job = "msk-topics"
        }
      }
      spec {
        restart_policy = "OnFailure"

        node_selector = {
          workload = "system"
        }

        container {
          name    = "kafka-cli"
          image   = var.kafka_image
          command = ["sh", "/topics/create.sh"]

          env {
            name  = "KAFKA_BROKERS"
            value = var.bootstrap_brokers
          }
          env {
            name  = "TOPIC_CONFIGS"
            value = local.topic_configs
          }
          env {
            name = "KAFKA_SASL_USERNAME"
            value_from {
              secret_key_ref {
                name = "msk-scram"
                key  = "username"
              }
            }
          }
          env {
            name = "KAFKA_SASL_PASSWORD"
            value_from {
              secret_key_ref {
                name = "msk-scram"
                key  = "password"
              }
            }
          }

          volume_mount {
            name       = "topics"
            mount_path = "/topics"
            read_only  = true
          }
        }

        volume {
          name = "topics"
          config_map {
            name = kubernetes_config_map.topics.metadata[0].name
          }
        }
      }
    }
  }

  wait_for_completion = true
  timeouts {
    create = "15m"
    update = "15m"
  }

  depends_on = [kubernetes_manifest.scram_secret]
}
