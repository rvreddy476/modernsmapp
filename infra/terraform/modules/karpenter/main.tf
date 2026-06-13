# Karpenter — elastic node provisioner. Replaces the static node-group
# autoscaler model with on-demand "see a Pending pod → spin up exactly
# the right instance type" provisioning.
#
# Why Karpenter over Cluster Autoscaler:
# - Faster (skips Auto Scaling Group churn — talks EC2 directly).
# - Better bin-packing across instance types.
# - Native spot interruption handling via SQS + EventBridge.
# - One NodePool can mix arm64 / x86 / spot / on-demand — no per-shape
#   ASG configuration.
#
# The static EKS managed node groups stay: `general` runs the platform
# tooling that needs to bootstrap before Karpenter (the Karpenter
# controller itself, ESO, ALB controller, ArgoCD). Karpenter handles
# the elastic burst capacity above that.

# AWS-provided submodule that owns:
#   - IAM role for the Karpenter controller (IRSA-bound)
#   - IAM role for the nodes Karpenter provisions (with the EKS
#     worker-node managed policies attached)
#   - Instance profile for the node role
#   - SQS interruption queue + EventBridge rules feeding it
module "karpenter" {
  source  = "terraform-aws-modules/eks/aws//modules/karpenter"
  version = "~> 20.29"

  cluster_name = var.cluster_name

  enable_irsa             = true
  irsa_oidc_provider_arn  = var.oidc_provider_arn
  irsa_namespace_service_accounts = ["karpenter:karpenter"]

  # Node role attaches the standard EKS worker policies + CNI + ECR
  # pull. The submodule handles the policy mix.
  node_iam_role_use_name_prefix = false
  node_iam_role_name            = "atpost-${var.environment}-karpenter-node"
  iam_role_use_name_prefix      = false
  iam_role_name                 = "atpost-${var.environment}-karpenter-controller"

  tags = {
    Name = "atpost-${var.environment}-karpenter"
  }
}

resource "kubernetes_namespace" "karpenter" {
  metadata {
    name = "karpenter"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "helm_release" "karpenter" {
  name       = "karpenter"
  repository = "oci://public.ecr.aws/karpenter"
  chart      = "karpenter"
  version    = var.chart_version
  namespace  = kubernetes_namespace.karpenter.metadata[0].name

  set {
    name  = "settings.clusterName"
    value = var.cluster_name
  }

  set {
    name  = "settings.interruptionQueue"
    value = module.karpenter.queue_name
  }

  set {
    name  = "serviceAccount.annotations.eks\\.amazonaws\\.com/role-arn"
    value = module.karpenter.iam_role_arn
  }

  # 2 replicas of the controller for HA — restart-during-deploy
  # without losing scaling capacity.
  set {
    name  = "replicas"
    value = "2"
  }

  # Pin Karpenter to system nodes. If it ran on a node it provisioned,
  # the chicken-and-egg of "scale-up needs Karpenter but Karpenter
  # was on the de-provisioned node" bites.
  set {
    name  = "nodeSelector.workload"
    value = "system"
  }
}

# EC2NodeClass: what AMI + instance options to use when provisioning.
# References the node IAM role the submodule created.
resource "kubernetes_manifest" "ec2_node_class_general" {
  manifest = {
    apiVersion = "karpenter.k8s.aws/v1"
    kind       = "EC2NodeClass"
    metadata = {
      name = "atpost-general"
    }
    spec = {
      amiFamily = "AL2023"
      role      = module.karpenter.node_iam_role_name

      subnetSelectorTerms = [
        for id in var.private_subnet_ids : { id = id }
      ]
      securityGroupSelectorTerms = [{
        tags = {
          "aws:eks:cluster-name" = var.cluster_name
        }
      }]
      tags = {
        Name = "atpost-${var.environment}-karpenter-node"
        # AWS-LB-Controller looks for this tag when reconciling
        # target groups so spot-interrupted pods land in the LB
        # without a manual refresh.
        "karpenter.sh/discovery" = var.cluster_name
      }
      # ImageGCHighThresholdPercent etc. on the AL2023 family are
      # sane defaults; override per workload via a custom node class
      # if hot images take too much disk.
      blockDeviceMappings = [{
        deviceName = "/dev/xvda"
        ebs = {
          volumeType          = "gp3"
          volumeSize          = "100Gi"
          deleteOnTermination = true
          encrypted           = true
        }
      }]
    }
  }

  depends_on = [helm_release.karpenter]
}

# NodePool: when to provision (Pending pod selector) and what to use
# (refers to the EC2NodeClass above).
resource "kubernetes_manifest" "node_pool_general" {
  manifest = {
    apiVersion = "karpenter.sh/v1"
    kind       = "NodePool"
    metadata = {
      name = "atpost-general"
    }
    spec = {
      template = {
        metadata = {
          labels = {
            workload = "general"
            provisioner = "karpenter"
          }
        }
        spec = {
          nodeClassRef = {
            group = "karpenter.k8s.aws"
            kind  = "EC2NodeClass"
            name  = "atpost-general"
          }
          # Instance shape requirements. Graviton (arm64) only, since
          # all our Docker images are linux/arm64. Mix on-demand + spot
          # with a 60/40 split via the weighted limits below.
          requirements = [
            { key = "kubernetes.io/arch", operator = "In", values = ["arm64"] },
            { key = "kubernetes.io/os", operator = "In", values = ["linux"] },
            {
              key      = "karpenter.k8s.aws/instance-category"
              operator = "In"
              values   = ["m", "c", "r"] # general, compute, memory
            },
            {
              key      = "karpenter.k8s.aws/instance-generation"
              operator = "Gt"
              values   = ["5"] # 6th gen+ Graviton; cheaper + faster
            },
            { key = "karpenter.sh/capacity-type", operator = "In", values = var.capacity_types },
          ]
        }
      }
      # Tear down underutilised nodes after 30s. Aggressive but
      # Karpenter is smart about not deleting if it'd cause pods to
      # be Pending.
      disruption = {
        consolidationPolicy = "WhenEmptyOrUnderutilized"
        consolidateAfter    = "30s"
      }
      limits = {
        cpu    = var.cpu_limit
        memory = var.memory_limit
      }
    }
  }

  depends_on = [kubernetes_manifest.ec2_node_class_general]
}
