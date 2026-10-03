output "web_acl_arn" {
  description = "ARN of the regional web ACL — set on the gateway Ingress wafv2-acl-arn annotation."
  value       = aws_wafv2_web_acl.this.arn
}

output "web_acl_name" {
  value = aws_wafv2_web_acl.this.name
}

output "web_acl_id" {
  value = aws_wafv2_web_acl.this.id
}
