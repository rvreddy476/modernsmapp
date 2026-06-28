# Prometheus Operator CRDs — provides the ServiceMonitor / PodMonitor kinds the
# atpost-service chart emits (serviceMonitor.enabled defaults true). Without
# these CRDs every service fails to deploy ("no matches for kind ServiceMonitor").
#
# This installs ONLY the CRDs (no operator pods), so the ServiceMonitor objects
# apply cleanly now; a full Prometheus/Grafana stack can be added to this module
# later to actually scrape them. AWS parity: the AWS tree has a full observability
# stack; this is the minimal Azure equivalent to keep deploys unblocked.

resource "kubernetes_namespace" "monitoring" {
  metadata {
    name   = "monitoring"
    labels = { "app.kubernetes.io/managed-by" = "terraform" }
  }
}

resource "helm_release" "prometheus_operator_crds" {
  name       = "prometheus-operator-crds"
  repository = "https://prometheus-community.github.io/helm-charts"
  chart      = "prometheus-operator-crds"
  namespace  = kubernetes_namespace.monitoring.metadata[0].name
  # version intentionally unpinned: CRDs-only chart, latest is low-risk and
  # avoids a brittle version guess. Pin via var.crds_chart_version if needed.
  version = var.crds_chart_version != "" ? var.crds_chart_version : null
}
