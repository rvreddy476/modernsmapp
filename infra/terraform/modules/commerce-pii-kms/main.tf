# Customer-managed KMS key for Commerce address PII.
#
# Commerce P0 LB-24 / v1 §5.14 / review §5-D8.
#
# Customer names, phone numbers and street addresses were plaintext columns.
# Aurora's storage encryption protects a stolen volume; it does nothing about
# a query, a log line, an analytics export or a compromised read replica —
# which are the realistic ways address data actually leaks.
#
# Why a SEPARATE customer-managed key rather than the RDS key:
#
#   * Revocability. Decrypt on address PII can be withdrawn without taking
#     the database offline. Sharing the storage key makes that impossible.
#   * Auditability. Every decrypt is a distinct CloudTrail event against a
#     key whose only purpose is address PII, so "who read customer addresses
#     and when" is answerable.
#   * Blast radius. A compromised commerce pod can decrypt addresses. It
#     cannot decrypt anything else, and nothing else can decrypt addresses.
#
# Two GRANT SCOPES, not two keys: the encryption context separates profile
# addresses from order snapshots, so the profile scope can be retired
# without touching invoice history. Review §5-D8 is explicit that order
# snapshots may be legally required for years and must NOT be shredded on a
# product default.

variable "env" {
  description = "Environment name (prod, staging, smoke)."
  type        = string
}

variable "cluster_oidc_provider_arn" {
  description = "EKS OIDC provider ARN, for the IRSA trust policy."
  type        = string
}

variable "cluster_oidc_issuer" {
  description = "EKS OIDC issuer host/path, without the https:// prefix."
  type        = string
}

variable "namespace" {
  description = "Kubernetes namespace the commerce service account lives in."
  type        = string
  default     = "atpost"
}

variable "deletion_window_days" {
  description = <<-EOT
    Days before a scheduled key deletion completes.

    Deliberately the 30-day maximum. Deleting this key destroys every
    encrypted address AND every order address snapshot irrecoverably, and
    review §5-D8 rules that destructive shredding stays disabled until a
    CA/legal ruling. The long window is the last chance to notice.
  EOT
  type        = number
  default     = 30
}

resource "aws_kms_key" "commerce_pii" {
  description = "atpost-${var.env}: Commerce address PII (envelope encryption)"

  # The application generates data keys and does the AES-GCM itself, so this
  # key only ever wraps and unwraps 32-byte data keys.
  key_usage                = "ENCRYPT_DECRYPT"
  customer_master_key_spec = "SYMMETRIC_DEFAULT"

  # Rotation gives new material annually while old ciphertext stays
  # readable. The application's own `pii_key_version` column handles
  # application-level rotation independently.
  enable_key_rotation     = true
  deletion_window_in_days = var.deletion_window_days

  policy = data.aws_iam_policy_document.key_policy.json

  tags = {
    Environment = var.env
    Service     = "commerce-service"
    DataClass   = "pii-address"
    # Flags the key for the retention review that D8 leaves open.
    RetentionDecision = "pending-legal-review"
  }
}

resource "aws_kms_alias" "commerce_pii" {
  name          = "alias/atpost-${var.env}-commerce-pii"
  target_key_id = aws_kms_key.commerce_pii.key_id
}

data "aws_caller_identity" "current" {}

