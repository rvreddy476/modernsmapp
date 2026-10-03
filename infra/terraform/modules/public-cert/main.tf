# Public ACM certificate validated through DNS records that live at
# Cloudflare (cleestudio.com is NOT a Route 53 zone — only aws.cleestudio.com
# is). Terraform cannot create the validation CNAMEs, so this module emits
# them as an output and does NOT wait for issuance: the lead adds the
# records at Cloudflare, ACM issues the certificate within minutes, and the
# resources that need an ISSUED certificate (CloudFront aliases, ALB
# listeners) are applied afterwards.
#
# Instantiate with `providers = { aws = aws.us_east_1 }` for a certificate
# CloudFront will use — CloudFront only accepts certificates from us-east-1.

resource "aws_acm_certificate" "this" {
  domain_name               = var.domain_name
  subject_alternative_names = var.subject_alternative_names
  validation_method         = "DNS"

  options {
    certificate_transparency_logging_preference = "ENABLED"
  }

  tags = {
    Name = "atpost-${var.environment}-cert-${var.name}"
  }

  lifecycle {
    create_before_destroy = true
  }
}
