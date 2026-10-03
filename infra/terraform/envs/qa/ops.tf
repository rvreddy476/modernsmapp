# Operations baseline: ops alert topic (the founder's email), AWS Backup for
# Aurora, CloudTrail, GuardDuty, AWS Budgets ($1,000/month limit).

module "ops" {
  source = "../../modules/ops-baseline"

  environment               = var.environment
  ops_alert_email           = var.ops_alert_email
  backup_resource_arns      = [module.aurora.cluster_arn]
  backup_retention_days     = var.backup_retention_days
  cloudtrail_retention_days = var.cloudtrail_retention_days
  budget_thresholds_usd     = var.budget_thresholds_usd
}

output "ops_alerts_topic_arn" { value = module.ops.ops_alerts_topic_arn }
output "backup_vault_name" { value = module.ops.backup_vault_name }
output "cloudtrail_bucket_name" { value = module.ops.cloudtrail_bucket_name }
output "guardduty_detector_id" { value = module.ops.guardduty_detector_id }
