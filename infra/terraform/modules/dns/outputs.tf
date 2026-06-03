output "zone_id" {
  value = aws_route53_zone.aws_subdomain.zone_id
}

output "name_servers" {
  description = "Set these as the NS records for `var.zone_name` in the parent Cloudflare zone. Without this delegation step, the zone won't resolve from the public internet."
  value       = aws_route53_zone.aws_subdomain.name_servers
}

output "wildcard_cert_arn" {
  value       = aws_acm_certificate.wildcard.arn
  description = "Pass to ALB / CloudFront listeners for TLS termination."
}
