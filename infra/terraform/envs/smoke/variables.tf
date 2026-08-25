variable "region" {
  description = "Decision 002 fixes this at ap-south-1 (Mumbai)."
  type        = string
  default     = "ap-south-1"
}

variable "cors_allowed_origins" {
  description = <<EOT
Origins permitted to drive a browser upload. For a device smoke the app is
native and sends no Origin header, so this can stay minimal — but it must not
be "*", and it must include any web origin you intend to test from.
EOT
  type        = list(string)
  default     = ["https://staging.cleestudio.com"]
}
