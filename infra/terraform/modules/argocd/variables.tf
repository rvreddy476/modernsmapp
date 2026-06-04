variable "environment" {
  type = string
}

variable "chart_version" {
  description = "argo-helm/argo-cd chart version. Bump after reading the project's release notes for CRD migrations."
  type        = string
  default     = "7.7.7"
}

variable "ingress_scheme" {
  description = "ALB scheme. internet-facing in staging (UI from anywhere); internal in prod (VPN-gated)."
  type        = string
  default     = "internal"
}

variable "argocd_hostname" {
  description = "Hostname for the ArgoCD UI. Example: argocd.staging.aws.cleestudio.com."
  type        = string
}

variable "acm_certificate_arn" {
  description = "ACM wildcard cert ARN. From the dns module output."
  type        = string
}

variable "allowed_source_repos" {
  description = "Git repos ArgoCD may pull from. Format: https://github.com/<org>/<repo>.git or `*` for any."
  type        = list(string)
  default = [
    "https://github.com/*/atpost.git",
    "https://github.com/*/atpost-helm-charts.git",
  ]
}

variable "applicationset_manifest_path" {
  description = <<EOT
Optional. Path to a YAML file containing one or more ApplicationSet
manifests (deploy/argocd/applicationset.yaml in this repo). When set,
the module applies it via kubernetes_manifest so the per-service
Applications come up automatically post-install. Leave empty to manage
the ApplicationSet out-of-band.
EOT
  type        = string
  default     = ""
}
