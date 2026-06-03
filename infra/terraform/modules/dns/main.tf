# Route 53 hosted zone + ACM cert.
#
# `cleestudio.com` is currently a Cloudflare-managed zone. Migration
# plan from the AWS roadmap §5.6: Cloudflare stays as the public DNS
# edge + WAF + DDoS layer, pointing at a CloudFront distribution that
# fronts the AWS-side ALB. Route 53 owns the AWS-internal hostnames:
# *.aws.cleestudio.com, alb-<env>.aws.cleestudio.com, etc.
#
# A wildcard ACM cert at *.aws.cleestudio.com + apex aws.cleestudio.com
# covers every service hostname we'd want to attach to an ALB or
# CloudFront distribution.

resource "aws_route53_zone" "aws_subdomain" {
  name = var.zone_name

  tags = {
    Name = "atpost-${var.environment}-zone"
  }
}

resource "aws_acm_certificate" "wildcard" {
  domain_name               = var.zone_name
  subject_alternative_names = ["*.${var.zone_name}"]
  validation_method         = "DNS"

  tags = {
    Name = "atpost-${var.environment}-cert-wildcard"
  }

  lifecycle {
    # ACM rotation issues a new cert before tearing down the old one
    # so connections aren't interrupted.
    create_before_destroy = true
  }
}

# DNS-validated cert: ACM emits one CNAME per SAN; we Reflect them
# back into the zone. Two records here because two SANs above
# (apex + wildcard).
resource "aws_route53_record" "cert_validation" {
  for_each = {
    for d in aws_acm_certificate.wildcard.domain_validation_options : d.domain_name => {
      name   = d.resource_record_name
      record = d.resource_record_value
      type   = d.resource_record_type
    }
  }

  zone_id = aws_route53_zone.aws_subdomain.zone_id
  name    = each.value.name
  type    = each.value.type
  records = [each.value.record]
  ttl     = 60
}

resource "aws_acm_certificate_validation" "wildcard" {
  certificate_arn         = aws_acm_certificate.wildcard.arn
  validation_record_fqdns = [for r in aws_route53_record.cert_validation : r.fqdn]
}
