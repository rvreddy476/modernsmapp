# Regional WAFv2 web ACL for the api-gateway ALB (the internet-facing edge).
#
# Attached to the ALB via the Ingress annotation
#   alb.ingress.kubernetes.io/wafv2-acl-arn: <this module's web_acl_arn>
# in deploy/services/api-gateway/values-${env}.yaml.
#
# Scope = REGIONAL because the ALB is a regional resource (CLOUDFRONT scope is
# only for CloudFront distributions; the media CDN can get its own global ACL
# later if needed).
#
# Rules (evaluated by priority, lowest first):
#   1. AWS Common rule set        — OWASP-ish baseline (XSS, LFI, etc.)
#   2. AWS Known Bad Inputs       — exploit signatures
#   3. AWS IP reputation list     — known malicious / bot source IPs
#   4. Rate-based per-IP limiter  — blunt DoS / credential-stuffing brake
#
# Managed groups run in their own (block) action; we don't "count" them — this
# is the edge, we want them enforcing from day one. Tune to count-mode first if
# false positives bite a launch.

resource "aws_wafv2_web_acl" "this" {
  name        = "atpost-${var.environment}-edge"
  description = "Edge WAF for the api-gateway ALB (${var.environment})"
  scope       = "REGIONAL"

  default_action {
    allow {}
  }

  rule {
    name     = "aws-common"
    priority = 1
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesCommonRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "atpost-${var.environment}-common"
      sampled_requests_enabled   = true
    }
  }

  rule {
    name     = "aws-known-bad-inputs"
    priority = 2
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesKnownBadInputsRuleSet"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "atpost-${var.environment}-known-bad-inputs"
      sampled_requests_enabled   = true
    }
  }

  rule {
    name     = "aws-ip-reputation"
    priority = 3
    override_action {
      none {}
    }
    statement {
      managed_rule_group_statement {
        name        = "AWSManagedRulesAmazonIpReputationList"
        vendor_name = "AWS"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "atpost-${var.environment}-ip-reputation"
      sampled_requests_enabled   = true
    }
  }

  rule {
    name     = "rate-limit-per-ip"
    priority = 4
    action {
      block {}
    }
    statement {
      rate_based_statement {
        limit              = var.rate_limit_per_5min
        aggregate_key_type = "IP"
      }
    }
    visibility_config {
      cloudwatch_metrics_enabled = true
      metric_name                = "atpost-${var.environment}-rate-limit"
      sampled_requests_enabled   = true
    }
  }

  visibility_config {
    cloudwatch_metrics_enabled = true
    metric_name                = "atpost-${var.environment}-edge"
    sampled_requests_enabled   = true
  }

  tags = {
    Name = "atpost-${var.environment}-edge"
  }
}
