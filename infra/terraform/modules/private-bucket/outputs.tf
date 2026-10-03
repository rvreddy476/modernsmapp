output "bucket_name" {
  value       = aws_s3_bucket.this.id
  description = "Bucket name — pass into the owning service's values (never hard-code it)."
}

output "bucket_arn" {
  value = aws_s3_bucket.this.arn
}

output "bucket_regional_domain_name" {
  value = aws_s3_bucket.this.bucket_regional_domain_name
}

output "client_iam_policy_arn" {
  value       = aws_iam_policy.client.arn
  description = "Read/write client policy. Attach to the owning service's IRSA role."
}

output "reader_iam_policy_arn" {
  value       = aws_iam_policy.reader.arn
  description = "Read-only policy (GetObject + ListBucket). For services that import from the bucket."
}
