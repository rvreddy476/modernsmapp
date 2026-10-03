variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "private_subnet_ids" {
  description = "Private subnet IDs across 3 AZs. The first `number_of_broker_nodes` are used (one broker per subnet)."
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "EKS node SG. Broker ingress (9096 SCRAM/TLS, 9098 IAM when enabled) is restricted to this SG."
  type        = string
}

variable "kafka_version" {
  description = "MSK Kafka version string, e.g. 3.9.x (KRaft). Check `aws kafka get-compatible-kafka-versions` before changing."
  type        = string
  default     = "3.9.x"
}

variable "broker_instance_type" {
  description = "Broker instance type. kafka.t3.small for the lean profile (≤300 partitions per broker); kafka.m7g.large and up for sustained load."
  type        = string
  default     = "kafka.t3.small"
}

variable "number_of_broker_nodes" {
  description = "Broker count. Must be a multiple of the number of subnets used; 2 → two AZs."
  type        = number
  default     = 2

  validation {
    condition     = var.number_of_broker_nodes >= 2 && var.number_of_broker_nodes <= 3
    error_message = "number_of_broker_nodes must be 2 or 3 (one per subnet, three subnets available)."
  }
}

variable "broker_ebs_volume_size_gb" {
  description = "EBS volume per broker, GB."
  type        = number
  default     = 100
}

variable "default_replication_factor" {
  description = "Broker default.replication.factor. Cannot exceed the broker count."
  type        = number
  default     = 2
}

variable "min_insync_replicas" {
  description = "Broker min.insync.replicas. 1 with two brokers so a single broker restart does not block acks=all producers."
  type        = number
  default     = 1
}

variable "default_partitions" {
  description = "Broker num.partitions (only used if a topic is created without an explicit count)."
  type        = number
  default     = 6
}

variable "log_retention_hours" {
  description = "Broker default log.retention.hours (7 days)."
  type        = number
  default     = 168
}

variable "broker_log_retention_days" {
  description = "CloudWatch retention for broker logs."
  type        = number
  default     = 14
}

variable "scram_username" {
  description = "SASL/SCRAM application username. Becomes part of the AmazonMSK_ secret name."
  type        = string
  default     = "atpost"
}

variable "enable_iam_auth" {
  description = "Also enable AWS_MSK_IAM auth (port 9098) and create the IRSA client policy. The services use SCRAM; leave false unless an IAM-only client appears."
  type        = bool
  default     = false
}
