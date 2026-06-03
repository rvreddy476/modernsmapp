# EKS cluster + three managed node groups.
#
# Built on terraform-aws-modules/eks/aws (community, well-tested) rather
# than hand-rolled aws_eks_cluster + node group resources — the module
# encodes hundreds of subtle settings (OIDC IRSA trust, KMS encryption,
# add-on dependency ordering, security group rules) that would otherwise
# need to be hand-maintained. Pinned to a major version we've vetted.
#
# Node group split (decision from aws_phase2_decisions §3):
#   general    m7g.large  — stateless app pods (Graviton; ~30 services)
#   memory     r7g.xlarge — Scylla StatefulSet (RF=3, one pod/AZ)
#   system     m7g.medium — ingress, ArgoCD, OTel, kube-system

module "eks" {
  source  = "terraform-aws-modules/eks/aws"
  version = "~> 20.29"

  cluster_name    = "atpost-${var.environment}"
  cluster_version = "1.31"

  # API endpoint: public + private. Public limited to operator CIDRs via
  # cluster_endpoint_public_access_cidrs (prod restricts further). Going
  # fully private would force a bastion/VPN for `kubectl`; we accept the
  # public surface in exchange for ops simplicity, and rely on IAM
  # auth + the access-entry RBAC binding below to gate the surface.
  cluster_endpoint_public_access       = true
  cluster_endpoint_private_access      = true
  cluster_endpoint_public_access_cidrs = var.cluster_endpoint_public_access_cidrs

  # etcd secret encryption with a customer-managed KMS key. The module
  # auto-creates the key (alias atpost-${env}-eks). Required for PCI
  # SAQ-A and India DPDP "encryption at rest" attestations.
  cluster_encryption_config = {
    resources = ["secrets"]
  }

  # Control-plane logging to CloudWatch. `api` + `audit` + `authenticator`
  # is the minimum useful set for an incident postmortem; `scheduler` +
  # `controllerManager` add lots of volume and are rarely useful unless
  # actively debugging scheduler behaviour.
  cluster_enabled_log_types              = ["api", "audit", "authenticator"]
  cloudwatch_log_group_retention_in_days = var.log_retention_days

  vpc_id                   = var.vpc_id
  subnet_ids               = var.private_subnet_ids
  control_plane_subnet_ids = var.private_subnet_ids

  # OIDC provider for IAM Roles for Service Accounts. Every service that
  # needs AWS API access (External Secrets Operator, CSI drivers,
  # CloudWatch agent, app-side S3/SQS clients) attaches an IRSA-bound
  # role — no static AWS keys in pods.
  enable_irsa = true

  # Add-ons. EKS-managed add-ons get auto-upgraded with cluster version
  # bumps (MOST_RECENT). VPC CNI gets the dedicated AmazonEKS_CNI_Policy
  # via IRSA so the pod IPs flow.
  cluster_addons = {
    vpc-cni = {
      most_recent              = true
      service_account_role_arn = module.vpc_cni_irsa.iam_role_arn
    }
    coredns = {
      most_recent = true
    }
    kube-proxy = {
      most_recent = true
    }
    aws-ebs-csi-driver = {
      most_recent              = true
      service_account_role_arn = module.ebs_csi_irsa.iam_role_arn
    }
  }

  eks_managed_node_group_defaults = {
    ami_type       = "AL2023_ARM_64_STANDARD" # Graviton-native AMIs
    capacity_type  = "ON_DEMAND"
    disk_size      = 100
    instance_types = ["m7g.large"]

    iam_role_additional_policies = {
      # CloudWatch agent on every node for kubelet + container metrics.
      AmazonSSMManagedInstanceCore = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
      CloudWatchAgentServerPolicy  = "arn:aws:iam::aws:policy/CloudWatchAgentServerPolicy"
    }
  }

  eks_managed_node_groups = {
    general = {
      name           = "atpost-${var.environment}-general"
      instance_types = ["m7g.large"]
      min_size       = var.general_node_min
      max_size       = var.general_node_max
      desired_size   = var.general_node_desired
      labels = {
        workload = "general"
      }
    }

    memory = {
      name           = "atpost-${var.environment}-memory"
      instance_types = [var.memory_node_instance_type]
      min_size       = var.memory_node_min
      max_size       = var.memory_node_max
      desired_size   = var.memory_node_desired
      labels = {
        workload = "memory"
      }
      # Scylla pods tolerate this taint via their StatefulSet spec, so
      # noisy app pods can't accidentally schedule onto memory-tier
      # nodes and OOM the database under load.
      taints = {
        scylla = {
          key    = "workload"
          value  = "scylla"
          effect = "NO_SCHEDULE"
        }
      }
    }

    system = {
      name           = "atpost-${var.environment}-system"
      instance_types = ["m7g.medium"]
      min_size       = 3 # one per AZ for ingress HA
      max_size       = 6
      desired_size   = 3
      labels = {
        workload = "system"
      }
    }
  }

  # Cluster-creator access. The IAM principal that runs `terraform apply`
  # gets admin via an access entry — without this you'd have to bootstrap
  # access manually via aws-auth ConfigMap from a separate machine.
  enable_cluster_creator_admin_permissions = true

  # Extra admins (break-glass + ops on-call). cluster_admin_arns is empty
  # by default; populate in tfvars.
  access_entries = {
    for arn in var.cluster_admin_arns : "admin-${replace(arn, "/[^a-zA-Z0-9]/", "-")}" => {
      principal_arn = arn
      policy_associations = {
        admin = {
          policy_arn = "arn:aws:iam::aws:policy/AmazonEKSClusterAdminPolicy"
          access_scope = {
            type = "cluster"
          }
        }
      }
    }
  }

  tags = {
    Name = "atpost-${var.environment}-eks"
  }
}

# IRSA module for VPC CNI. Module rolls the IAM role + trust policy that
# binds it to the kube-system/aws-node service account.
module "vpc_cni_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.48"

  role_name             = "atpost-${var.environment}-vpc-cni"
  attach_vpc_cni_policy = true
  vpc_cni_enable_ipv4   = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:aws-node"]
    }
  }
}

# IRSA for the EBS CSI driver — the cluster needs this to provision PVs
# for any pod that requests `storageClassName: gp3` (Scylla StatefulSet,
# OpenSearch, monitoring stack).
module "ebs_csi_irsa" {
  source  = "terraform-aws-modules/iam/aws//modules/iam-role-for-service-accounts-eks"
  version = "~> 5.48"

  role_name             = "atpost-${var.environment}-ebs-csi"
  attach_ebs_csi_policy = true

  oidc_providers = {
    main = {
      provider_arn               = module.eks.oidc_provider_arn
      namespace_service_accounts = ["kube-system:ebs-csi-controller-sa"]
    }
  }
}

# gp3 StorageClass — gp3 is cheaper + faster than gp2 (the EKS default),
# so make it the cluster default. Existing PVCs keep their class; new
# PVCs without an explicit class land here.
#
# The module emits a kubernetes_manifest but applying it requires the
# kubernetes provider to be authenticated against the cluster — which
# requires the cluster to already exist. To avoid the circular
# dependency, the manifest lives in a separate post-create step
# (see envs/<env>/k8s.tf, applied after the cluster output is known).
