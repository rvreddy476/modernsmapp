variable "environment" {
  type = string
}

variable "bootstrap_brokers" {
  description = "SASL/SCRAM bootstrap broker list from the msk module (bootstrap_brokers_sasl_scram)."
  type        = string
}

variable "scram_secret_name" {
  description = "Secrets Manager name of the AmazonMSK_ SCRAM secret (msk module: scram_secret_name)."
  type        = string
}

variable "cluster_secret_store_name" {
  description = "ClusterSecretStore created by the external-secrets module."
  type        = string
  default     = "aws-secrets-manager"
}

variable "topics" {
  description = <<EOT
Every topic the services produce to or consume from. The 18 from
Architecture/docker/init-topics.sh plus the ones the code uses without
being in that script (aws-fixes contract, 3 Oct 2026). Adding a topic
re-runs the Job; removing one does NOT delete it.
EOT
  type        = list(string)
  default = [
    # Architecture/docker/init-topics.sh
    "social.events.v1",
    "social.events.v1.dlq",
    "chat.events.v1",
    "identity.events.v1",
    "call.lifecycle",
    "call.notifications",
    "call.analytics",
    "platform-events",
    "media.events",
    "media.copyright.pairs",
    "atpost.channel.updates",
    "atpost.channel.notifications",
    "atpost.channel.feed-inject",
    "qa-events",
    "dating-events",
    "wallet-events",
    "billpay-events",
    "rider-events",
    # Used by code but missing from the script
    "platform.purge-acks.v1",
    "food-events",
    "channel-events",
    "community-events",
    "group-events",
    "monetization.events",
    "search.events.v1.dlq",
    "media.events.dlq",
  ]
}

variable "partitions" {
  description = "Partitions per topic. 6 in prod (contract); kafka.t3.small allows 300 partitions per broker."
  type        = number
  default     = 6
}

variable "replication_factor" {
  description = "Replication factor per topic. Cannot exceed the broker count (2 in the lean profile)."
  type        = number
  default     = 2
}

variable "topic_configs" {
  description = "Extra topic-level configs applied to every topic (kafka-topics.sh --config k=v)."
  type        = map(string)
  default = {
    "retention.ms" = "604800000" # 7 days
  }
}

variable "kafka_image" {
  description = "Image with /opt/kafka/bin/kafka-topics.sh. The official apache/kafka image has it at that path."
  type        = string
  default     = "apache/kafka:3.9.1"
}
