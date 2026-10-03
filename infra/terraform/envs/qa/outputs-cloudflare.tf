# Everything to add at Cloudflare for QA, in one list:
#   terraform output -json cloudflare_dns_records \
#     | jq -r '.[] | [.purpose,.name,.type,.value] | @tsv'
# All validation, SES and media records are DNS-only (proxy OFF): ACM
# validation records must resolve to AWS exactly, SES compares them byte for
# byte, and the media host must reach CloudFront directly.
#
# The four public host rows (qa., api-qa., ws-qa., admin-qa.) point at ALB
# host names that exist only after ArgoCD has created the Ingresses. Until
# ingress_alb_hostnames is filled (README.md step 9) their value reads
# PENDING; afterwards it is the real target.

locals {
  public_host_records = [
    for s in var.public_subdomains : {
      purpose = "QA public host ${s}.${var.domain} → ALB (after the first ArgoCD sync)"
      name    = "${s}.${var.domain}"
      type    = "CNAME"
      value   = lookup(var.ingress_alb_hostnames, s, "PENDING: ALB host name from `kubectl -n atpost get ingress`")
    }
  ]
}

output "cloudflare_dns_records" {
  description = "DNS records to create at Cloudflare for QA (proxy off)."
  value = concat(
    [
      {
        purpose = "Route 53 delegation for the internal zone ${var.internal_zone_name} (NS × 4; one record per server) — add FIRST"
        name    = var.internal_zone_name
        type    = "NS"
        value   = join(" ", module.dns.name_servers)
      },
    ],
    [
      for r in module.public_edge_cert.validation_records : {
        purpose = "acm-validation (${r.host})"
        name    = r.name
        type    = r.type
        value   = r.value
      }
    ],
    [
      for r in module.media_cdn_cert.validation_records : {
        purpose = "acm-validation us-east-1 (${r.host})"
        name    = r.name
        type    = r.type
        value   = r.value
      }
    ],
    module.ses.dns_records,
    [
      {
        purpose = "media CDN host (add after media_custom_domain_enabled=true is applied)"
        name    = local.media_domain
        type    = "CNAME"
        value   = module.media.cloudfront_domain_name
      },
    ],
    local.public_host_records,
  )
}

output "public_hostnames" {
  description = "Host names on the public certificate (qa., api-qa., ws-qa., admin-qa.). Their CNAME targets are the ALB host names from `kubectl -n atpost get ingress` after the first ArgoCD sync."
  value       = local.public_hostnames
}
