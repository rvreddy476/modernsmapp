# Alert routing (feed-10): Alertmanager → the ops SNS topic → the founder's
# inbox.
#
# Before this file Alertmanager had no receiver and no route: a firing alert
# was visible only to someone who opened the Alertmanager UI.
#
# What it does when var.alert_routing_enabled is true:
#   - an IAM role the Alertmanager pod assumes through its ServiceAccount
#     (IRSA), allowed to sns:Publish on ONE topic (var.alert_sns_topic_arn,
#     the ops-baseline module's atpost-<env>-ops-alerts, which already
#     e-mails var.ops_alert_email);
#   - a receiver `ops-email` using Alertmanager's own SNS integration
#     (sns_configs, SigV4 with the pod's role; no webhook, no extra pod);
#   - routes: every alert the atpost-service chart renders carries
#     source=atpost-service (charts/atpost-service/templates/
#     prometheusrule.yaml) and goes to ops-email. severity critical or page
#     repeats every 2 h while firing, everything else every 24 h. Resolved
#     notices are sent.
#   - everything else (kube-prometheus-stack's own rules) stays on the `null`
#     receiver, visible in the UI only, unless var.route_platform_critical is
#     true. It is false by default because the stack's control-plane rules
#     (scheduler, controller-manager, etcd) fire permanently on EKS, where
#     those components cannot be scraped; turn them off in the stack before
#     turning this on.
#
# The topic policy (ops-baseline, statement AccountPublish) already lets a
# principal of this account publish; the role policy below is the grant.
# The e-mail subscription must be CONFIRMED (the link SNS sent to the ops
# address) or nothing is delivered: docs/runbooks/service-alerts.md.

locals {
  alertmanager_service_account = "alertmanager"

  alert_subject = "[{{ .Status | toUpper }}{{ if eq .Status \"firing\" }}:{{ .Alerts.Firing | len }}{{ end }}] {{ .CommonLabels.alertname }} {{ .CommonLabels.service }} (atpost-${var.environment})"

  alert_message = <<-EOT
    {{ range .Alerts }}{{ .Status | toUpper }}: {{ .Labels.alertname }}
    severity: {{ .Labels.severity }}   service: {{ .Labels.service }}   environment: atpost-${var.environment}
    {{ .Annotations.summary }}
    {{ .Annotations.description }}
    started: {{ .StartsAt }}
    {{ range .Labels.SortedPairs }}{{ .Name }}={{ .Value }} {{ end }}

    {{ end }}
  EOT

  alert_routes = concat(
    [
      {
        receiver        = "ops-email"
        matchers        = ["source = \"atpost-service\"", "severity =~ \"critical|page\""]
        repeat_interval = "2h"
      },
      {
        receiver        = "ops-email"
        matchers        = ["source = \"atpost-service\""]
        repeat_interval = "24h"
      },
    ],
    var.route_platform_critical ? [
      {
        receiver        = "ops-email"
        matchers        = ["severity = \"critical\"", "alertname != \"Watchdog\""]
        repeat_interval = "4h"
      },
    ] : [],
  )

  alert_routing_values = {
    alertmanager = {
      serviceAccount = {
        create = true
        name   = local.alertmanager_service_account
        annotations = {
          "eks.amazonaws.com/role-arn" = one(aws_iam_role.alertmanager[*].arn)
        }
      }
      config = {
        global = {
          resolve_timeout = "5m"
        }
        route = {
          receiver        = "null"
          group_by        = ["alertname", "service"]
          group_wait      = "30s"
          group_interval  = "5m"
          repeat_interval = "12h"
          routes          = local.alert_routes
        }
        receivers = [
          { name = "null" },
          {
            name = "ops-email"
            sns_configs = [{
              topic_arn     = var.alert_sns_topic_arn
              send_resolved = true
              sigv4 = {
                region = var.aws_region
              }
              subject = local.alert_subject
              message = local.alert_message
            }]
          },
        ]
      }
    }
  }
}

data "aws_iam_policy_document" "alertmanager_trust" {
  count = var.alert_routing_enabled ? 1 : 0

  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    effect  = "Allow"

    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:sub"
      values   = ["system:serviceaccount:${kubernetes_namespace.observability.metadata[0].name}:${local.alertmanager_service_account}"]
    }
  }
}

data "aws_iam_policy_document" "alertmanager_publish" {
  count = var.alert_routing_enabled ? 1 : 0

  statement {
    sid       = "PublishOpsAlerts"
    effect    = "Allow"
    actions   = ["sns:Publish"]
    resources = [var.alert_sns_topic_arn]
  }
}

resource "aws_iam_role" "alertmanager" {
  count = var.alert_routing_enabled ? 1 : 0

  name               = "atpost-${var.environment}-alertmanager-irsa"
  assume_role_policy = data.aws_iam_policy_document.alertmanager_trust[0].json

  tags = {
    Name = "atpost-${var.environment}-alertmanager-irsa"
  }
}

resource "aws_iam_role_policy" "alertmanager_publish" {
  count = var.alert_routing_enabled ? 1 : 0

  name   = "publish-ops-alerts"
  role   = aws_iam_role.alertmanager[0].id
  policy = data.aws_iam_policy_document.alertmanager_publish[0].json
}
