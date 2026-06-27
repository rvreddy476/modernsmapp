output "domain" {
  value = aws_codeartifact_domain.this.domain
}

output "repository" {
  value = aws_codeartifact_repository.web.repository
}

output "npm_endpoint" {
  description = "npm registry URL — put in .npmrc / `aws codeartifact login`."
  value       = data.aws_codeartifact_repository_endpoint.web_npm.repository_endpoint
}

output "policy_arn" {
  description = "Read/publish policy — attach to the CI role."
  value       = aws_iam_policy.rw.arn
}
