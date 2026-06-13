variable "environment" {
  type = string
}

variable "repositories" {
  description = "Service names that get a private ECR repo. Final repo path: atpost/<name>."
  type        = list(string)
}
