variable "environment" {
  type = string
}

variable "cors_allowed_origins" {
  description = <<EOT
Origins allowed for CORS on the media bucket + CloudFront response.
Example: ["https://app.cleestudio.com", "https://staging.cleestudio.com"].
Do NOT use "*" — it weakens the upload-presigned-URL story.
EOT
  type        = list(string)
}

variable "cloudfront_price_class" {
  description = <<EOT
CloudFront edge tier. PriceClass_100 = US + EU + Israel + South Africa
(cheapest). PriceClass_200 adds Asia / South America / Middle East.
PriceClass_All adds Australia + NZ. ap-south-1 users are served best
by PriceClass_200 since India + Asia are included.
EOT
  type        = string
  default     = "PriceClass_200"
}

variable "custom_domain" {
  description = "Custom host name for the distribution (media.cleestudio.com). Null → *.cloudfront.net only. Requires acm_certificate_arn_us_east_1."
  type        = string
  default     = null
}

variable "acm_certificate_arn_us_east_1" {
  description = "ISSUED ACM certificate in us-east-1 covering custom_domain. CloudFront accepts certificates from us-east-1 only. Null until the Cloudflare validation record exists."
  type        = string
  default     = null
}

variable "manage_signing_key" {
  description = "Generate the CloudFront signing key pair in Terraform (private key lands in state, like auth-keys). false → pass signing_public_key_pem and keep the private key out of state."
  type        = bool
  default     = true
}

variable "signing_public_key_pem" {
  description = "PEM public key for the CloudFront key group when manage_signing_key=false."
  type        = string
  default     = null
}
