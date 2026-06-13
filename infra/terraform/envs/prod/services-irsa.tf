# Per-service IRSA roles — prod. Mirror of envs/staging/services-irsa.tf
# by design; structural drift between envs is regret.

locals {
  policies_msk_only = [
    module.msk.client_iam_policy_arn,
  ]
  policies_msk_and_media = [
    module.msk.client_iam_policy_arn,
    module.media.client_iam_policy_arn,
  ]

  service_irsa_map = {
    "post-service"         = local.policies_msk_only
    "user-service"         = local.policies_msk_only
    "feed-service"         = local.policies_msk_only
    "media-service"        = local.policies_msk_and_media
    "commerce-service"     = local.policies_msk_only
    "payments-service"     = local.policies_msk_only
    "notification-service" = local.policies_msk_only
    "search-service"       = local.policies_msk_only
    "analytics-service"    = local.policies_msk_only
    "graph-service"        = local.policies_msk_only
    "trust-safety-service" = local.policies_msk_only
    "monetization-service" = local.policies_msk_only
    "community-service"    = local.policies_msk_only
    "channel-service"      = local.policies_msk_only
    "group-service"        = local.policies_msk_only
    "qa-service"           = local.policies_msk_only
    "live-service-v2"      = local.policies_msk_only
    "admin-service"        = local.policies_msk_only
    "ai-service"           = local.policies_msk_only
    "bill-pay-service"     = local.policies_msk_only
    "dating-service"       = local.policies_msk_only
    "rider-service"        = local.policies_msk_only
    "wallet-service"       = local.policies_msk_only
    "api-gateway"          = local.policies_msk_only

    "identity-auth-service"    = local.policies_msk_only
    "identity-user-service"    = local.policies_msk_only
    "identity-profile-service" = local.policies_msk_only

    "chat-message-service" = local.policies_msk_only
    "chat-call-service"    = local.policies_msk_only
    "chat-ws-gateway"      = local.policies_msk_only
  }
}

module "service_irsa" {
  source = "../../modules/service-irsa"

  for_each = local.service_irsa_map

  environment         = "prod"
  service_name        = each.key
  oidc_provider_arn   = module.eks.oidc_provider_arn
  oidc_provider_url   = module.eks.oidc_provider_url
  k8s_service_account = each.key
  policy_arns         = each.value
}

output "service_irsa_role_arns" {
  description = "Map of service name → IRSA role ARN. Wire into deploy/services/<svc>/values-prod.yaml::serviceAccount.irsaRoleArn."
  value       = { for k, m in module.service_irsa : k => m.role_arn }
}
