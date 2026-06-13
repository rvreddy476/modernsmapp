# Bootstrap: provisions the S3 bucket + DynamoDB lock table that hold
# every other Terraform workspace's remote state. Run this ONCE per
# AWS account, manually, with state stored on disk (no backend block).
# After it succeeds, every other workspace's `terraform init` uses the
# outputs here as its backend.
#
# Why a separate workspace: the bucket can't store its own creation
# state in itself. Standard chicken-and-egg pattern.

terraform {
  required_version = ">= 1.6, < 2.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.70"
    }
  }
}

provider "aws" {
  region = var.aws_region
}

variable "aws_region" {
  type    = string
  default = "ap-south-1"
}

variable "state_bucket_name" {
  description = "Globally-unique S3 bucket for Terraform state. Convention: atpost-tfstate-<account-id>."
  type        = string
}

variable "lock_table_name" {
  description = "DynamoDB table for Terraform state lock."
  type        = string
  default     = "atpost-tfstate-locks"
}

# State bucket. Versioning + encryption + block-public-access are
# non-negotiable: a leaked or accidentally-rolled-back state file is
# a credential exposure (state contains every secret Terraform reads).
resource "aws_s3_bucket" "state" {
  bucket = var.state_bucket_name

  # Lifecycle: prevent accidental destroy. To actually delete this
  # bucket you must remove this block first, then `terraform apply`,
  # THEN `terraform destroy`.
  lifecycle {
    prevent_destroy = true
  }
}

resource "aws_s3_bucket_versioning" "state" {
  bucket = aws_s3_bucket.state.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "state" {
  bucket = aws_s3_bucket.state.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm = "AES256"
    }
  }
}

resource "aws_s3_bucket_public_access_block" "state" {
  bucket                  = aws_s3_bucket.state.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Lock table. Terraform uses DynamoDB conditional writes to prevent
# concurrent `apply` from corrupting state. PAY_PER_REQUEST keeps the
# bill at cents — there's basically no traffic here.
resource "aws_dynamodb_table" "lock" {
  name         = var.lock_table_name
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "LockID"

  attribute {
    name = "LockID"
    type = "S"
  }

  lifecycle {
    prevent_destroy = true
  }
}

output "state_bucket" {
  value       = aws_s3_bucket.state.id
  description = "Use this as `bucket = ...` in every envs/<env>/backend.tf."
}

output "lock_table" {
  value       = aws_dynamodb_table.lock.name
  description = "Use this as `dynamodb_table = ...` in every envs/<env>/backend.tf."
}
