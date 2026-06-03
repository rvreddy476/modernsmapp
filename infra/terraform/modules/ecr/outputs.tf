output "repository_urls" {
  description = "Map of service name -> ECR repo URL. CI uses this for docker push."
  value       = { for k, r in aws_ecr_repository.this : k => r.repository_url }
}

output "repository_arns" {
  description = "Map of service name -> ECR repo ARN. Used by IAM policies that scope CI permissions."
  value       = { for k, r in aws_ecr_repository.this : k => r.arn }
}
