variable "subscription_id" {
  type = string
}

variable "location" {
  type    = string
  default = "centralindia"
}

variable "environment" {
  type    = string
  default = "staging"
}

variable "github_subjects" {
  description = "GitHub OIDC subjects allowed to assume the CI identity."
  type        = list(string)
  default = [
    "repo:rvreddy476/modernsmapp:ref:refs/heads/main",
    "repo:rvreddy476/modernsmapp:environment:staging",
  ]
}
