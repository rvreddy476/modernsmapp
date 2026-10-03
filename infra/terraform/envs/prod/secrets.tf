# Empty-shell secrets — one per deployable, named atpost/prod/<name> to match
# the `externalSecret.remoteKey` in deploy/services/*/values-prod.yaml, plus
# the two web images. Terraform never writes a value: the seeder (W3) does,
# and External Secrets mirrors them into the cluster.
#
# Secrets that already exist elsewhere and are NOT shells:
#   atpost/prod/platform-auth           modules/auth-keys (generated)
#   atpost/prod/aurora/master           modules/aurora (generated)
#   atpost/prod/elasticache/auth        modules/elasticache (generated)
#   atpost/prod/opensearch/master       modules/opensearch (generated)
#   atpost/prod/media/cloudfront-signing modules/media (generated)
#   AmazonMSK_atpost-prod-atpost        modules/msk (generated; AWS-mandated name)

module "service_secrets" {
  source = "../../modules/service-secrets"

  environment             = "prod"
  names                   = concat(var.service_names, var.web_image_names)
  recovery_window_in_days = var.secret_recovery_window_days
}

output "service_secret_arns" {
  value       = module.service_secrets.secret_arns
  description = "name → Secrets Manager ARN of the empty shell the seeder fills."
}

output "service_secret_names" {
  value = module.service_secrets.secret_names
}
