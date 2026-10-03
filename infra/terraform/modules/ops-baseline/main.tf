# Operations baseline for a single production account:
#   - an SNS topic that emails the founder (budgets, SES bounces, anything
#     else that wants a human);
#   - AWS Backup: daily Aurora backup kept 14 days, in its own vault;
#   - CloudTrail: all regions, management events, log-file validation,
#     to a private S3 bucket that expires objects after 90 days;
#   - GuardDuty: the detector with S3 and EKS protection on;
#   - AWS Budgets: one monthly cost budget with alerts at each threshold.
#
# None of this is multi-account / Organizations aware on purpose — there is
# one account. CloudTrail here is not an organization trail.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

# ─── Ops alert topic ────────────────────────────────────────────────
resource "aws_sns_topic" "ops_alerts" {
  name = "atpost-${var.environment}-ops-alerts"

  tags = {
    Name = "atpost-${var.environment}-ops-alerts"
  }
}

# Budgets, SES and CloudWatch alarms publish here.
data "aws_iam_policy_document" "ops_alerts" {
  statement {
    sid     = "AccountPublish"
    effect  = "Allow"
    actions = ["sns:Publish"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
    resources = [aws_sns_topic.ops_alerts.arn]
  }

  statement {
    sid     = "ServicePublish"
    effect  = "Allow"
    actions = ["sns:Publish"]
    principals {
      type = "Service"
      identifiers = [
        "budgets.amazonaws.com",
        "ses.amazonaws.com",
        "cloudwatch.amazonaws.com",
        "backup.amazonaws.com",
        "events.amazonaws.com",
      ]
    }
    resources = [aws_sns_topic.ops_alerts.arn]
    condition {
      test     = "StringEquals"
      variable = "aws:SourceAccount"
      values   = [data.aws_caller_identity.current.account_id]
    }
  }
}

resource "aws_sns_topic_policy" "ops_alerts" {
  arn    = aws_sns_topic.ops_alerts.arn
  policy = data.aws_iam_policy_document.ops_alerts.json
}

# The founder must click the confirmation link SNS sends to this address
# before any alert is delivered.
resource "aws_sns_topic_subscription" "ops_email" {
  topic_arn = aws_sns_topic.ops_alerts.arn
  protocol  = "email"
  endpoint  = var.ops_alert_email
}

# ─── AWS Backup (Aurora) ────────────────────────────────────────────
resource "aws_backup_vault" "this" {
  name = "atpost-${var.environment}-vault"

  tags = {
    Name = "atpost-${var.environment}-backup-vault"
  }
}

resource "aws_backup_plan" "daily" {
  name = "atpost-${var.environment}-daily"

  rule {
    rule_name         = "daily-${var.backup_retention_days}d"
    target_vault_name = aws_backup_vault.this.name
    schedule          = var.backup_schedule_cron
    start_window      = 60  # minutes
    completion_window = 360 # minutes

    lifecycle {
      delete_after = var.backup_retention_days
    }
  }

  tags = {
    Name = "atpost-${var.environment}-daily"
  }
}

data "aws_iam_policy_document" "backup_trust" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["backup.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "backup" {
  name               = "atpost-${var.environment}-backup"
  assume_role_policy = data.aws_iam_policy_document.backup_trust.json

  tags = {
    Name = "atpost-${var.environment}-backup"
  }
}

resource "aws_iam_role_policy_attachment" "backup" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForBackup"
}

resource "aws_iam_role_policy_attachment" "restore" {
  role       = aws_iam_role.backup.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSBackupServiceRolePolicyForRestores"
}

resource "aws_backup_selection" "aurora" {
  name         = "aurora"
  plan_id      = aws_backup_plan.daily.id
  iam_role_arn = aws_iam_role.backup.arn
  resources    = var.backup_resource_arns
}

# Backup job failures → ops topic.
resource "aws_backup_vault_notifications" "this" {
  backup_vault_name   = aws_backup_vault.this.name
  sns_topic_arn       = aws_sns_topic.ops_alerts.arn
  backup_vault_events = ["BACKUP_JOB_FAILED", "RESTORE_JOB_FAILED", "BACKUP_JOB_EXPIRED"]
}

# ─── CloudTrail ─────────────────────────────────────────────────────
resource "random_id" "trail_suffix" {
  byte_length = 4
}

resource "aws_s3_bucket" "trail" {
  bucket = "atpost-${var.environment}-cloudtrail-${random_id.trail_suffix.hex}"

  tags = {
    Name = "atpost-${var.environment}-cloudtrail"
  }
}

