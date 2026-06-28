variable "environment" {
  type = string
}

variable "resource_group_name" {
  type = string
}

variable "location" {
  type = string
}

variable "acr_id" {
  type = string
}

variable "aks_id" {
  type = string
}

variable "github_subjects" {
  description = <<EOT
GitHub OIDC subjects allowed to assume the CI identity, e.g.
  ["repo:rvreddy476/modernsmapp:ref:refs/heads/main",
   "repo:rvreddy476/modernsmapp:environment:staging"]
EOT
  type        = list(string)
}
