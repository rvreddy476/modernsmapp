# AWS Load Balancer Controller.
#
# Translates kubernetes Ingress + Service resources into AWS ALB / NLB.
# Without it, the EKS cluster can't provision external load balancers —
# Ingress objects sit Pending forever. ArgoCD UI, the api-gateway,
# and the chat WebSocket gateway all need this.
#
# Uses the IRSA module's pre-baked policy so we don't have to maintain
# a 500-line IAM policy doc by hand (AWS publishes one; the module
# tracks updates).

# IRSA role with the canonical AWS-LB-Controller policy. The module
# pulls the policy JSON from the AWS docs URL pinned to the chart
# version, so the policy stays in sync with the controller.
module "irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.48"

  role_name                              = "atpost-${var.environment}-aws-lb-controller"
  attach_load_balancer_controller_policy = true

  oidc_providers = {
    main = {
      provider_arn               = var.oidc_provider_arn
      namespace_service_accounts = ["kube-system:aws-load-balancer-controller"]
    }
  }
}

resource "helm_release" "aws_lb_controller" {
  name       = "aws-load-balancer-controller"
  repository = "https://aws.github.io/eks-charts"
  chart      = "aws-load-balancer-controller"
  version    = var.chart_version
  namespace  = "kube-system"

  set {
    name  = "clusterName"
    value = var.cluster_name
  }

  set {
    name  = "serviceAccount.create"
    value = "true"
  }

  set {
    name  = "serviceAccount.name"
    value = "aws-load-balancer-controller"
  }

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = module.irsa.iam_role_arn
  }

  # Region is required for SDK calls (parameter lookups, etc.).
  set {
    name  = "region"
    value = var.aws_region
  }

  set {
    name  = "vpcId"
    value = var.vpc_id
  }

  # Two replicas for controller HA — survives a node restart without
  # gapping Ingress reconciliation.
  set {
    name  = "replicaCount"
    value = "2"
  }

  # Schedule on system nodes — platform tooling, not app load.
  set {
    name  = "nodeSelector.workload"
    value = "system"
  }

  # Pod disruption budget so a noisy upgrade doesn't take both
  # replicas at once.
  set {
    name  = "podDisruptionBudget.maxUnavailable"
    value = "1"
  }
}
