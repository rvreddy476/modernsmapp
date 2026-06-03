# External Secrets Operator — bridges AWS Secrets Manager into
# kubernetes Secrets. App pods consume regular kubernetes Secret /
# secretKeyRef references; ESO reconciles them against the upstream
# AWS Secrets Manager entries created by the Aurora / ElastiCache /
# OpenSearch modules.
#
# Auth: IRSA. The Helm chart installs into the `external-secrets`
# namespace with a ServiceAccount whose IRSA annotation points at
# the IAM role here, which grants secretsmanager:GetSecretValue +
# kms:Decrypt on every atpost-${env}/* secret.
#
# Why this matters: without ESO, every service Helm chart would need
# to either bake secrets into env vars at deploy time (audit trail
# nightmare) or hand-mount them via a sidecar. ESO turns it into a
# declarative "I need this secret, here's the AWS source" — Aurora
# password rotation flows through automatically.

resource "kubernetes_namespace" "external_secrets" {
  metadata {
    name = "external-secrets"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

# IRSA role: trust the EKS OIDC provider, allow assume from the
# external-secrets / external-secrets ServiceAccount only.
data "aws_iam_policy_document" "eso_trust" {
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
      values   = ["system:serviceaccount:external-secrets:external-secrets"]
    }
  }
}

resource "aws_iam_role" "eso" {
  name               = "atpost-${var.environment}-external-secrets"
  assume_role_policy = data.aws_iam_policy_document.eso_trust.json

  tags = {
    Name = "atpost-${var.environment}-external-secrets"
  }
}

# Permissions: read every atpost-${env}/* secret + use the KMS keys
# the secrets are encrypted with. Wildcard scoped to the env prefix
# so ESO can't read other envs' secrets.
data "aws_iam_policy_document" "eso_secrets" {
  statement {
    sid    = "ReadSecrets"
    effect = "Allow"
    actions = [
      "secretsmanager:GetSecretValue",
      "secretsmanager:DescribeSecret",
      "secretsmanager:ListSecrets", # required by some PushSecret flows
    ]
    resources = [
      "arn:aws:secretsmanager:*:*:secret:atpost/${var.environment}/*",
    ]
  }

  statement {
    sid    = "KMSDecrypt"
    effect = "Allow"
    actions = [
      "kms:Decrypt",
      "kms:DescribeKey",
    ]
    resources = var.kms_key_arns
  }
}

resource "aws_iam_role_policy" "eso_secrets" {
  name   = "secrets-access"
  role   = aws_iam_role.eso.id
  policy = data.aws_iam_policy_document.eso_secrets.json
}

# Helm chart. Pin to a known-stable version; the ESO project bumps
# the chart often + the CRDs occasionally have breaking renames.
resource "helm_release" "external_secrets" {
  name       = "external-secrets"
  repository = "https://charts.external-secrets.io"
  chart      = "external-secrets"
  version    = var.chart_version
  namespace  = kubernetes_namespace.external_secrets.metadata[0].name

  # Install the CRDs as part of the chart — simpler than the separate
  # external-secrets-crds chart, and the CRD upgrades stay in sync
  # with the operator version.
  set {
    name  = "installCRDs"
    value = "true"
  }

  # IRSA annotation on the ServiceAccount.
  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = aws_iam_role.eso.arn
  }

  # Affinity to system node group — ESO is platform tooling, not app
  # workload; keep it off the general pool.
  set {
    name  = "nodeSelector.workload"
    value = "system"
  }
  set {
    name  = "webhook.nodeSelector.workload"
    value = "system"
  }
  set {
    name  = "certController.nodeSelector.workload"
    value = "system"
  }

  # Modest replica count — ESO is webhook + controller, no extreme HA
  # needed. 2 replicas survive a node restart without dropping
  # reconciliation.
  set {
    name  = "replicaCount"
    value = "2"
  }

  depends_on = [aws_iam_role_policy.eso_secrets]
}

# ClusterSecretStore — the cluster-wide pointer at AWS Secrets Manager.
# Service Helm charts reference this by name in their ExternalSecret
# resources; this way no per-service AWS auth config is repeated.
resource "kubernetes_manifest" "secret_store" {
  manifest = {
    apiVersion = "external-secrets.io/v1beta1"
    kind       = "ClusterSecretStore"
    metadata = {
      name = "aws-secrets-manager"
    }
    spec = {
      provider = {
        aws = {
          service = "SecretsManager"
          region  = var.aws_region
          auth = {
            jwt = {
              serviceAccountRef = {
                name      = "external-secrets"
                namespace = "external-secrets"
              }
            }
          }
        }
      }
    }
  }

  depends_on = [helm_release.external_secrets]
}
