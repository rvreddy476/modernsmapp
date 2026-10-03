variable "environment" {
  type = string
}

variable "ops_alert_email" {
  description = "Address that receives budget, backup, GuardDuty and SES bounce alerts. SNS sends a confirmation link that must be clicked."
  type        = string
}

variable "backup_resource_arns" {
  description = "Resource ARNs for the daily backup selection (the Aurora cluster ARN)."
  type        = list(string)
}

variable "backup_retention_days" {
  description = "Days AWS Backup keeps each daily recovery point."
  type        = number
  default     = 14
}

variable "backup_schedule_cron" {
  description = "AWS Backup schedule (UTC). 19:30 UTC = 01:00 IST, after the Aurora backup window."
  type        = string
  default     = "cron(30 19 * * ? *)"
}

variable "cloudtrail_retention_days" {
  description = "Days CloudTrail objects stay in S3."
  type        = number
  default     = 90
}

variable "budget_thresholds_usd" {
  description = "Monthly spend thresholds (USD) that each email the ops address when ACTUAL spend passes them. The largest is the budget limit."
  type        = list(number)
  default     = [500, 1000, 1500]
}
