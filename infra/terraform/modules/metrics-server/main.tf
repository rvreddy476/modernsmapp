# metrics-server — the resource-metrics API that HorizontalPodAutoscaler
# and `kubectl top` read. EKS does not install it; without it every HPA in
# the service chart sits at "unknown" and never scales.

resource "helm_release" "metrics_server" {
  name       = "metrics-server"
  repository = "https://kubernetes-sigs.github.io/metrics-server/"
  chart      = "metrics-server"
  version    = var.chart_version
  namespace  = "kube-system"

  set {
    name  = "replicas"
    value = "2"
  }

  set {
    name  = "nodeSelector.workload"
    value = "system"
  }

  set {
    name  = "podDisruptionBudget.enabled"
    value = "true"
  }

  set {
    name  = "podDisruptionBudget.minAvailable"
    value = "1"
  }
}

variable "chart_version" {
  description = "metrics-server Helm chart version."
  type        = string
  default     = "3.12.2"
}

output "release_name" {
  value = helm_release.metrics_server.name
}
