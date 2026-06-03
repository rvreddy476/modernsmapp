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
