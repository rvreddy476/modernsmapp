variable "environment" {
  type = string
}

variable "rate_limit_per_5min" {
  description = <<EOT
Per-IP request ceiling over a rolling 5-minute window before WAF blocks the IP.
2000 is a generous default for a logged-in social app behind a CDN; lower it if
the gateway sees credential-stuffing. AWS minimum is 100.
EOT
  type        = number
  default     = 2000
}