data "aws_iam_policy_document" "key_policy" {
  # Account root retains administrative control; without this statement the
  # key can become unmanageable.
  statement {
    sid       = "AllowAccountAdministration"
    effect    = "Allow"
    actions   = ["kms:*"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
  }

  # commerce-service may generate and unwrap data keys — and ONLY under an
  # encryption context that names a known scope. A ciphertext moved between
  # the profile and order-snapshot columns therefore fails to decrypt rather
  # than silently succeeding, which mirrors the AEAD additional-data check
  # the application performs in internal/pii.
  statement {
    sid    = "AllowCommerceServiceEnvelopeOperations"
    effect = "Allow"
    actions = [
      "kms:GenerateDataKey",
      "kms:Decrypt",
    ]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = [aws_iam_role.commerce_pii.arn]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:scope"
      values   = ["profile", "order_snapshot"]
    }
    # B3: purpose and environment are pinned too. Scope alone would let a
    # blob from another environment decrypt under this key if the ARN ever
    # leaked across boundaries.
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:purpose"
      values   = ["commerce-pii"]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:environment"
      values   = [var.env]
    }
  }

  # Nothing else in the account may decrypt address PII, including roles that
  # hold a broad kms:Decrypt in their own policy — a key policy is the only
  # place that can actually deny them.
  statement {
    sid       = "DenyDecryptToEveryoneElse"
    effect    = "Deny"
    actions   = ["kms:Decrypt", "kms:GenerateDataKey"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
    condition {
      test     = "ArnNotEquals"
      variable = "aws:PrincipalArn"
      values = [
        aws_iam_role.commerce_pii.arn,
        "arn:aws:iam::${data.aws_caller_identity.current.account_id}:root",
      ]
    }
  }
}

# IRSA role for the commerce service account.
#
# Note what this role is and is not. The review corrected a misconception in
# the v1 plan: IRSA authorises AWS API calls, and does NOT authenticate an
# HTTP request from commerce-service to payments-service. Service-to-service
# authentication is the Ed25519 token in shared/servicetoken; this role only
# lets commerce talk to KMS.
resource "aws_iam_role" "commerce_pii" {
  name = "atpost-${var.env}-commerce-pii-kms"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      Principal = {
        Federated = var.cluster_oidc_provider_arn
      }
      Action = "sts:AssumeRoleWithWebIdentity"
      Condition = {
        StringEquals = {
          "${var.cluster_oidc_issuer}:sub" = "system:serviceaccount:${var.namespace}:commerce-service"
          "${var.cluster_oidc_issuer}:aud" = "sts.amazonaws.com"
        }
      }
    }]
  })

  tags = {
    Environment = var.env
    Service     = "commerce-service"
  }
}

resource "aws_iam_role_policy" "commerce_pii" {
  name = "commerce-pii-kms"
  role = aws_iam_role.commerce_pii.id

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect = "Allow"
      # B3 — least privilege, and only what the adapter actually calls.
      #
      # DescribeKey was removed: internal/kmsclient makes exactly two calls,
      # GenerateDataKey and Decrypt. A granted permission nothing uses is a
      # permission nobody will notice being used.
      Action = [
        "kms:GenerateDataKey",
        "kms:Decrypt",
      ]
      Resource = aws_kms_key.commerce_pii.arn
      Condition = {
        StringEquals = {
          # All three context fields the application sets are pinned. The
          # environment binding is what stops a staging role, if it ever
          # obtained this key ARN, from unwrapping a production blob: KMS
          # verifies the context byte-for-byte, so a prod ciphertext simply
          # does not decrypt under a staging context.
          "kms:EncryptionContext:purpose"     = "commerce-pii"
          "kms:EncryptionContext:environment" = var.env
          "kms:EncryptionContext:scope"       = ["profile", "order_snapshot"]
        }
      }
    }]
  })
}

output "key_id" {
  description = "KMS key id for COMMERCE_KMS_KEY_ID."
  value       = aws_kms_key.commerce_pii.key_id
}

output "key_arn" {
  value = aws_kms_key.commerce_pii.arn
}

output "irsa_role_arn" {
  description = "Role ARN for the commerce-service ServiceAccount annotation."
  value       = aws_iam_role.commerce_pii.arn
}

output "policy_arn_note" {
  description = <<-EOT
    The commerce IRSA role in services-irsa.tf must ALSO carry this role's
    permissions, or be replaced by it. Address decryption fails closed: the
    service refuses to start in prod without a usable key, so a missing grant
    is a failed rollout rather than a silent plaintext fallback.
  EOT
  value       = aws_iam_role.commerce_pii.arn
}
