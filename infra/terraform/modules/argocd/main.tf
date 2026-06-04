# ArgoCD — GitOps deploy controller per Phase-2 decision §4.
#
# Reconciles every service's desired state (declared in a git repo)
# against what's actually running in the cluster. Without ArgoCD,
# deploys would have to push from CI (kubectl apply) — the GitOps
# inversion is what makes drift detection + per-service rollback
# work without bespoke scripting.
#
# This module installs the controller + UI + an initial Application
# pointing at the atpost-helm-charts repo (a sibling repo containing
# the Helm umbrella chart). Per-service Applications get created
# either via ApplicationSet (preferred) or one-off Applications as
# the per-service charts land.

resource "kubernetes_namespace" "argocd" {
  metadata {
    name = "argocd"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

# Initial admin password — set explicitly so the password doesn't get
# auto-generated to a value we then have to fish out of a Secret. The
# kubernetes Secret below is created BEFORE the Helm release so the
# chart picks it up at install time.
resource "random_password" "admin" {
  length  = 32
  special = true
}

# bcrypt hash of the password — ArgoCD stores admin.password as bcrypt.
# Computed locally so it's deterministic across applies.
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
    # Don't reset the password on every plan — bcrypt is non-deterministic
    # so the hash differs each time even with the same input. We want
    # the password to stay stable.
    ignore_changes = [data]
  }
}

# Mirror the admin password in Secrets Manager so the rotation /
# break-glass story has a documented source of truth.
resource "aws_secretsmanager_secret" "admin" {
  name                    = "atpost/${var.environment}/argocd/admin"
  description             = "ArgoCD admin password (break-glass). UI default user is `admin`."
  recovery_window_in_days = 7

  tags = {
    Name = "atpost-${var.environment}-argocd-admin"
  }
}

resource "aws_secretsmanager_secret_version" "admin" {
  secret_id = aws_secretsmanager_secret.admin.id
  secret_string = jsonencode({
    username = "admin"
    password = random_password.admin.result
  })
}

resource "helm_release" "argocd" {
  name       = "argocd"
  repository = "https://argoproj.github.io/argo-helm"
  chart      = "argo-cd"
  version    = var.chart_version
  namespace  = kubernetes_namespace.argocd.metadata[0].name

  # HA mode: 2 replicas of every component (server, repo-server,
  # application-controller). Worth the cost — a single-replica
  # ArgoCD that goes down stops reconciling, which is a quiet
  # failure mode you don't notice until the next deploy.
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

  # Schedule on the system node group.
  set {
    name  = "global.nodeSelector.workload"
    value = "system"
  }

  # Ingress: ALB via the aws-load-balancer-controller (already installed
  # by the cluster-tooling layer above). Internet-facing in staging,
  # internal in prod (prod-UI users go through a VPN / bastion).
  set {
    name  = "server.ingress.enabled"
    value = "true"
  }
  set {
    name  = "server.ingress.ingressClassName"
    value = "alb"
  }
  set {
    name  = "server.ingress.annotations.alb\\.ingress\\.kubernetes\\.io/scheme"
    value = var.ingress_scheme
  }
  set {
    name  = "server.ingress.annotations.alb\\.ingress\\.kubernetes\\.io/target-type"
    value = "ip"
  }
  set {
    name  = "server.ingress.annotations.alb\\.ingress\\.kubernetes\\.io/listen-ports"
    value = "[{\"HTTPS\":443}]"
  }
  set {
    name  = "server.ingress.annotations.alb\\.ingress\\.kubernetes\\.io/certificate-arn"
    value = var.acm_certificate_arn
  }
  set {
    name  = "server.ingress.hostname"
    value = var.argocd_hostname
  }

  # Service.type=ClusterIP — the ALB targets the Service directly
  # (no NodePort indirection).
  set {
    name  = "server.service.type"
    value = "ClusterIP"
  }

  # Tell the server it's behind a load balancer that terminates TLS.
  set {
    name  = "configs.params.server\\.insecure"
    value = "true" # TLS terminated at ALB; ArgoCD speaks HTTP inside
  }

  depends_on = [kubernetes_secret.argocd_secret]
}

# Project: "atpost". All app Applications live in this Project; cluster
# admins still see them all, but per-Project RBAC can later scope team
# access (e.g. mobile team can sync mobile-bff but not payments).
resource "kubernetes_manifest" "project_atpost" {
  manifest = {
    apiVersion = "argoproj.io/v1alpha1"
    kind       = "AppProject"
    metadata = {
      name      = "atpost"
      namespace = kubernetes_namespace.argocd.metadata[0].name
    }
    spec = {
      description = "atpost services + platform addons"

      sourceRepos = var.allowed_source_repos

      destinations = [
        {
          server    = "https://kubernetes.default.svc"
          namespace = "*"
        }
      ]

      clusterResourceWhitelist = [
        {
          group = "*"
          kind  = "*"
        }
      ]

      namespaceResourceWhitelist = [
        {
          group = "*"
          kind  = "*"
        }
      ]
    }
  }

  depends_on = [helm_release.argocd]
}

# Apply the multi-document ApplicationSet YAML (deploy/argocd/
# applicationset.yaml). Empty path = skip; useful in staging if you
# want the ApplicationSet to be managed by hand for the first few
# iterations.
locals {
  applicationsets = var.applicationset_manifest_path != "" ? [
    for doc in split("\n---\n", file(var.applicationset_manifest_path)) :
    yamldecode(doc) if trimspace(doc) != ""
  ] : []
}

resource "kubernetes_manifest" "applicationsets" {
  for_each = { for i, m in local.applicationsets : "${m.metadata.name}" => m }

  manifest = each.value

  depends_on = [kubernetes_manifest.project_atpost]
}
