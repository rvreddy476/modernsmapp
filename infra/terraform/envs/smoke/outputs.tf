# Everything the local docker stack needs to be pointed at real AWS.
# `terraform output -json` after apply, then set the env vars in step 6 of
# docs/runbooks/aws-media-smoke.md.

output "s3_bucket" {
  description = "MINIO_BUCKET / S3_BUCKET"
  value       = module.media.bucket_name
}

output "cloudfront_domain" {
  description = "MEDIA_CDN_BASE_URL (prefix with https://)"
  value       = module.media.cloudfront_domain_name
}

output "client_iam_policy_arn" {
  description = "Attach to the smoke IAM user; it is the least-privilege policy the module already defines."
  value       = module.media.client_iam_policy_arn
}

output "kms_key_arn" {
  value = module.media.kms_key_arn
}
