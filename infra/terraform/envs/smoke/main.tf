# Media-only smoke environment — the cheapest thing that can prove S3,
# CloudFront, bucket CORS and IAM are real.
#
# ## WHY THIS ROOT EXISTS
#
# The staging root (`../staging`) builds 23 modules: EKS, Aurora, MSK,
# OpenSearch, ElastiCache, Scylla, WAF, NAT gateways. None of that is needed to
# answer the only question the local stack cannot answer: does media actually
# work against real AWS?
#
# MinIO proves the SHAPE of the upload flow. It cannot prove S3 presigning,
# bucket CORS, CloudFront delivery, or an IAM policy that is narrow enough to
# work and narrow enough to be safe. This root provisions exactly those and
# nothing else, so the smoke costs a bucket, a distribution and a user rather
# than a cluster.
#
# `modules/media` is reused verbatim — it has no VPC or EKS coupling, so it
# stands alone without modification.
#
# ## STATE IS LOCAL, DELIBERATELY
#
# The shared S3 backend does not exist yet (`../staging/backend.tf` is still a
# TODO). A throwaway smoke environment should not be what forces that decision.
# Local state also makes `terraform destroy` a one-liner when the smoke is done,
# which is the point.

terraform {
  required_version = ">= 1.5"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 5.0"
    }
  }
}

provider "aws" {
  region = var.region
  # Decision 002: ap-south-1, so Indian user media stays in India.
}

module "media" {
  source = "../../modules/media"

  environment = "smoke"

  # NOT "*". The presigned-upload story depends on the origin list being real:
  # a wildcard here would let any page drive an upload with a leaked URL.
  cors_allowed_origins = var.cors_allowed_origins

  # India + Asia included. The users this is for are in India, and a smoke test
  # served from a US-only edge tier would measure the wrong latency.
  cloudfront_price_class = "PriceClass_200"
}
