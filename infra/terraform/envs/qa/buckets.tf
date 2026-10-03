# Service buckets (besides media, which lives in modules/media with
# CloudFront in front). Names carry a random suffix: values files take them
# from the outputs below, never a literal. QA: versioning off on all three
# (contract: versioning only on media).
#
#   live-recordings    LiveKit Cloud (QA project) egress writes recordings;
#                      live-service-v2 reads/deletes; media-service imports.
#   commerce-invoices  GST invoices for test-mode orders.
#   food-files         Feast menus / images.

module "bucket_live_recordings" {
  source = "../../modules/private-bucket"

  environment                 = var.environment
  purpose                     = "live-recordings"
  service_tag                 = "live-service-v2"
  versioning                  = false
  transition_to_ia_after_days = 30
  expire_after_days           = 90
}

module "bucket_commerce_invoices" {
  source = "../../modules/private-bucket"

  environment = var.environment
  purpose     = "commerce-invoices"
  service_tag = "commerce-service"
  versioning  = false
}

module "bucket_food_files" {
  source = "../../modules/private-bucket"

  environment = var.environment
  purpose     = "food-files"
  service_tag = "food-service"
  versioning  = false
}

# LiveKit Cloud egress needs an access key (it runs outside the account).
# Terraform creates neither the user nor the key (keys must not enter
# state): README.md "LiveKit egress credentials" creates the user by CLI and
# attaches THIS policy, which can only write recordings.
data "aws_iam_policy_document" "livekit_egress" {
  statement {
    sid    = "WriteRecordings"
    effect = "Allow"
    actions = [
      "s3:PutObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${module.bucket_live_recordings.bucket_arn}/*"]
  }

  statement {
    sid       = "ProbeBucket"
    effect    = "Allow"
    actions   = ["s3:ListBucket", "s3:GetBucketLocation", "s3:ListBucketMultipartUploads"]
    resources = [module.bucket_live_recordings.bucket_arn]
  }
}

resource "aws_iam_policy" "livekit_egress" {
  name        = "atpost-${var.environment}-livekit-egress-write"
  description = "Write-only access to the live-recordings bucket for the LiveKit Cloud egress user (created by CLI, see envs/qa/README.md)."
  policy      = data.aws_iam_policy_document.livekit_egress.json
}

output "live_recordings_bucket_name" { value = module.bucket_live_recordings.bucket_name }
output "live_recordings_bucket_arn" { value = module.bucket_live_recordings.bucket_arn }
output "commerce_invoices_bucket_name" { value = module.bucket_commerce_invoices.bucket_name }
output "commerce_invoices_bucket_arn" { value = module.bucket_commerce_invoices.bucket_arn }
output "food_files_bucket_name" { value = module.bucket_food_files.bucket_name }
output "food_files_bucket_arn" { value = module.bucket_food_files.bucket_arn }
output "livekit_egress_policy_arn" {
  value       = aws_iam_policy.livekit_egress.arn
  description = "Attach to the IAM user LiveKit Cloud egress uses (README: LiveKit egress credentials)."
}
