# Empty-shell secrets — one per deployable, named atpost/qa/<name> to match
# the `externalSecret.remoteKey` in deploy/services/*/values-qa.yaml, plus
# the two web images and the ArgoCD repository credential
# (atpost/qa/argocd-repo-modernsmapp). Terraform never writes a value: the
# seeder (`--env qa`) does, and External Secrets mirrors them into the
# cluster.
#
# Secrets that already exist elsewhere and are NOT shells:
#   atpost/qa/platform-auth             modules/auth-keys (generated)
#   atpost/qa/aurora/master             modules/aurora (generated)
#   atpost/qa/elasticache/auth          modules/elasticache (generated)
#   atpost/qa/opensearch/master         modules/opensearch (generated)
#   atpost/qa/media/cloudfront-signing  modules/media (generated)
#   atpost/qa/argocd/admin              modules/argocd (generated)
#   atpost/qa/grafana/admin             modules/observability (generated)
#   AmazonMSK_atpost-qa-atpost          modules/msk (generated; AWS-mandated name)

module "service_secrets" {
  source = "../../modules/service-secrets"

  environment             = var.environment
  names                   = concat(var.service_names, var.web_image_names, var.extra_secret_shells)
  recovery_window_in_days = var.secret_recovery_window_days
}

output "service_secret_arns" {
  value       = module.service_secrets.secret_arns
  description = "name → Secrets Manager ARN of the empty shell the seeder fills."
}

output "service_secret_names" {
  value = module.service_secrets.secret_names
}
