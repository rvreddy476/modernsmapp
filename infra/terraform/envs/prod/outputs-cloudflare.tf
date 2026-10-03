# Everything to add at Cloudflare, in one list. `terraform output -json
# cloudflare_dns_records | jq -r '.[] | [.purpose,.name,.type,.value] | @tsv'`
# prints a table. All records are DNS-only (proxy OFF): ACM validation
# records must resolve to AWS exactly, SES compares them byte for byte, and
# the media host must reach CloudFront directly.
#
# Host records for api./ws./app./admin. are NOT here: their targets are ALB
# host names that only exist after the Ingresses are created in the second
# pass (`kubectl -n atpost get ingress`). The README lists that step.

output "cloudflare_dns_records" {
  description = "DNS records to create at Cloudflare (proxy off)."
  value = concat(
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
      {
        purpose = "Route 53 delegation for the internal zone aws.cleestudio.com (NS × 4; one record per server)"
        name    = "aws.${var.domain}"
        type    = "NS"
        value   = join(" ", module.dns.name_servers)
      },
    ],
  )
}

output "public_hostnames" {
  description = "Host names on the public certificate. Their CNAME targets are the ALB host names from `kubectl -n atpost get ingress` after the second pass."
  value       = local.all_hostnames
}
