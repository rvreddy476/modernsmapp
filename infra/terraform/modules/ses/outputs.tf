output "identity_arn" {
  value = aws_sesv2_email_identity.domain.arn
}

output "configuration_set_name" {
  value = aws_sesv2_configuration_set.transactional.configuration_set_name
}

output "configuration_set_arn" {
  value = aws_sesv2_configuration_set.transactional.arn
}

output "send_policy_arn" {
  value       = aws_iam_policy.send.arn
  description = "Attach to identity-auth-service's IRSA role."
}

output "dkim_tokens" {
  value = aws_sesv2_email_identity.domain.dkim_signing_attributes[0].tokens
}

# Everything Cloudflare needs for this domain to send with DKIM + SPF +
# DMARC. Proxy OFF (DNS-only) on all of them.
output "dns_records" {
  description = "DNS records to add at Cloudflare for SES."
  value = concat(
    [
      for t in aws_sesv2_email_identity.domain.dkim_signing_attributes[0].tokens : {
        purpose = "ses-dkim"
        name    = "${t}._domainkey.${var.domain}"
        type    = "CNAME"
        value   = "${t}.dkim.amazonses.com"
      }
    ],
    [
      {
        purpose = "ses-mail-from-mx"
        name    = "${var.mail_from_subdomain}.${var.domain}"
        type    = "MX"
        value   = "10 feedback-smtp.${data.aws_region.current.name}.amazonses.com"
      },
      {
        purpose = "ses-mail-from-spf"
        name    = "${var.mail_from_subdomain}.${var.domain}"
        type    = "TXT"
        value   = "v=spf1 include:amazonses.com -all"
      },
      {
        purpose = "dmarc (add once; p=none while watching reports, then quarantine)"
        name    = "_dmarc.${var.domain}"
        type    = "TXT"
        value   = "v=DMARC1; p=none; rua=mailto:${var.dmarc_report_address}"
      },
    ]
  )
}
