# MSK Serverless cluster — Phase-2 decision §2.
#
# Serverless picked over Provisioned because today's load is 1 Redpanda
# broker; even 10x growth fits under the per-topic ceiling (200 MB/s
# ingress, 400 MB/s egress). Pay-per-use, auto-scales, no capacity
# planning. If sustained throughput crosses the break-even later, swap
# to Provisioned via a topic-by-topic MirrorMaker 2 mirror — not
# trivial, but the migration path is documented in the AWS docs.
#
# Auth model: MSK Serverless ONLY supports IAM auth (no SASL/SCRAM,
# no mTLS, no plaintext). Every producer/consumer must speak the
# AWS_MSK_IAM SASL mechanism. The `segmentio/kafka-go` client we use
# everywhere has IAM-SASL support via AWS SDK token signing — no
# library swap needed.
#
# Topics: MSK Serverless can auto-create on first write, but we use
# explicit aws_msk_topic resources in service modules so the topic
# config (partitions, retention, compaction) is reviewable in PR.

resource "aws_security_group" "msk" {
  name        = "atpost-${var.environment}-msk"
  description = "MSK Serverless cluster"
  vpc_id      = var.vpc_id

  tags = {
    Name = "atpost-${var.environment}-msk-sg"
  }
}

# IAM-auth bootstrap port is 9098. No 9092 or 9094 (plaintext / TLS-plain
# aren't supported by Serverless). Only EKS nodes can reach it.
resource "aws_security_group_rule" "msk_iam_from_eks" {
  type                     = "ingress"
  from_port                = 9098
  to_port                  = 9098
  protocol                 = "tcp"
  security_group_id        = aws_security_group.msk.id
  source_security_group_id = var.eks_node_security_group_id
  description              = "MSK 9098 (IAM SASL) from EKS nodes"
}

resource "aws_msk_serverless_cluster" "this" {
  cluster_name = "atpost-${var.environment}"

  vpc_config {
    # MSK Serverless picks 2-3 of these AZs automatically. Pass all
    # three private subnets so the cluster spreads across the full
    # AZ set.
    subnet_ids         = var.private_subnet_ids
    security_group_ids = [aws_security_group.msk.id]
  }

  # Serverless mandates IAM auth — no SASL/SCRAM block here.
  client_authentication {
    sasl {
      iam {
        enabled = true
      }
    }
  }

  tags = {
    Name = "atpost-${var.environment}-msk"
  }
}

# Reusable IAM policy doc: read+write any topic, manage consumer groups.
# Apps attach this to their IRSA role. Scope down per service if a
# stricter least-privilege boundary is needed (e.g. payments should
# never write to commerce.events.v1).
data "aws_iam_policy_document" "msk_client" {
  statement {
    sid    = "Connect"
    effect = "Allow"
    actions = [
      "kafka-cluster:Connect",
      "kafka-cluster:DescribeCluster",
    ]
    resources = [aws_msk_serverless_cluster.this.arn]
  }

  statement {
    sid    = "TopicReadWrite"
    effect = "Allow"
    actions = [
      "kafka-cluster:DescribeTopic",
      "kafka-cluster:CreateTopic",
      "kafka-cluster:WriteData",
      "kafka-cluster:ReadData",
    ]
    # Topic ARNs follow the kafka-cluster: format. arn:aws:kafka:
    # <region>:<account>:topic/<cluster-name>/<cluster-uuid>/<topic>.
    # Wildcard topic scope is the simplest start; tighten per service
    # if needed.
    resources = [replace(aws_msk_serverless_cluster.this.arn, "cluster/", "topic/")]
  }

  statement {
    sid    = "ConsumerGroup"
    effect = "Allow"
    actions = [
      "kafka-cluster:AlterGroup",
      "kafka-cluster:DescribeGroup",
    ]
    resources = [replace(aws_msk_serverless_cluster.this.arn, "cluster/", "group/")]
  }
}

resource "aws_iam_policy" "msk_client" {
  name        = "atpost-${var.environment}-msk-client"
  description = "Standard MSK client policy. Attach to IRSA roles that need to produce/consume."
  policy      = data.aws_iam_policy_document.msk_client.json

  tags = {
    Name = "atpost-${var.environment}-msk-client"
  }
}
