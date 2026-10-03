variable "environment" {
  type = string
}

variable "name" {
  description = "Short name for tags: public-edge, media-cdn."
  type        = string
}

variable "domain_name" {
  description = "Primary domain on the certificate."
  type        = string
}

variable "subject_alternative_names" {
  description = "Additional host names."
  type        = list(string)
  default     = []
}
