# GitHub Actions OIDC + a CI role scoped to ECR push.
#
# IAM Identity Center (the human-access SSO story) is account-level
# infra and lives in the master account, not per-env. This module
# covers the workload-account pieces: the OIDC provider GitHub uses
# to assume roles, and a CI role with just-enough permissions to
# push images and run plans.

data "aws_caller_identity" "current" {}

# OIDC provider for GitHub Actions. Thumbprint per AWS docs (rotated
# 2023-06; if GitHub's intermediate cert changes, look up the new SHA1
# at https://docs.github.com/en/actions/deployment/security-hardening-your-deployments/configuring-openid-connect-in-amazon-web-services).
resource "aws_iam_openid_connect_provider" "github" {
  url             = "https://token.actions.githubusercontent.com"
  client_id_list  = ["sts.amazonaws.com"]
  thumbprint_list = ["6938fd4d98bab03faadb97b34396831e3780aea1"]

  tags = {
    Name = "atpost-${var.environment}-oidc-github"
  }
}

# CI role: trusted to be assumed only by the GitHub OIDC provider, only
# from workflows in our org/repo. The `repo:...:ref:refs/heads/main`
# condition prevents PRs from a fork from impersonating prod CI.
data "aws_iam_policy_document" "ci_trust" {
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    effect  = "Allow"

    principals {
      type        = "Federated"
      identifiers = [aws_iam_openid_connect_provider.github.arn]
    }

    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }

    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      values   = [for r in var.github_repos : "repo:${r}:*"]
    }
  }
}

resource "aws_iam_role" "ci" {
  name               = "atpost-${var.environment}-ci-github"
  assume_role_policy = data.aws_iam_policy_document.ci_trust.json

  tags = {
    Name = "atpost-${var.environment}-ci-github"
  }
}

# ECR push policy — scoped to repos managed by terraform.
data "aws_iam_policy_document" "ci_ecr_push" {
  statement {
    sid     = "ECRAuth"
    actions = ["ecr:GetAuthorizationToken"]
    # GetAuthorizationToken is account-scoped, can't be resource-scoped.
    resources = ["*"]
  }

  statement {
    sid = "ECRPush"
    actions = [
      "ecr:BatchCheckLayerAvailability",
      "ecr:CompleteLayerUpload",
      "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload",
      "ecr:PutImage",
      "ecr:UploadLayerPart",
      "ecr:DescribeRepositories",
      "ecr:DescribeImages",
    ]
    resources = [
      for r in var.ecr_repository_arns : r
    ]
  }
}

resource "aws_iam_role_policy" "ci_ecr_push" {
  name   = "ecr-push"
  role   = aws_iam_role.ci.id
  policy = data.aws_iam_policy_document.ci_ecr_push.json
}

# Plan-only role for `terraform plan` from CI on PRs. Apply lives
# behind a separate manually-triggered workflow and a stricter role —
# see PR-#TODO for the apply role.
data "aws_iam_policy_document" "ci_terraform_plan" {
  statement {
    sid = "TerraformPlanReadOnly"
    actions = [
      "ec2:Describe*",
      "ecr:Describe*",
      "iam:Get*",
      "iam:List*",
      "route53:Get*",
      "route53:List*",
      "s3:GetBucket*",
      "s3:ListBucket",
      "dynamodb:DescribeTable",
      "dynamodb:GetItem",
    ]
    resources = ["*"]
  }

  statement {
    sid = "TerraformStateAccess"
    actions = [
      "s3:GetObject",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:ListBucket",
    ]
    resources = [
      var.tfstate_bucket_arn,
      "${var.tfstate_bucket_arn}/*",
    ]
  }

  statement {
    sid = "TerraformLock"
    actions = [
      "dynamodb:GetItem",
      "dynamodb:PutItem",
      "dynamodb:DeleteItem",
    ]
    resources = [var.tfstate_lock_table_arn]
  }
}

resource "aws_iam_role_policy" "ci_terraform_plan" {
  name   = "terraform-plan"
  role   = aws_iam_role.ci.id
  policy = data.aws_iam_policy_document.ci_terraform_plan.json
}