resource "aws_s3_bucket_public_access_block" "trail" {
  bucket                  = aws_s3_bucket.trail.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "trail" {
  bucket = aws_s3_bucket.trail.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "trail" {
  bucket = aws_s3_bucket.trail.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_versioning" "trail" {
  bucket = aws_s3_bucket.trail.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "trail" {
  bucket = aws_s3_bucket.trail.id

  rule {
    id     = "expire-${var.cloudtrail_retention_days}d"
    status = "Enabled"
    filter {}
    expiration {
      days = var.cloudtrail_retention_days
    }
    noncurrent_version_expiration {
      noncurrent_days = 7
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  depends_on = [aws_s3_bucket_versioning.trail]
}

locals {
  trail_name = "atpost-${var.environment}-trail"
  trail_arn  = "arn:aws:cloudtrail:${data.aws_region.current.name}:${data.aws_caller_identity.current.account_id}:trail/${local.trail_name}"
}

data "aws_iam_policy_document" "trail_bucket" {
  statement {
    sid     = "AWSCloudTrailAclCheck"
    effect  = "Allow"
    actions = ["s3:GetBucketAcl"]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    resources = [aws_s3_bucket.trail.arn]
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [local.trail_arn]
    }
  }

  statement {
    sid     = "AWSCloudTrailWrite"
    effect  = "Allow"
    actions = ["s3:PutObject"]
    principals {
      type        = "Service"
      identifiers = ["cloudtrail.amazonaws.com"]
    }
    resources = ["${aws_s3_bucket.trail.arn}/AWSLogs/${data.aws_caller_identity.current.account_id}/*"]
    condition {
      test     = "StringEquals"
      variable = "s3:x-amz-acl"
      values   = ["bucket-owner-full-control"]
    }
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [local.trail_arn]
    }
  }

  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
    resources = [
      aws_s3_bucket.trail.arn,
      "${aws_s3_bucket.trail.arn}/*",
    ]
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "trail" {
  bucket = aws_s3_bucket.trail.id
  policy = data.aws_iam_policy_document.trail_bucket.json

  depends_on = [aws_s3_bucket_public_access_block.trail]
}

resource "aws_cloudtrail" "this" {
  name                          = local.trail_name
  s3_bucket_name                = aws_s3_bucket.trail.id
  include_global_service_events = true
  is_multi_region_trail         = true
  is_organization_trail         = false
  enable_log_file_validation    = true
  enable_logging                = true

  event_selector {
    read_write_type           = "All"
    include_management_events = true
  }

  tags = {
    Name = local.trail_name
  }

  depends_on = [aws_s3_bucket_policy.trail]
}

# ─── GuardDuty ──────────────────────────────────────────────────────
resource "aws_guardduty_detector" "this" {
  enable                       = true
  finding_publishing_frequency = "FIFTEEN_MINUTES"

  tags = {
    Name = "atpost-${var.environment}-guardduty"
  }
}

resource "aws_guardduty_detector_feature" "s3" {
  detector_id = aws_guardduty_detector.this.id
  name        = "S3_DATA_EVENTS"
  status      = "ENABLED"
}

resource "aws_guardduty_detector_feature" "eks_audit" {
  detector_id = aws_guardduty_detector.this.id
  name        = "EKS_AUDIT_LOGS"
  status      = "ENABLED"
}

# High-severity findings → ops topic (EventBridge rule).
resource "aws_cloudwatch_event_rule" "guardduty_high" {
  name        = "atpost-${var.environment}-guardduty-high"
  description = "GuardDuty findings with severity >= 7"
  event_pattern = jsonencode({
    source      = ["aws.guardduty"]
    detail-type = ["GuardDuty Finding"]
    detail = {
      severity = [{ numeric = [">=", 7] }]
    }
  })
}

resource "aws_cloudwatch_event_target" "guardduty_high" {
  rule      = aws_cloudwatch_event_rule.guardduty_high.name
  target_id = "ops-alerts"
  arn       = aws_sns_topic.ops_alerts.arn
}

# ─── Budgets ────────────────────────────────────────────────────────
#
# One monthly cost budget whose limit is the highest threshold; each
# threshold is an ACTUAL notification in absolute dollars, plus one
# FORECASTED notification at the limit.
resource "aws_budgets_budget" "monthly" {
  name         = "atpost-${var.environment}-monthly"
  budget_type  = "COST"
  limit_amount = tostring(max(var.budget_thresholds_usd...))
  limit_unit   = "USD"
  time_unit    = "MONTHLY"

  dynamic "notification" {
    for_each = toset(var.budget_thresholds_usd)
    content {
      comparison_operator        = "GREATER_THAN"
      threshold                  = notification.value
      threshold_type             = "ABSOLUTE_VALUE"
      notification_type          = "ACTUAL"
      subscriber_email_addresses = [var.ops_alert_email]
      subscriber_sns_topic_arns  = [aws_sns_topic.ops_alerts.arn]
    }
  }

  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "FORECASTED"
    subscriber_email_addresses = [var.ops_alert_email]
    subscriber_sns_topic_arns  = [aws_sns_topic.ops_alerts.arn]
  }

  depends_on = [aws_sns_topic_policy.ops_alerts]
}
