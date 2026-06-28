# ArgoCD — GitOps controller for the AKS cluster (Azure twin of the AWS
# argocd module). Same chart + AppProject + ApplicationSet wiring; the
# only cloud-specific differences are the ingress (nginx, not ALB) and
# the admin-password mirror (Key Vault, not Secrets Manager).
#
# The AKS-side ArgoCD reconciles the -azure values via the Azure
# ApplicationSets (deploy/azure-applicationset.yaml). The AWS ArgoCD
# stays the source of truth for EKS — both run the same chart against
# their own cluster.

resource "kubernetes_namespace" "argocd" {
  metadata {
    name = "argocd"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "random_password" "admin" {
  length  = 32
  special = true
}

resource "kubernetes_secret" "argocd_secret" {
  metadata {
    name      = "argocd-secret"
    namespace = kubernetes_namespace.argocd.metadata[0].name
    labels = {
      "app.kubernetes.io/name"    = "argocd-secret"
      "app.kubernetes.io/part-of" = "argocd"
    }
  }

  data = {
    "admin.password"      = bcrypt(random_password.admin.result)
    "admin.passwordMtime" = "2026-01-01T00:00:00Z"
  }

  lifecycle {
    # bcrypt is non-deterministic; don't churn the password each plan.
    ignore_changes = [data]
  }
}

# Break-glass mirror of the admin password in Key Vault.
resource "azurerm_key_vault_secret" "admin" {
  name         = "atpost-${var.environment}-argocd-admin"
  key_vault_id = var.key_vault_id

  value = jsonencode({
    username = "admin"
    password = random_password.admin.result
  })

  tags = {
    environment = var.environment
  }
}

resource "helm_release" "argocd" {
  name       = "argocd"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = var.chart_version
  namespace  = kubernetes_namespace.argocd.metadata[0].name

  set {
    name  = "controller.replicas"
    value = "2"
  }
  set {
    name  = "server.replicas"
    value = "2"
  }
  set {
    name  = "repoServer.replicas"
    value = "2"
  }
  set {
    name  = "applicationSet.replicas"
    value = "2"
  }

  set {
    name  = "global.nodeSelector.workload"
    value = "system"
  }

  # Ingress: nginx (the ingress-nginx module installs the class) fronted
  # by Front Door. TLS is terminated at the edge, so ArgoCD runs HTTP
  # internally.
  set {
    name  = "server.ingress.enabled"
    value = "true"
  }
  set {
    name  = "server.ingress.ingressClassName"
    value = "nginx"
  }
  set {
    name  = "server.ingress.hostname"
    value = var.argocd_hostname
  }
  set {
    name  = "server.service.type"
    value = "ClusterIP"
  }
  set {
    name  = "configs.params.server\\.insecure"
    value = "true"
  }
  # Bring-your-own argocd-secret: we pre-create it (above) with the admin
  # password mirrored to Key Vault, so the chart must NOT try to create/own it
  # (otherwise Helm refuses: "exists and cannot be imported"). argocd-server
  # backfills server.secretkey into it at runtime.
  set {
    name  = "configs.secret.createSecret"
    value = "false"
    type  = "string"
  }

  depends_on = [kubernetes_secret.argocd_secret]
}

resource "kubernetes_manifest" "project_atpost" {
  manifest = {
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "AppProject"
    metadata = {
      name      = "atpost"
      namespace = kubernetes_namespace.argocd.metadata[0].name
    }
    spec = {
      description = "atpost services + platform addons (Azure)"
      sourceRepos = var.allowed_source_repos
      destinations = [
        {
          server    = "https://kubernetes.default.svc"
          namespace = "*"
        }
      ]
      clusterResourceWhitelist = [
        { group = "*", kind = "*" }
      ]
      namespaceResourceWhitelist = [
        { group = "*", kind = "*" }
      ]
    }
  }

  depends_on = [helm_release.argocd]
}

# Apply the Azure ApplicationSet YAML(s). Empty path = skip (manage by
# hand for the first few iterations).
locals {
  applicationsets = var.applicationset_manifest_path != "" ? [
    for doc in split("\n---\n", file(var.applicationset_manifest_path)) :
    yamldecode(doc) if trimspace(doc) != ""
  ] : []
}

resource "kubernetes_manifest" "applicationsets" {
  for_each = { for m in local.applicationsets : "${m.metadata.name}" => m }

  manifest = each.value

  depends_on = [kubernetes_manifest.project_atpost]
}
