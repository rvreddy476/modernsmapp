variable "environment" {
  type = string
}

variable "zone_name" {
  description = <<EOT
DNS zone owned by Route 53 in this env. AtPost convention:
  staging -> staging.aws.cleestudio.com
  prod    -> aws.cleestudio.com
The parent `cleestudio.com` stays in Cloudflare; you NS-delegate this
subdomain to Route 53's name servers (see this module's outputs).
EOT
  type        = string
}
