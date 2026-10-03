output "ci_role_arn" {
  value       = aws_iam_role.ci.arn
  description = "Set as `role-to-assume` in the GitHub Actions aws-actions/configure-aws-credentials step."
}

output "github_oidc_provider_arn" {
  value = aws_iam_openid_connect_provider.github.arn
}

output "ci_role_name" {
  value       = aws_iam_role.ci.name
  description = "CI role name — for attaching extra policies (e.g. CodeArtifact)."
}

output "terraform_apply_role_arn" {
  value       = var.create_terraform_apply_role ? aws_iam_role.terraform_apply[0].arn : null
  description = "Set as repository secret AWS_TERRAFORM_ROLE_ARN; the apply workflow assumes it from the `prod` GitHub environment."
}
