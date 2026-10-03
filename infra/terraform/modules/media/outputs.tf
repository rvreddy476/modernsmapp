output "bucket_name" {
  value = aws_s3_bucket.media.id
}

output "bucket_arn" {
  value = aws_s3_bucket.media.arn
}

output "bucket_regional_domain_name" {
  value = aws_s3_bucket.media.bucket_regional_domain_name
}

output "kms_key_arn" {
  value = aws_kms_key.media.arn
}

output "cloudfront_domain_name" {
  value       = aws_cloudfront_distribution.media.domain_name
  description = "CloudFront-issued *.cloudfront.net hostname. Front with a custom domain via Cloudflare CNAME post-cutover."
}

output "cloudfront_distribution_arn" {
  value = aws_cloudfront_distribution.media.arn
}

output "cloudfront_distribution_id" {
  value       = aws_cloudfront_distribution.media.id
  description = "Use to invalidate paths after large content migrations."
}

output "client_iam_policy_arn" {
  value       = aws_iam_policy.media_client.arn
  description = "Standard media client IAM policy. Attach to media-service IRSA role."
}

output "cloudfront_key_pair_id" {
  value       = aws_cloudfront_public_key.signing.id
  description = "MEDIA_CLOUDFRONT_KEY_PAIR_ID for media-service."
}

output "cloudfront_key_group_id" {
  value = aws_cloudfront_key_group.signing.id
}

output "cloudfront_signing_secret_arn" {
  value       = aws_secretsmanager_secret.signing.arn
  description = "Secrets Manager ARN holding the key pair id + private key (atpost/<env>/media/cloudfront-signing). The seeder copies it into the media-service secret."
}

output "cdn_base_url" {
  value       = local.custom_domain_enabled ? "https://${var.custom_domain}" : "https://${aws_cloudfront_distribution.media.domain_name}"
  description = "MEDIA_CDN_BASE_URL for media-service."
}

output "custom_domain" {
  value = var.custom_domain
}
