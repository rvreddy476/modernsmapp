# Public TLS certificates. DNS for cleestudio.com is at Cloudflare, so ACM's
# validation CNAMEs are emitted (outputs-cloudflare.tf) and added there by
# hand; Terraform does not wait for issuance.
#
#   public_edge_cert   ap-south-1  qa., api-qa., ws-qa., admin-qa.cleestudio.com
#                      → ALB listeners (alb.ingress.kubernetes.io/certificate-arn)
#   media_cdn_cert     us-east-1   media-qa.cleestudio.com
#                      → the CloudFront distribution (CloudFront requires us-east-1)
#
# No bare cleestudio.com and no prod host names (app., api., ws., admin.,
# media.) — those belong to the future production account.

module "public_edge_cert" {
  source = "../../modules/public-cert"

  environment               = var.environment
  name                      = "public-edge"
  domain_name               = local.public_hostnames[0]
  subject_alternative_names = slice(local.public_hostnames, 1, length(local.public_hostnames))
}

module "media_cdn_cert" {
  source = "../../modules/public-cert"

  providers = {
    aws = aws.us_east_1
  }

  environment = var.environment
  name        = "media-cdn"
  domain_name = local.media_domain
}

output "public_edge_certificate_arn" {
  value       = module.public_edge_cert.certificate_arn
  description = "ACM (ap-south-1) certificate for the ALBs: alb.ingress.kubernetes.io/certificate-arn in api-gateway, chat-ws-gateway, web and admin values-qa. Usable once ISSUED."
}

output "media_cdn_certificate_arn" {
  value       = module.media_cdn_cert.certificate_arn
  description = "ACM (us-east-1) certificate for media-qa.cleestudio.com. Set media_custom_domain_enabled=true once ISSUED."
}
