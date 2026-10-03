# Amazon SES for transactional email (identity-auth-service: verification
# codes, password resets; later notification digests).
#
# What this creates:
#   - a domain identity for `var.domain` with Easy DKIM (2048-bit) — the
#     three DKIM CNAMEs are emitted as outputs for Cloudflare;
#   - a custom MAIL FROM subdomain (`mail.<domain>`) so SPF aligns with the
#     From domain and DMARC passes — MX + TXT records are outputs too;
#   - the configuration set the services name in SES_CONFIGURATION_SET,
#     with TLS required and reputation metrics on;
#   - an event destination that publishes bounces, complaints and rejects to
#     the SNS topic passed in (which emails the founder);
#   - an IAM policy that lets a role send FROM `var.from_address` only, via
#     this identity and this configuration set — attach to identity-auth.
#
# What this CANNOT do: leave the SES sandbox. Production access is a
# support request in the console (SES → Account dashboard → Request
# production access). Until it is granted, mail goes only to verified
# addresses and the daily quota is 200.

data "aws_caller_identity" "current" {}
data "aws_region" "current" {}

resource "aws_sesv2_configuration_set" "transactional" {
  configuration_set_name = var.configuration_set_name

  delivery_options {
    tls_policy = "REQUIRE"
  }

  reputation_options {
    reputation_metrics_enabled = true
  }

  sending_options {
    sending_enabled = true
  }

  suppression_options {
    suppressed_reasons = ["BOUNCE", "COMPLAINT"]
  }

  tags = {
    Name = var.configuration_set_name
  }
}

resource "aws_sesv2_email_identity" "domain" {
  email_identity         = var.domain
  configuration_set_name = aws_sesv2_configuration_set.transactional.configuration_set_name

  dkim_signing_attributes {
    next_signing_key_length = "RSA_2048_BIT"
  }

  tags = {
    Name = "atpost-${var.environment}-ses-${var.domain}"
  }
}

resource "aws_sesv2_email_identity_mail_from_attributes" "domain" {
  email_identity   = aws_sesv2_email_identity.domain.email_identity
  mail_from_domain = "${var.mail_from_subdomain}.${var.domain}"
  # If the MX record is missing, fall back to amazonses.com rather than
  # refusing to send — deliverability degrades, mail still goes.
  behavior_on_mx_failure = "USE_DEFAULT_VALUE"
}

# Bounces and complaints → SNS → the founder's inbox. Reject = SES refused
# to send (e.g. virus, suppressed address); worth seeing too.
resource "aws_sesv2_configuration_set_event_destination" "sns" {
  configuration_set_name = aws_sesv2_configuration_set.transactional.configuration_set_name
  event_destination_name = "ops-alerts-sns"

  event_destination {
    enabled              = true
    matching_event_types = ["BOUNCE", "COMPLAINT", "REJECT"]

    sns_destination {
      topic_arn = var.event_sns_topic_arn
    }
  }
}

# Send policy for the service role. Resource-scoped to the identity and the
# configuration set; the From address is pinned so a compromised pod cannot
# impersonate another sender on the domain.
data "aws_iam_policy_document" "send" {
  statement {
    sid    = "SendTransactional"
    effect = "Allow"
    actions = [
      "ses:SendEmail",
      "ses:SendRawEmail",
    ]
    resources = [
      aws_sesv2_email_identity.domain.arn,
      aws_sesv2_configuration_set.transactional.arn,
    ]
    condition {
      test     = "StringEquals"
      variable = "ses:FromAddress"
      values   = [var.from_address]
    }
  }
}

resource "aws_iam_policy" "send" {
  name        = "atpost-${var.environment}-ses-send"
  description = "Send email from ${var.from_address} through SES identity ${var.domain} / configuration set ${var.configuration_set_name}."
  policy      = data.aws_iam_policy_document.send.json
}
