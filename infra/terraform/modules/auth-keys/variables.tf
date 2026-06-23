variable "environment" {
  type = string
}

variable "manage_values" {
  description = <<EOT
When true (default) Terraform generates the secret values (HS256 secret, internal
key, RS256 keypair) — convenient + reproducible, but the values land in TF state
(keep state encrypted + locked down). Set false to have TF create only the empty
secret container so you can populate it out-of-band and keep the signing key out
of state entirely.
EOT
  type        = bool
  default     = true
}

variable "jwt_kid" {
  description = "HS256 key id stamped on tokens (matches services' JWT_KID)."
  type        = string
  default     = "v1"
}

variable "jwt_rs256_kid" {
  description = "RS256 key id (matches services' JWT_RS256_KID)."
  type        = string
  default     = "rsa-1"
}
