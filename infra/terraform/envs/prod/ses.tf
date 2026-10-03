# Amazon SES: cleestudio.com identity with DKIM, the transactional
# configuration set identity-auth names, bounces/complaints to the ops
# topic (→ founder's email). Production access (leaving the sandbox) is a
# console request — README.md.

module "ses" {
  source = "../../modules/ses"

  environment            = "prod"
  domain                 = var.domain
  from_address           = var.ses_from_address
  configuration_set_name = var.ses_configuration_set_name
  event_sns_topic_arn    = module.ops.ops_alerts_topic_arn
  dmarc_report_address   = coalesce(var.dmarc_report_address, var.ops_alert_email)
}

output "ses_identity_arn" { value = module.ses.identity_arn }
output "ses_configuration_set_name" { value = module.ses.configuration_set_name }
output "ses_dkim_tokens" { value = module.ses.dkim_tokens }
