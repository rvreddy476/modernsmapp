output "ops_alerts_topic_arn" {
  value       = aws_sns_topic.ops_alerts.arn
  description = "SNS topic for anything that should reach a human. SES bounces publish here."
}

output "backup_vault_name" {
  value = aws_backup_vault.this.name
}

output "backup_plan_id" {
  value = aws_backup_plan.daily.id
}

output "cloudtrail_bucket_name" {
  value = aws_s3_bucket.trail.id
}

output "cloudtrail_arn" {
  value = aws_cloudtrail.this.arn
}

output "guardduty_detector_id" {
  value = aws_guardduty_detector.this.id
}

output "budget_name" {
  value = aws_budgets_budget.monthly.name
}
