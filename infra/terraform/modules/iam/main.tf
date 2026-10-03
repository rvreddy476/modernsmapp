# GitHub Actions OIDC + a CI role scoped to ECR push + plan, and an
# optional Terraform APPLY role for the production environment.
#
# IAM Identity Center (the human-access SSO story) is account-level
# infra and lives in the master account, not per-env. This module
# covers the workload-account pieces: the OIDC provider GitHub uses
# to assume roles, and roles with just-enough permissions.
#
# Trust shapes (GitHub's `sub` claim):
#   repo:ORG/REPO:ref:refs/heads/main      push / workflow_dispatch on main
#   repo:ORG/REPO:environment:prod         a job that declares `environment: prod`
#   repo:ORG/REPO:pull_request             PR-triggered workflows
# When a job uses an environment, the sub carries the environment and NOT
# the ref; branch restriction for the apply role therefore comes from the
# GitHub environment's "deployment branches" rule (main only), which GitHub
# enforces before the token is minted. Document that rule in the repo.

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

locals {
  # Explicit subjects win; otherwise any branch/PR of each repo (staging).
  ci_subjects = length(var.ci_github_subjects) > 0 ? var.ci_github_subjects : [for r in var.github_repos : "repo:${r}:*"]
}

# CI role: trusted to be assumed only by the GitHub OIDC provider, only
# from the subjects above.
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
      values   = local.ci_subjects
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
      "ecr:BatchGetImage",
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
# behind the stricter role below.
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

# ─── Terraform apply role ───────────────────────────────────────────
#
# Assumed only by the subjects in `apply_github_subjects` (prod: the
# `environment:prod` subject of this repo — the GitHub environment pins the
# branch to main and requires a reviewer). It carries AdministratorAccess
# because this configuration creates IAM roles, KMS keys and every managed
# service; the explicit denies below stop it from leaving the account or
# removing its own guard rails. Session duration is capped at one hour.

data "aws_iam_policy_document" "apply_trust" {
  count = var.create_terraform_apply_role ? 1 : 0

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
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:sub"
      values   = var.apply_github_subjects
    }
  }
}

resource "aws_iam_role" "terraform_apply" {
  count = var.create_terraform_apply_role ? 1 : 0

  name                 = "atpost-${var.environment}-terraform-apply"
  assume_role_policy   = data.aws_iam_policy_document.apply_trust[0].json
  max_session_duration = 3600

  tags = {
    Name = "atpost-${var.environment}-terraform-apply"
  }
}

resource "aws_iam_role_policy_attachment" "terraform_apply_admin" {
  count = var.create_terraform_apply_role ? 1 : 0

  role       = aws_iam_role.terraform_apply[0].name
  policy_arn = "arn:aws:iam::aws:policy/AdministratorAccess"
}

data "aws_iam_policy_document" "apply_guardrails" {
  count = var.create_terraform_apply_role ? 1 : 0

  statement {
    sid    = "NeverLeaveTheAccount"
    effect = "Deny"
    actions = [
      "organizations:*",
      "account:CloseAccount",
      "account:PutAlternateContact",
      "iam:CreateUser",
      "iam:CreateAccessKey",
      "iam:CreateLoginProfile",
      "sts:AssumeRole",
    ]
    resources = ["*"]
  }

  statement {
    sid    = "KeepTheGuardRails"
    effect = "Deny"
    actions = [
      "iam:DeleteOpenIDConnectProvider",
      "iam:UpdateAssumeRolePolicy",
      "iam:DeleteRole",
      "iam:DeleteRolePolicy",
      "iam:DetachRolePolicy",
      "iam:PutRolePermissionsBoundary",
      "iam:DeleteRolePermissionsBoundary",
    ]
    resources = [
      aws_iam_openid_connect_provider.github.arn,
      aws_iam_role.ci.arn,
      "arn:aws:iam::${data.aws_caller_identity.current.account_id}:role/atpost-${var.environment}-terraform-apply",
    ]
  }

  statement {
    sid    = "StateBucketStays"
    effect = "Deny"
    actions = [
      "s3:DeleteBucket",
      "s3:PutBucketVersioning",
      "dynamodb:DeleteTable",
    ]
    resources = [
      var.tfstate_bucket_arn,
      var.tfstate_lock_table_arn,
    ]
  }
}

resource "aws_iam_role_policy" "apply_guardrails" {
  count = var.create_terraform_apply_role ? 1 : 0

  name   = "guardrails"
  role   = aws_iam_role.terraform_apply[0].id
  policy = data.aws_iam_policy_document.apply_guardrails[0].json
}
