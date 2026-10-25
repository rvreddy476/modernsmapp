# Customer-managed KMS key for chat field encryption at rest.
#
# chat-service/services/message-service/internal/fieldkeys refuses to boot
# with APP_ENV=production unless CHAT_KMS_KEY_ID names a key it can use. The
# ring (chat-service/shared/fieldcrypt/kmsring.go) mints AES-256 master keys
# with GenerateDataKey, persists them WRAPPED in chat.field_key_ring and
# unwraps every version with Decrypt once at boot. KMS is never called per
# message.
#
# Same shape as modules/commerce-pii-kms, for the same reasons: a separate
# key makes chat-content decryption revocable without touching the database,
# auditable as its own CloudTrail stream, and unreachable from any other
# workload.
#
# The encryption context the application sets (fieldcrypt.KMSRing):
#
#   purpose     = "chat-field"        (fieldkeys.KMSPurpose)
#   scope       = "chat.field"        (fieldkeys.KMSRingScope)
#   environment = the CANONICAL APP_ENV: "prod"/"production" -> "production",
#                 anything else verbatim (fieldkeys.environment)
#
# QA runs with APP_ENV=production on purpose (deploy/services/
# chat-message-service/values-qa.yaml), so QA's context environment is
# "production" too. That is why the pinned value is its own variable and not
# the resource-name env: pinning "qa" would make every QA boot fail closed.
# QA and production are different AWS accounts with different keys, so the
# shared label does not let one unwrap the other's blobs.
#
# Principals: the env's chat-message-service IRSA role (envs/*/
# services-irsa.tf; one role per ServiceAccount). The key policy admits the
# ARNs in principal_role_arns; the env attaches client_policy_arn to the same
# role. The fieldcrypt-backfill job runs under that ServiceAccount too.

variable "env" {
  description = "Environment name used in resource names (prod, qa)."
  type        = string
}

variable "encryption_context_environment" {
  description = "The `environment` encryption-context value message-service sets: its canonical APP_ENV (\"production\" for APP_ENV=prod|production, else the value verbatim)."
  type        = string
}

variable "principal_role_arns" {
  description = "IAM role ARNs allowed to generate and unwrap data keys under this key (the env's chat-message-service IRSA role). Everyone else is denied by the key policy."
  type        = list(string)

  validation {
    condition     = length(var.principal_role_arns) > 0
    error_message = "At least one principal (chat-message-service's IRSA role) is required; a key nobody may use fails every boot."
  }
}

variable "deletion_window_days" {
  description = <<-EOT
    Days before a scheduled key deletion completes.

    The 30-day maximum. Deleting this key makes every sealed message body,
    attachment name and other encrypted chat field unreadable for good: the
    wrapped master keys in chat.field_key_ring can only be unwrapped by it.
  EOT
  type        = number
  default     = 30
}

locals {
  purpose = "chat-field"
  scope   = "chat.field"
}

data "aws_caller_identity" "current" {}

resource "aws_kms_key" "chat_field" {
  description = "atpost-${var.env}: chat field encryption at rest (envelope encryption)"

  # The application does AES-GCM itself; this key only wraps and unwraps
  # 32-byte master keys.
  key_usage                = "ENCRYPT_DECRYPT"
  customer_master_key_spec = "SYMMETRIC_DEFAULT"

  # New key material yearly; old wrapped blobs stay decryptable. The ring's
  # own versions (chat.field_key_ring) rotate independently.
  enable_key_rotation     = true
  deletion_window_in_days = var.deletion_window_days

  policy = data.aws_iam_policy_document.key_policy.json

  tags = {
    Environment = var.env
    Service     = "chat-message-service"
    DataClass   = "chat-content"
  }
}

resource "aws_kms_alias" "chat_field" {
  name          = "alias/atpost-${var.env}-chat-field"
  target_key_id = aws_kms_key.chat_field.key_id
}

data "aws_iam_policy_document" "key_policy" {
  # Account root keeps administrative control; without it the key can become
  # unmanageable.
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

  # chat-message-service may mint and unwrap data keys, and only under the
  # exact context the ring sets. kmsclient makes exactly these two calls;
  # it never calls Encrypt (the data key is generated, not imported) or
  # DescribeKey.
  statement {
    sid    = "AllowChatMessageServiceEnvelopeOperations"
    effect = "Allow"
    actions = [
      "kms:GenerateDataKey",
      "kms:Decrypt",
    ]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = var.principal_role_arns
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:purpose"
      values   = [local.purpose]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:scope"
      values   = [local.scope]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:environment"
      values   = [var.encryption_context_environment]
    }
  }

  # Nothing else in the account may unwrap chat keys, including roles whose
  # own policy grants a broad kms:Decrypt: only a key policy can deny them.
  statement {
    sid       = "DenyDataKeyUseToEveryoneElse"
    effect    = "Deny"
    actions   = ["kms:Decrypt", "kms:GenerateDataKey", "kms:Encrypt", "kms:ReEncrypt*"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
    condition {
      test     = "ArnNotEquals"
      variable = "aws:PrincipalArn"
      values = concat(
        var.principal_role_arns,
        ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"],
      )
    }
  }
}

# Identity-side half of the same grant, attached by the env to the
# chat-message-service IRSA role. Scoped to this key ARN only.
data "aws_iam_policy_document" "client" {
  statement {
    sid    = "ChatFieldEnvelope"
    effect = "Allow"
    actions = [
      "kms:GenerateDataKey",
      "kms:Decrypt",
    ]
    resources = [aws_kms_key.chat_field.arn]
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:purpose"
      values   = [local.purpose]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:scope"
      values   = [local.scope]
    }
    condition {
      test     = "StringEquals"
      variable = "kms:EncryptionContext:environment"
      values   = [var.encryption_context_environment]
    }
  }
}

resource "aws_iam_policy" "client" {
  name        = "atpost-${var.env}-chat-field-kms-client"
  description = "GenerateDataKey/Decrypt on the chat field key under the pinned encryption context. Attach to the chat-message-service IRSA role."
  policy      = data.aws_iam_policy_document.client.json
}

output "key_id" {
  description = "KMS key id for chat-message-service CHAT_KMS_KEY_ID."
  value       = aws_kms_key.chat_field.key_id
}

output "key_arn" {
  value = aws_kms_key.chat_field.arn
}

output "alias_name" {
  value = aws_kms_alias.chat_field.name
}

output "client_policy_arn" {
  description = "Managed policy to attach to the role(s) in principal_role_arns."
  value       = aws_iam_policy.client.arn
}
