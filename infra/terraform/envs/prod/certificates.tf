# Public TLS certificates. DNS for cleestudio.com is at Cloudflare, so ACM's
# validation CNAMEs are emitted (outputs.tf → cloudflare_dns_records) and
# added there by hand; Terraform does not wait for issuance.
#
#   public_edge_cert   ap-south-1  cleestudio.com, api., ws., app., admin.
#                      → ALB listeners (alb.ingress.kubernetes.io/certificate-arn)
#   media_cdn_cert     us-east-1   media.cleestudio.com
#                      → the CloudFront distribution (CloudFront requires us-east-1)

module "public_edge_cert" {
  source = "../../modules/public-cert"

  environment               = "prod"
  name                      = "public-edge"
  domain_name               = var.domain
  subject_alternative_names = [for s in var.public_subdomains : "${s}.${var.domain}"]
}

module "media_cdn_cert" {
  source = "../../modules/public-cert"

  providers = {
    aws = aws.us_east_1
  }

  environment = "prod"
  name        = "media-cdn"
  domain_name = local.media_domain
}

output "public_edge_certificate_arn" {
  value       = module.public_edge_cert.certificate_arn
  description = "ACM (ap-south-1) certificate for the ALBs: set alb.ingress.kubernetes.io/certificate-arn in api-gateway, chat-ws-gateway and the web values. Usable once ISSUED."
}

output "media_cdn_certificate_arn" {
  value       = module.media_cdn_cert.certificate_arn
  description = "ACM (us-east-1) certificate for media.cleestudio.com. Set media_custom_domain_enabled=true once ISSUED."
}
