# Platform auth crypto — generated once, stored in Secrets Manager, consumed by
# services via External Secrets. Automates the material that was previously
# hand-created: the HS256 shared secret, the service-to-service internal key,
# and the RS256 signing keypair (private for auth-service, public for verifiers).
#
# SECURITY TRADE-OFF (read before adopting): generating these in Terraform means
# the values land in TF *state*. That is acceptable only because state lives in
# the encrypted, access-restricted S3 backend — treat state as a secret. If your
# bar is "the token-signing private key must never touch TF state", set
# var.manage_values = false: this module then creates the empty secret container
# (with lifecycle ignore on the value) and you populate it out-of-band.

resource "tls_private_key" "rs256" {
  count     = var.manage_values ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 2048
}

resource "random_password" "jwt_secret" {
  count   = var.manage_values ? 1 : 0
  length  = 64
  special = false
}

resource "random_password" "internal_service_key" {
  count   = var.manage_values ? 1 : 0
  length  = 48
  special = false
}

# Dedicated CMK so this secret has its own key. Its ARN is fed to the External
# Secrets Operator's decrypt policy (see envs/*/main.tf external_secrets call).
resource "aws_kms_key" "auth" {
  description             = "atpost ${var.environment} platform auth secrets"
  deletion_window_in_days = 14
  enable_key_rotation     = true

  tags = {
    Name = "atpost-${var.environment}-auth"
  }
}

resource "aws_kms_alias" "auth" {
  name          = "alias/atpost-${var.environment}-auth"
  target_key_id = aws_kms_key.auth.key_id
}

resource "aws_secretsmanager_secret" "auth" {
  name                    = "atpost/${var.environment}/platform-auth"
  description             = "Shared auth crypto (JWT secret, internal key, RS256 keypair). Read by External Secrets Operator."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.auth.arn

  tags = {
    Name = "atpost-${var.environment}-platform-auth"
  }
}

resource "aws_secretsmanager_secret_version" "auth" {
  count     = var.manage_values ? 1 : 0
  secret_id = aws_secretsmanager_secret.auth.id
  secret_string = jsonencode({
    jwt_secret           = random_password.jwt_secret[0].result
    jwt_kid              = var.jwt_kid
    internal_service_key = random_password.internal_service_key[0].result
    jwt_private_key_pem  = tls_private_key.rs256[0].private_key_pem
    jwt_public_key_pem   = tls_private_key.rs256[0].public_key_pem
    jwt_rs256_kid        = var.jwt_rs256_kid
  })

  # Don't fight a manual rotation done in the console / by a rotation lambda.
  lifecycle {
    ignore_changes = [secret_string]
  }
}
