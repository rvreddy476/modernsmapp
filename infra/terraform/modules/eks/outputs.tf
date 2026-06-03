output "cluster_name" {
  value = module.eks.cluster_name
}

output "cluster_endpoint" {
  value       = module.eks.cluster_endpoint
  description = "Cluster API endpoint. Feed to kubectl / Helm / ArgoCD configs."
}

output "cluster_certificate_authority_data" {
  value       = module.eks.cluster_certificate_authority_data
  description = "Base64-encoded CA cert. Required for kubeconfig + downstream kubernetes provider config."
}

output "cluster_security_group_id" {
  value       = module.eks.cluster_security_group_id
  description = "Control-plane security group. Use for ingress rules from any service that needs to reach the API."
}

output "oidc_provider_arn" {
  value       = module.eks.oidc_provider_arn
  description = "OIDC provider ARN for IRSA. Pass to any module that creates a service-account-bound IAM role."
}

output "oidc_provider_url" {
  value       = module.eks.cluster_oidc_issuer_url
  description = "OIDC issuer URL. Used in IRSA trust policies; usually the IRSA submodule consumes this directly."
}

output "ebs_csi_irsa_role_arn" {
  value = module.ebs_csi_irsa.iam_role_arn
}

output "node_security_group_id" {
  value       = module.eks.node_security_group_id
  description = "Node security group. Add ingress rules here for any in-cluster service that needs to reach node ports (Scylla, etc.)."
}
