# Per-service IRSA roles. Each entry:
#   k8s_service_account: usually the service name (chart convention).
#   policy_arns: AWS API access this service actually needs.
#
# Default for every event producer/consumer: msk_client_iam_policy_arn.
# media-service additionally gets media_client_iam_policy_arn for S3.
# Services that DON'T need any AWS API (pure in-cluster talkers like
# graph-service) are omitted — their pods run with the default SA + no
# AWS permissions.

locals {
  # Standard policies attached to every event producer/consumer.
  policies_msk_only = [
    module.msk.client_iam_policy_arn,
  ]
  # Policies for the media path (uploads, transcode pulls).
  policies_msk_and_media = [
    module.msk.client_iam_policy_arn,
    module.media.client_iam_policy_arn,
  ]

  # Service name → policy set. Add a service here when its pod needs
  # direct AWS API access.
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

    # identity-platform
    "identity-auth-service"    = local.policies_msk_only
    "identity-user-service"    = local.policies_msk_only
    "identity-profile-service" = local.policies_msk_only

    # chat-service
    "chat-message-service" = local.policies_msk_only
    "chat-call-service"    = local.policies_msk_only
    "chat-ws-gateway"      = local.policies_msk_only
  }
}

module "service_irsa" {
  source = "../../modules/service-irsa"

  for_each = local.service_irsa_map

  environment         = "staging"
  service_name        = each.key
  oidc_provider_arn   = module.eks.oidc_provider_arn
  oidc_provider_url   = module.eks.oidc_provider_url
  k8s_service_account = each.key
  policy_arns         = each.value
}

# Surface the role ARNs as a map output. ArgoCD's Helm value templating
# can consume this, and ops can `terraform output service_irsa_role_arns`
# during the first cutover to populate the values files.
output "service_irsa_role_arns" {
  description = "Map of service name → IRSA role ARN. Wire into deploy/services/<svc>/values-staging.yaml::serviceAccount.irsaRoleArn."
  value       = { for k, m in module.service_irsa : k => m.role_arn }
}
