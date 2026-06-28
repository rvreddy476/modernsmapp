# ingress-nginx — the in-cluster ingress controller (Azure edge =
# Front Door → this nginx → Services). On AWS the equivalent is the
# aws-load-balancer-controller provisioning ALBs from Ingress objects;
# on Azure we run nginx and front it with Azure Front Door + WAF.
#
# The controller's Service is type LoadBalancer → AKS provisions an
# Azure Standard Load Balancer with a public IP. Front Door's origin
# points at that IP (or at a private LB if you go private-link later).

resource "kubernetes_namespace" "ingress_nginx" {
  metadata {
    name = "ingress-nginx"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "helm_release" "ingress_nginx" {
  name       = "ingress-nginx"
  repository = "https://kubernetes.github.io/ingress-nginx"
  chart      = "ingress-nginx"
  version    = var.chart_version
  namespace  = kubernetes_namespace.ingress_nginx.metadata[0].name

  # IngressClass name services reference (ingress.className: nginx).
  set {
    name  = "controller.ingressClassResource.name"
    value = "nginx"
  }
  set {
    name  = "controller.ingressClassResource.default"
    value = "true"
  }

  # Health-probe path Azure LB expects on the controller Service.
  set {
    name  = "controller.service.annotations.service\\.beta\\.kubernetes\\.io/azure-load-balancer-health-probe-request-path"
    value = "/healthz"
  }

  # HA controller; platform tooling on the system pool.
  set {
    name  = "controller.replicaCount"
    value = var.replica_count
  }
  set {
    name  = "controller.nodeSelector.workload"
    value = "system"
  }

  # Trust Front Door's forwarded headers so client IPs / proto survive
  # the edge hop (real_ip from the X-Forwarded-For chain).
  set {
    name  = "controller.config.use-forwarded-headers"
    value = "true"
  }
}
