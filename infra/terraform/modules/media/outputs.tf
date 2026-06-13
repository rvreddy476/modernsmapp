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
