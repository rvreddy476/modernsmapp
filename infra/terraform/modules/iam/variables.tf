variable "environment" {
  type = string
}

variable "github_repos" {
  description = <<EOT
GitHub repos allowed to assume the CI role. Format: `org/repo`. Example:
  ["anthropics/atpost", "anthropics/postbook-ui"]
The trust policy uses `repo:<org>/<repo>:*` — i.e. ANY branch/PR can plan.
For prod-apply, narrow this to `repo:<org>/<repo>:ref:refs/heads/main`
in a separate, stricter role.
EOT
  type        = list(string)
}

variable "ecr_repository_arns" {
  description = "ECR repo ARNs the CI role may push to. Wired from the ecr module's outputs."
  type        = list(string)
}

variable "tfstate_bucket_arn" {
  description = "S3 bucket ARN holding remote state — CI plan needs read+write here."
  type        = string
}

variable "tfstate_lock_table_arn" {
  description = "DynamoDB table ARN for Terraform state lock."
  type        = string
}
