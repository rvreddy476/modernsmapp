# Observability stack: kube-prometheus-stack (Prometheus + Alertmanager
# + Grafana + node-exporter + kube-state-metrics).
#
# Why in-cluster instead of AMP/AMG (which the plan also lists): the
# Phase-2 scaffold ships before we have real load shape, and kube-
# prometheus-stack is the lowest-friction default. The atpost-service
# Helm chart's ServiceMonitor resources land directly into this
# Prometheus without extra wiring. Migration to AMP is a config flip on
# the Prometheus remoteWrite later.
#
# Grafana exposed via ALB Ingress (uses the aws-load-balancer-controller
# already installed). Admin password mirrored to Secrets Manager.

resource "kubernetes_namespace" "observability" {
  metadata {
    name = "observability"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
      # The atpost-service chart's ServiceMonitor sets
      # `release: prometheus` so kube-prometheus-stack picks them up
      # without needing per-service prometheus rules.
      "monitoring"                   = "enabled"
    }
  }
}

# Grafana admin password — generated, mirrored to Secrets Manager.
resource "random_password" "grafana_admin" {
  length  = 32
  special = true
}

resource "aws_secretsmanager_secret" "grafana_admin" {
  name                    = "atpost/${var.environment}/grafana/admin"
  description             = "Grafana admin password. Username: admin."
  recovery_window_in_days = 7

  tags = {
    Name = "atpost-${var.environment}-grafana-admin"
  }
}

resource "aws_secretsmanager_secret_version" "grafana_admin" {
  secret_id = aws_secretsmanager_secret.grafana_admin.id
  secret_string = jsonencode({
    username = "admin"
    password = random_password.grafana_admin.result
  })
}

# The Prometheus operator + everything else.
# Pinned chart version is the one we've vetted; bump it after reading
# the changelog (the chart changes CRDs sometimes).
resource "helm_release" "kube_prometheus_stack" {
  name             = "prometheus"
  repository       = "https://prometheus-community.github.io/helm-charts"
  chart            = "kube-prometheus-stack"
  version          = var.chart_version
  namespace        = kubernetes_namespace.observability.metadata[0].name
  create_namespace = false

  # Helm-CRD-apply mode: install CRDs as part of the release so a
  # destroy cleans them up. The CRDs are large; upgrades take a
  # minute or two — patience during the first apply.
  set {
    name  = "crds.enabled"
    value = "true"
  }

  values = [
    yamlencode({
      # ─── Prometheus ───────────────────────────────────────────────
      prometheus = {
        prometheusSpec = {
          retention            = var.prometheus_retention
          retentionSize        = var.prometheus_retention_size
          replicas             = var.prometheus_replicas
          # Storage: gp3 (made cluster default by the scylla module).
          # 100GB prod / 30GB staging — sized for the retention above.
          storageSpec = {
            volumeClaimTemplate = {
              spec = {
                accessModes      = ["ReadWriteOnce"]
                storageClassName = "gp3"
                resources = {
                  requests = {
                    storage = var.prometheus_storage_size
                  }
                }
              }
            }
          }
          # Scrape EVERY ServiceMonitor + PodMonitor in the cluster,
          # not just ones tagged with the helm release label. Lets
          # per-service ServiceMonitors (atpost-service chart) work
          # without the operator-default `release` label gate.
          serviceMonitorSelectorNilUsesHelmValues = false
          podMonitorSelectorNilUsesHelmValues     = false
          ruleSelectorNilUsesHelmValues           = false
          probeSelectorNilUsesHelmValues          = false

          # External labels stamped on every series — distinguishes
          # staging from prod when both push to a shared store.
          externalLabels = {
            environment = var.environment
            cluster     = "atpost-${var.environment}"
          }

          nodeSelector = {
            workload = "system"
          }

          resources = {
            requests = {
              cpu    = "500m"
              memory = "2Gi"
            }
            limits = {
              cpu    = "2"
              memory = "4Gi"
            }
          }
        }
      }

      # ─── Alertmanager ─────────────────────────────────────────────
      alertmanager = {
        alertmanagerSpec = {
          replicas = var.alertmanager_replicas
          storage = {
            volumeClaimTemplate = {
              spec = {
                accessModes      = ["ReadWriteOnce"]
                storageClassName = "gp3"
                resources = {
                  requests = {
                    storage = "10Gi"
                  }
                }
              }
            }
          }
          nodeSelector = {
            workload = "system"
          }
        }
      }

      # ─── Grafana ──────────────────────────────────────────────────
      grafana = {
        replicas      = 2
        adminPassword = random_password.grafana_admin.result
        nodeSelector = {
          workload = "system"
        }
        persistence = {
          enabled          = true
          storageClassName = "gp3"
          size             = "10Gi"
        }
        ingress = {
          enabled          = true
          ingressClassName = "alb"
          annotations = {
            "alb.ingress.kubernetes.io/scheme"          = var.grafana_ingress_scheme
            "alb.ingress.kubernetes.io/target-type"     = "ip"
            "alb.ingress.kubernetes.io/listen-ports"    = "[{\"HTTPS\":443}]"
            "alb.ingress.kubernetes.io/certificate-arn" = var.acm_certificate_arn
          }
          hosts = [var.grafana_hostname]
        }
        # Default datasources: Prometheus inside the cluster.
        # The "additionalDataSources" pattern below leaves room for
        # Tempo + Loki when they're added.
        defaultDashboardsEnabled = true
        sidecar = {
          dashboards = {
            enabled = true
            label   = "grafana_dashboard"
          }
          datasources = {
            enabled = true
            label   = "grafana_datasource"
          }
        }
      }

      # ─── kube-state-metrics + node-exporter ───────────────────────
      # Both ship enabled in the chart's defaults; just nudge them to
      # the system node pool so they don't compete with app capacity.
      kubeStateMetrics = {
        enabled = true
      }
      "kube-state-metrics" = {
        nodeSelector = {
          workload = "system"
        }
      }
      nodeExporter = {
        enabled = true
      }
      prometheus-node-exporter = {
        # node-exporter runs as a DaemonSet on every node — no node
        # selector. Tolerate the scylla taint so we get memory-node
        # metrics too.
        tolerations = [{
          key      = "workload"
          operator = "Equal"
          value    = "scylla"
          effect   = "NoSchedule"
        }]
      }
    })
  ]
}
