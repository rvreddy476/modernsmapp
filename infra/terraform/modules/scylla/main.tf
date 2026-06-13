# Scylla Operator + ScyllaCluster on EKS.
#
# Phase-2 decision §3: stay on Scylla (vs. DynamoDB rewrite). 3-replica
# StatefulSet across AZs, RF=3, EBS gp3 per replica. Pods land on the
# memory node group (r7g.xlarge) which has the workload=scylla:NoSchedule
# taint — only ScyllaCluster pods tolerate it, so noisy app pods can't
# accidentally land here and OOM the DB.
#
# The Scylla Operator manages the lifecycle: provisioning new replicas,
# rolling upgrades, member replacements on node loss. We just declare
# the desired shape in a ScyllaCluster CR.
#
# Storage class: gp3 with the EBS CSI driver already installed by the
# eks module's add-on. Volume binding is WaitForFirstConsumer so the
# PV lands in the same AZ as the pod (Scylla's AZ-aware sharding
# depends on this).

resource "kubernetes_namespace" "scylla_operator" {
  metadata {
    name = "scylla-operator"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

resource "kubernetes_namespace" "scylla" {
  metadata {
    name = "scylla"
    labels = {
      "app.kubernetes.io/managed-by" = "terraform"
    }
  }
}

# Storage class: gp3 with WaitForFirstConsumer + delete reclaim policy.
# Standard EKS install ships `gp2` as the default; we want gp3 for the
# 4x IOPS-per-GB ratio + the ability to provision IOPS / throughput
# independently of size.
resource "kubernetes_storage_class" "gp3" {
  metadata {
    name = "gp3"
    annotations = {
      "storageclass.kubernetes.io/is-default-class" = "true"
    }
  }
  storage_provisioner    = "ebs.csi.aws.com"
  reclaim_policy         = "Delete"
  volume_binding_mode    = "WaitForFirstConsumer"
  allow_volume_expansion = true

  parameters = {
    type       = "gp3"
    fsType     = "ext4"
    encrypted  = "true"
    iops       = "3000"
    throughput = "250"
  }
}

# Scylla Operator Helm chart. Two-step install: the operator chart
# brings CRDs + the operator controller; ScyllaCluster CRs (below)
# declare the actual clusters.
resource "helm_release" "scylla_operator" {
  name       = "scylla-operator"
  repository = "https://scylla-operator-charts.storage.googleapis.com/stable"
  chart      = "scylla-operator"
  version    = var.operator_chart_version
  namespace  = kubernetes_namespace.scylla_operator.metadata[0].name

  set {
    name  = "nodeSelector.workload"
    value = "system"
  }

  # Operator HA — 2 replicas survive a node restart without dropping
  # reconciliation. The operator itself is light; this is platform
  # tooling, not DB load.
  set {
    name  = "replicas"
    value = "2"
  }
}

# ScyllaCluster CR — the actual database. Replication factor 3
# (one replica per AZ); per-replica storage on the gp3 class above.
# rackConfig.members=1 means one pod per "rack"; we run one rack per
# AZ so the operator places pods correctly.
resource "kubernetes_manifest" "scylla_cluster" {
  manifest = {
    apiVersion = "scylla.scylladb.com/v1"
    kind       = "ScyllaCluster"
    metadata = {
      name      = "atpost-${var.environment}"
      namespace = kubernetes_namespace.scylla.metadata[0].name
    }
    spec = {
      version = var.scylla_version
      agentVersion = var.scylla_agent_version

      developerMode = false

      datacenter = {
        name = "ap-south-1"
        racks = [
          for az in var.availability_zones : {
            name    = az
            members = 1
            scyllaConfig = "scylla-config"
            scyllaAgentConfig = "scylla-agent-config"

            # Land on the memory node group only. Without the
            # tolerations the workload=scylla:NoSchedule taint
            # keeps pods off.
            placement = {
              nodeAffinity = {
                requiredDuringSchedulingIgnoredDuringExecution = {
                  nodeSelectorTerms = [{
                    matchExpressions = [{
                      key      = "workload"
                      operator = "In"
                      values   = ["memory"]
                    }, {
                      key      = "topology.kubernetes.io/zone"
                      operator = "In"
                      values   = [az]
                    }]
                  }]
                }
              }
              tolerations = [{
                key      = "workload"
                operator = "Equal"
                value    = "scylla"
                effect   = "NoSchedule"
              }]
            }

            resources = {
              limits = {
                cpu    = var.cpu_per_replica
                memory = var.memory_per_replica
              }
              requests = {
                cpu    = var.cpu_per_replica
                memory = var.memory_per_replica
              }
            }

            storage = {
              capacity         = var.storage_per_replica
              storageClassName = kubernetes_storage_class.gp3.metadata[0].name
            }
          }
        ]
      }
    }
  }

  depends_on = [helm_release.scylla_operator]
}
