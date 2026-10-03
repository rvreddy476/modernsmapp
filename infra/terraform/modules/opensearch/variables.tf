variable "environment" {
  type = string
}

variable "vpc_id" {
  type = string
}

variable "isolated_subnet_ids" {
  description = "Isolated subnet IDs across 3 AZs. Domain ENIs land in the first 3."
  type        = list(string)
}

variable "eks_node_security_group_id" {
  description = "EKS node SG. OpenSearch 443 ingress restricted to this SG."
  type        = string
}

variable "master_username" {
  description = "OpenSearch master user for Kibana break-glass."
  type        = string
  default     = "atpost_admin"
}

variable "data_instance_type" {
  description = "Data node instance type. r6g.large.search (Graviton, 2 vCPU, 16 GB) for prod; t3.small.search for staging."
  type        = string
  default     = "r6g.large.search"
}

variable "data_instance_count" {
  description = "Data node count. 3 for prod multi-AZ; 1 for staging."
  type        = number
  default     = 3
}

variable "dedicated_master_enabled" {
  description = "Provision 3 dedicated master nodes for quorum stability. Required at prod scale; off in staging."
  type        = bool
  default     = true
}

variable "master_instance_type" {
  description = "Dedicated master node type. m6g.large.search is the AWS-recommended starter."
  type        = string
  default     = "m6g.large.search"
}

variable "ebs_volume_size_gb" {
  description = "Per-node EBS gp3 volume size. 100 GB for prod; 30 GB for staging."
  type        = number
  default     = 100
}

variable "engine_version" {
  description = "OpenSearch engine version. 2.19 is the newest 2.x (standard support to Nov 2027); 2.13 (the original pin) ends standard support the same day but is three releases behind. 3.x needs a client-compat check first."
  type        = string
  default     = "OpenSearch_2.19"
}

variable "ebs_throughput_mibps" {
  description = "gp3 throughput per node. 125 is the free baseline (lean); 250 for prod-scale data nodes."
  type        = number
  default     = 250
}

variable "ebs_iops" {
  description = "gp3 IOPS per node. 3000 is the free baseline."
  type        = number
  default     = 3000
}
