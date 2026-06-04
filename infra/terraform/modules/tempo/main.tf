# Grafana Tempo — distributed-trace backend. The shared/o11y/trace
# package already exports W3C trace context through HTTP + Kafka, so
# every service is already emitting spans; this module is the place
# they land.
#
# Single-binary mode (tempo, not tempo-distributed) — simplest deploy,
# fits well below 1B spans/day. Scale to tempo-distributed when
# ingestion rate justifies it.
#
# Storage: S3 bucket via IRSA. Tempo's hash-ring + WAL handle hot
# spans; S3 holds the long-term blocks.

resource "aws_s3_bucket" "tempo" {
  bucket = "atpost-${var.environment}-tempo-${var.random_suffix}"

  tags = {
    Name = "atpost-${var.environment}-tempo"
  }
}

resource "aws_s3_bucket_versioning" "tempo" {
  bucket = aws_s3_bucket.tempo.id
  versioning_configuration {
    status = "Suspended" # trace data is throwaway; versioning would just bloat cost
  }
}

resource "aws_s3_bucket_public_access_block" "tempo" {
  bucket                  = aws_s3_bucket.tempo.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "tempo" {
  bucket = aws_s3_bucket.tempo.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "tempo" {
  bucket = aws_s3_bucket.tempo.id

  rule {
    id     = "trace-retention"
    status = "Enabled"

    expiration {
      days = var.retention_days
    }

    abort_incomplete_multipart_upload {
      days_after_initiation = 1
    }
  }
}

# IRSA for the Tempo pod.
data "aws_iam_policy_document" "tempo_trust" {
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
      values   = ["system:serviceaccount:${var.namespace}:tempo"]
    }
  }
}

resource "aws_iam_role" "tempo" {
  name               = "atpost-${var.environment}-tempo"
  assume_role_policy = data.aws_iam_policy_document.tempo_trust.json
}

data "aws_iam_policy_document" "tempo_s3" {
  statement {
    actions = [
      "s3:ListBucket",
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
    ]
    resources = [
      aws_s3_bucket.tempo.arn,
      "${aws_s3_bucket.tempo.arn}/*",
    ]
  }
}

resource "aws_iam_role_policy" "tempo_s3" {
  name   = "s3-access"
  role   = aws_iam_role.tempo.id
  policy = data.aws_iam_policy_document.tempo_s3.json
}

resource "helm_release" "tempo" {
  name       = "tempo"
  repository = "https://grafana.github.io/helm-charts"
  chart      = "tempo"
  version    = var.chart_version
  namespace  = var.namespace

  values = [
    yamlencode({
      tempo = {
        # S3 backend instead of the default local PVC.
        storage = {
          trace = {
            backend = "s3"
            s3 = {
              bucket   = aws_s3_bucket.tempo.id
              endpoint = "s3.${var.aws_region}.amazonaws.com"
              region   = var.aws_region
            }
          }
        }
        # Receivers: OTLP gRPC + HTTP. The shared/o11y/trace package
        # talks gRPC; the HTTP receiver is there for ad-hoc curl.
        receivers = {
          otlp = {
            protocols = {
              grpc = { endpoint = "0.0.0.0:4317" }
              http = { endpoint = "0.0.0.0:4318" }
            }
          }
        }
      }
      serviceAccount = {
        create = true
        name   = "tempo"
        annotations = {
          "eks.amazonaws.com/role-arn" = aws_iam_role.tempo.arn
        }
      }
      nodeSelector = {
        workload = "system"
      }
      # Grafana datasource auto-discovery. The kube-prometheus-stack
      # Grafana sidecar watches for ConfigMaps with this label and
      # imports them as datasources at runtime.
      ingester = {
        # Persist the WAL on gp3 — 50 GB is plenty for the hash-ring
        # window before spans flush to S3.
        persistence = {
          enabled          = true
          storageClassName = "gp3"
          size             = "50Gi"
        }
      }
    })
  ]
}

# Datasource ConfigMap — picked up by Grafana's sidecar via the
# `grafana_datasource` label, no admin click needed.
resource "kubernetes_config_map" "grafana_datasource" {
  metadata {
    name      = "tempo-datasource"
    namespace = var.namespace
    labels = {
      grafana_datasource = "1"
    }
  }
  data = {
    "tempo-datasource.yaml" = <<-YAML
      apiVersion: 1
      datasources:
        - name: Tempo
          type: tempo
          access: proxy
          url: http://tempo.${var.namespace}.svc.cluster.local:3100
          editable: false
    YAML
  }
}
