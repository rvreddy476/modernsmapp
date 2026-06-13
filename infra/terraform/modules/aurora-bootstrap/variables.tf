variable "environment" {
  type = string
}

variable "master_secret_name" {
  description = "Name (key) of the master credentials in AWS Secrets Manager. From the aurora module: atpost/${env}/aurora/master."
  type        = string
}

variable "cluster_secret_store_name" {
  description = "Name of the ClusterSecretStore the external-secrets module created. Usually `aws-secrets-manager`."
  type        = string
  default     = "aws-secrets-manager"
}

variable "databases" {
  description = "Logical database names to CREATE inside the Aurora cluster. Matches the docker-compose convention."
  type        = list(string)
  default = [
    "app",
    "identity_db",
    "chat_db",
    "commerce_db",
    "feed_db",
  ]
}
