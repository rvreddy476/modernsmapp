output "ci_role_arn" {
  value       = aws_iam_role.ci.arn
  description = "Set as `role-to-assume` in the GitHub Actions aws-actions/configure-aws-credentials step."
}

output "github_oidc_provider_arn" {
  value = aws_iam_openid_connect_provider.github.arn
}
