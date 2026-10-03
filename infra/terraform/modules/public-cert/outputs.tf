output "certificate_arn" {
  value       = aws_acm_certificate.this.arn
  description = "Usable once ACM reports ISSUED (after the validation records exist at Cloudflare)."
}

output "validation_records" {
  description = "DNS records to add at Cloudflare (proxy OFF / DNS-only). One CNAME per host name."
  value = [
    for d in aws_acm_certificate.this.domain_validation_options : {
      host  = d.domain_name
      name  = trimsuffix(d.resource_record_name, ".")
      type  = d.resource_record_type
      value = trimsuffix(d.resource_record_value, ".")
    }
  ]
}

output "domain_names" {
  value = concat([var.domain_name], var.subject_alternative_names)
}
