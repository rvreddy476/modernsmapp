variable "environment" {
  type = string
}

variable "domain" {
  description = "Sending domain to verify (cleestudio.com)."
  type        = string
}

variable "from_address" {
  description = "The only From address the service role may use."
  type        = string
}

variable "configuration_set_name" {
  description = "Configuration set name the services pass as SES_CONFIGURATION_SET."
  type        = string
}

variable "mail_from_subdomain" {
  description = "Custom MAIL FROM subdomain (SPF alignment). `mail` → mail.cleestudio.com."
  type        = string
  default     = "mail"
}

variable "event_sns_topic_arn" {
  description = "SNS topic that receives bounce/complaint/reject events. Its policy must allow ses.amazonaws.com to publish."
  type        = string
}

variable "dmarc_report_address" {
  description = "Mailbox for DMARC aggregate reports (rua=)."
  type        = string
}
