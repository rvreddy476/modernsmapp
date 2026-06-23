output "web_acl_arn" {
  description = "ARN of the regional web ACL — set on the gateway Ingress wafv2-acl-arn annotation."
  value       = aws_wafv2_web_acl.this.arn
}
