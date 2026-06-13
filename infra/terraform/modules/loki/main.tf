# Grafana Loki — log aggregation backend. S3 storage via IRSA.
#
# Single-binary mode for now (one binary handles distributor +
# ingester + querier + compactor). Scales to "simple-scalable" (read
# vs. write pods split) when log volume justifies.
#
# Ingestion: Promtail / Grafana Alloy DaemonSet on every node ships
# container stdout/stderr to Loki. Separate Helm install — kept out of
# this module so Loki can be re-pointed at AMP/CloudWatch later
# without touching the agent.

resource "aws_s3_bucket" "loki" {
  bucket = "atpost-${var.environment}-loki-${var.random_suffix}"
  tags = {
    Name = "atpost-${var.environment}-loki"
  }
}

resource "aws_s3_bucket_versioning" "loki" {
  bucket = aws_s3_bucket.loki.id
  versioning_configuration {
    status = "Suspended"
  }
}

resource "aws_s3_bucket_public_access_block" "loki" {
  bucket                  = aws_s3_bucket.loki.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "loki" {
  bucket = aws_s3_bucket.loki.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "loki" {
  bucket = aws_s3_bucket.loki.id

  rule {
    id     = "log-retention"
    status = "Enabled"

    expiration {
      days = var.retention_days
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

data "aws_iam_policy_document" "loki_trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    effect  = "Allow"
    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:sub"
      values   = ["system:serviceaccount:${var.namespace}:loki"]
    }
  }
}

resource "aws_iam_role" "loki" {
  name               = "atpost-${var.environment}-loki"
  assume_role_policy = data.aws_iam_policy_document.loki_trust.json
}

data "aws_iam_policy_document" "loki_s3" {
  statement {
    actions = [
      "s3:ListBucket",
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
    ]
    resources = [
      aws_s3_bucket.loki.arn,
      "${aws_s3_bucket.loki.arn}/*",
    ]
  }
}

resource "aws_iam_role_policy" "loki_s3" {
  name   = "s3-access"
  role   = aws_iam_role.loki.id
  policy = data.aws_iam_policy_document.loki_s3.json
}

resource "helm_release" "loki" {
  name       = "loki"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "loki"
  version    = var.chart_version
  namespace  = var.namespace

  values = [
    yamlencode({
      deploymentMode = "SingleBinary"

      loki = {
        auth_enabled = false
        commonConfig = {
          replication_factor = 1
        }
        storage = {
          type = "s3"
          bucketNames = {
            chunks = aws_s3_bucket.loki.id
            ruler  = aws_s3_bucket.loki.id
            admin  = aws_s3_bucket.loki.id
          }
          s3 = {
            region   = var.aws_region
            endpoint = "s3.${var.aws_region}.amazonaws.com"
            insecure = false
          }
        }
        schemaConfig = {
          configs = [{
            from         = "2024-01-01"
            store        = "tsdb"
            object_store = "s3"
            schema       = "v13"
            index = {
              prefix = "loki_index_"
              period = "24h"
            }
          }]
        }
        # Per-tenant limits — single-tenant deployment, so these apply
        # globally. Bump if rejection rate trips.
        limits_config = {
          retention_period          = "${var.retention_days * 24}h"
          ingestion_rate_mb         = 10
          ingestion_burst_size_mb   = 20
          max_query_parallelism     = 32
          split_queries_by_interval = "30m"
        }
      }

      singleBinary = {
        replicas = 1
        persistence = {
          enabled          = true
          storageClass     = "gp3"
          size             = "50Gi"
        }
        nodeSelector = {
          workload = "system"
        }
      }

      serviceAccount = {
        create = true
        name   = "loki"
        annotations = {
          "eks.amazonaws.com/role-arn" = aws_iam_role.loki.arn
        }
      }

      # Disable the distributor/ingester/querier-Distinct deployments;
      # SingleBinary covers all of them. Without this the chart still
      # tries to create those as zero-replica StatefulSets.
      backend       = { replicas = 0 }
      read          = { replicas = 0 }
      write         = { replicas = 0 }
      ingester      = { replicas = 0 }
      querier       = { replicas = 0 }
      queryFrontend = { replicas = 0 }
      queryScheduler = { replicas = 0 }
      distributor   = { replicas = 0 }
      compactor     = { replicas = 0 }
      indexGateway  = { replicas = 0 }
      memcached     = { enabled = false }
      memcachedExporter = { enabled = false }
      gateway       = { enabled = false }

      test = { enabled = false }
      lokiCanary = { enabled = false }
    })
  ]
}

# Grafana datasource — picked up by the kube-prometheus-stack
# Grafana sidecar via the grafana_datasource label.
resource "kubernetes_config_map" "grafana_datasource" {
  metadata {
    name      = "loki-datasource"
    namespace = var.namespace
    labels = {
      grafana_datasource = "1"
    }
  }
  data = {
    "loki-datasource.yaml" = <<-YAML
      apiVersion: 1
      datasources:
        - name: Loki
          type: loki
          access: proxy
          url: http://loki.${var.namespace}.svc.cluster.local:3100
          editable: false
    YAML
  }
}

# Grafana Alloy DaemonSet — ships container stdout to Loki. Replaces
# Promtail (now in maintenance). Runs on every node, including
# memory-tier (Scylla logs are useful).
resource "helm_release" "alloy" {
  name       = "alloy-logs"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "alloy"
  version    = var.alloy_chart_version
  namespace  = var.namespace

  values = [
    yamlencode({
      alloy = {
        configMap = {
          create = true
          content = <<-RIVER
            // Scrape all container stdout/stderr via the kubelet.
            discovery.kubernetes "pods" {
              role = "pod"
            }
            discovery.relabel "pods" {
              targets = discovery.kubernetes.pods.targets
              rule {
                source_labels = ["__meta_kubernetes_pod_node_name"]
                target_label  = "node"
              }
              rule {
                source_labels = ["__meta_kubernetes_namespace"]
                target_label  = "namespace"
              }
              rule {
                source_labels = ["__meta_kubernetes_pod_label_app_kubernetes_io_name"]
                target_label  = "service"
              }
              rule {
                source_labels = ["__meta_kubernetes_pod_name"]
                target_label  = "pod"
              }
              rule {
                source_labels = ["__meta_kubernetes_pod_container_name"]
                target_label  = "container"
              }
            }
            loki.source.kubernetes "pods" {
              targets    = discovery.relabel.pods.output
              forward_to = [loki.write.default.receiver]
            }
            loki.write "default" {
              endpoint {
                url = "http://loki.${var.namespace}.svc.cluster.local:3100/loki/api/v1/push"
              }
            }
          RIVER
        }
      }
      controller = {
        type = "daemonset"
        tolerations = [{
          # Land on memory-tier nodes too (Scylla logs).
          key      = "workload"
          operator = "Equal"
          value    = "scylla"
          effect   = "NoSchedule"
        }]
      }
    })
  ]

  depends_on = [helm_release.loki]
}
