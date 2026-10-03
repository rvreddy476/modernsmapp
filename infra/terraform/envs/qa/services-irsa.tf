# Per-service IRSA roles — QA. Same policy sets as envs/prod/services-irsa.tf
# (what each service's code actually calls):
#
#   identity-auth-service   SES send (no-reply@qa.cleestudio.com, one config set)
#   media-service           media bucket + KMS, Rekognition, READ on live-recordings
#                           (the worker shares media-service's ServiceAccount)
#   commerce-service        commerce PII KMS key, commerce-invoices bucket
#   food-service            food-files bucket
#   live-service-v2         live-recordings bucket
#   everyone else           nothing — Kafka is SASL/SCRAM, OpenSearch basic auth
#
# Every service still gets a role so the chart's serviceAccount.irsaRoleArn
# is uniform: arn:aws:iam::<account>:role/atpost-qa-<service>-irsa.

# Rekognition has no resource-level permissions for the Detect*/Compare
# APIs; the action list is the whole scope.
data "aws_iam_policy_document" "rekognition" {
  statement {
    sid    = "MediaModerationAndFaces"
    effect = "Allow"
    actions = [
      "rekognition:DetectModerationLabels",
      "rekognition:DetectFaces",
      "rekognition:CompareFaces",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_policy" "rekognition" {
  name        = "atpost-${var.environment}-rekognition"
  description = "DetectModerationLabels, DetectFaces, CompareFaces for media-service and media-worker."
  policy      = data.aws_iam_policy_document.rekognition.json
}

locals {
  policies_none = []

  policies_media = [
    module.media.client_iam_policy_arn,
    aws_iam_policy.rekognition.arn,
    module.bucket_live_recordings.reader_iam_policy_arn, # imports recordings into videos
  ]

  service_irsa_map = {
    "post-service"         = local.policies_none
    "user-service"         = local.policies_none
    "feed-service"         = local.policies_none
    "media-service"        = local.policies_media
    "commerce-service"     = [module.bucket_commerce_invoices.client_iam_policy_arn]
    "food-service"         = [module.bucket_food_files.client_iam_policy_arn]
    "payments-service"     = local.policies_none
    "notification-service" = local.policies_none
    "search-service"       = local.policies_none
    "suggestion-service"   = local.policies_none
    "analytics-service"    = local.policies_none
    "graph-service"        = local.policies_none
    "trust-safety-service" = local.policies_none
    "monetization-service" = local.policies_none
    "community-service"    = local.policies_none
    "channel-service"      = local.policies_none
    "group-service"        = local.policies_none
    "qa-service"           = local.policies_none
    "live-service-v2"      = [module.bucket_live_recordings.client_iam_policy_arn]
    "admin-service"        = local.policies_none
    "ai-service"           = local.policies_none
    "bill-pay-service"     = local.policies_none
    "dating-service"       = local.policies_none
    "rider-service"        = local.policies_none
    "wallet-service"       = local.policies_none
    "api-gateway"          = local.policies_none

    "identity-auth-service"    = [module.ses.send_policy_arn]
    "identity-user-service"    = local.policies_none
    "identity-profile-service" = local.policies_none

    "chat-message-service" = local.policies_none
    "chat-call-service"    = local.policies_none
    "chat-ws-gateway"      = local.policies_none
  }
}

module "service_irsa" {
  source = "../../modules/service-irsa"

  for_each = local.service_irsa_map

  environment         = var.environment
  service_name        = each.key
  oidc_provider_arn   = module.eks.oidc_provider_arn
  oidc_provider_url   = module.eks.oidc_provider_url
  k8s_service_account = each.key
  policy_arns         = each.value
}

# The commerce PII key policy names the commerce role as its principal
# (main.tf, module.commerce_pii_kms); this attaches the identity-side half.
resource "aws_iam_role_policy_attachment" "commerce_pii_kms" {
  role       = module.service_irsa["commerce-service"].role_name
  policy_arn = module.commerce_pii_kms.client_policy_arn
}

output "service_irsa_role_arns" {
  description = "Map of service name → IRSA role ARN. Wire into deploy/services/<svc>/values-qa.yaml::serviceAccount.irsaRoleArn (the media worker runs under media-service's ServiceAccount)."
  value       = { for k, m in module.service_irsa : k => m.role_arn }
}
