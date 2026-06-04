output "controller_iam_role_arn" {
  value = module.karpenter.iam_role_arn
}

output "node_iam_role_name" {
  value = module.karpenter.node_iam_role_name
}

output "interruption_queue_name" {
  value = module.karpenter.queue_name
}

output "node_pool_name" {
  value       = "atpost-general"
  description = "Default NodePool. Pods without a nodeSelector land here. App workloads target labels.workload = general."
}
