# Empty-shell Secrets Manager secrets, one per deployable: atpost/<env>/<name>.
#
# Every service chart has an ExternalSecret whose remoteKey is
# `atpost/prod/<service>`; External Secrets needs the secret to EXIST before
# it can mirror it, and the seeder (workstream W3) needs a target to write
# to. Terraform therefore creates the containers only — never a
# aws_secretsmanager_secret_version — so no generated service secret value
# ever enters Terraform state. The seeder's writes are invisible to
# Terraform: there is no managed version resource to drift.
#
# All shells share one CMK; its ARN goes on the External Secrets Operator
# decrypt list.

resource "aws_kms_key" "secrets" {
  description             = "atpost-${var.environment} per-service Secrets Manager secrets"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-service-secrets-kms"
  }
}

resource "aws_kms_alias" "secrets" {
  name          = "alias/atpost-${var.environment}-service-secrets"
  target_key_id = aws_kms_key.secrets.key_id
}

resource "aws_secretsmanager_secret" "shell" {
  for_each = toset(var.names)

  name                    = "atpost/${var.environment}/${each.key}"
  description             = "Runtime secrets for ${each.key} (JSON). Created empty by Terraform; filled by the seeder; read by External Secrets."
  kms_key_id              = aws_kms_key.secrets.arn
  recovery_window_in_days = var.recovery_window_in_days

  tags = {
    Name    = "atpost-${var.environment}-${each.key}"
    Service = each.key
  }

  lifecycle {
    # The seeder may re-tag or re-describe; a value written by the seeder
    # is a secret VERSION, which this resource never manages.
    ignore_changes = [description]
  }
}
