# Per-service IRSA — IAM role assumable by the ServiceAccount
# `atpost/<service>` in the EKS cluster. Used by service pods that
# need direct AWS API access (S3 media bucket, MSK IAM auth, etc.).
#
# Most services only need msk_client (every event producer/consumer).
# media-service additionally needs media_client (S3 + KMS). Future
# AWS-touching services (when we wire SQS or DynamoDB elsewhere) add
# their policy ARN to the list.

data "aws_iam_policy_document" "trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    effect  = "Allow"

    principals {
      type        = "Federated"
      identifiers = [var.oidc_provider_arn]
    }

    # Audience claim — sts.amazonaws.com is the only audience the EKS
    # OIDC provider issues for.
    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:aud"
      values   = ["sts.amazonaws.com"]
    }

    # Subject claim — pin the role to the specific ServiceAccount.
    # Without this, ANY pod in the cluster could assume this role.
    condition {
      test     = "StringEquals"
      variable = "${replace(var.oidc_provider_url, "https://", "")}:sub"
      values   = ["system:serviceaccount:${var.k8s_namespace}:${var.k8s_service_account}"]
    }
  }
}

resource "aws_iam_role" "this" {
  name               = "atpost-${var.environment}-${var.service_name}-irsa"
  assume_role_policy = data.aws_iam_policy_document.trust.json

  tags = {
    Name    = "atpost-${var.environment}-${var.service_name}-irsa"
    Service = var.service_name
  }
}

# Attach every policy in `policy_arns`. for_each produces stable IDs
# so reordering the list doesn't churn the plan.
resource "aws_iam_role_policy_attachment" "policies" {
  for_each = { for arn in var.policy_arns : arn => arn }

  role       = aws_iam_role.this.name
  policy_arn = each.value
}
