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

variable "ci_github_subjects" {
  description = <<EOT
Exact GitHub OIDC `sub` patterns (StringLike) allowed to assume the CI role.
When non-empty this REPLACES the any-branch `repo:<org>/<repo>:*` shape
built from github_repos. Prod example:
  ["repo:ORG/atpost:ref:refs/heads/main",
   "repo:ORG/atpost:ref:refs/heads/release/prod",
   "repo:ORG/atpost:environment:prod"]
EOT
  type        = list(string)
  default     = []
}

variable "create_terraform_apply_role" {
  description = "Create the Terraform apply role (AdministratorAccess + guard-rail denies) assumable only by apply_github_subjects."
  type        = bool
  default     = false
}

variable "apply_github_subjects" {
  description = "Exact GitHub OIDC `sub` values (StringEquals) that may assume the apply role. Prod: [\"repo:ORG/atpost:environment:prod\"]; QA: [\"repo:ORG/REPO:environment:qa\"] (envs/qa derives it from terraform_apply_github_environment)."
  type        = list(string)
  default     = []
}
