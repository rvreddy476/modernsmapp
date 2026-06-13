output "role_arn" {
  value       = aws_iam_role.this.arn
  description = "Feed to the chart's serviceAccount.irsaRoleArn value."
}

output "role_name" {
  value = aws_iam_role.this.name
}
