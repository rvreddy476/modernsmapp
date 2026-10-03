# S3 media bucket + CloudFront distribution.
#
# Replaces the MinIO bucket on the docker-compose stack. atpost
# media-service uses the minio-go client today, but it speaks AWS S3
# natively when pointed at an S3 endpoint — no code change needed.
#
# CDN strategy (Phase-2 decision §5): Cloudflare stays as the public
# DNS edge + free DDoS protection + WAF + bot management for
# cleestudio.com; CloudFront fronts the ALB-served API and the S3
# media bucket. Two cache layers, two WAFs — redundancy not waste.
# This module only owns the AWS half; the Cloudflare CNAME / page
# rules stay in Cloudflare's dashboard.
#
# Delivery contract (media-service internal/delivery/signer.go):
#   public/*     stable unsigned URLs (avatars, public post media)
#   everything else is PROTECTED and only reachable through a CloudFront
#   signed URL (≤ 5 minutes) that media-service issues after content
#   authorization. The distribution enforces that split: the `public/*`
#   behaviour has no key group, the default behaviour requires one.
#
# Custom domain: pass `custom_domain` + `acm_certificate_arn_us_east_1`
# (an ISSUED us-east-1 certificate) to put media.cleestudio.com on the
# distribution. Leave both null on the first apply, add the ACM validation
# CNAME at Cloudflare, then set them — CloudFront refuses a pending cert.

# ─── Media bucket ───────────────────────────────────────────────────
#
# Objects: post images / video transcodes / avatars / story media, under
# the public/ and protected/ prefixes above.

resource "aws_s3_bucket" "media" {
  bucket = "atpost-${var.environment}-media-${random_id.bucket_suffix.hex}"

  tags = {
    Name = "atpost-${var.environment}-media"
  }
}

# 8-char suffix so multiple environments / re-creates don't collide on
# the global S3 namespace. Stays stable across plans.
resource "random_id" "bucket_suffix" {
  byte_length = 4
}

resource "aws_s3_bucket_versioning" "media" {
  bucket = aws_s3_bucket.media.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_public_access_block" "media" {
  bucket                  = aws_s3_bucket.media.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_server_side_encryption_configuration" "media" {
  bucket = aws_s3_bucket.media.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.media.arn
    }
    bucket_key_enabled = true
  }
}

resource "aws_kms_key" "media" {
  description             = "atpost-${var.environment} media S3 bucket encryption"
  enable_key_rotation     = true
  deletion_window_in_days = 30

  tags = {
    Name = "atpost-${var.environment}-media-kms"
  }
}

resource "aws_kms_alias" "media" {
  name          = "alias/atpost-${var.environment}-media"
  target_key_id = aws_kms_key.media.key_id
}

data "aws_caller_identity" "current" {}

# Key policy. Objects are SSE-KMS, so CloudFront's origin access control
# must be able to DECRYPT with this key or every GET returns 403 (the
# bucket policy alone is not enough — the audit's "KMS key without a
# CloudFront-readable policy"). The root statement keeps IAM policies
# (media-service, media-worker, External Secrets) working as before.
data "aws_iam_policy_document" "media_key" {
  statement {
    sid       = "AccountAdministration"
    effect    = "Allow"
    actions   = ["kms:*"]
    resources = ["*"]
    principals {
      type        = "AWS"
      identifiers = ["arn:aws:iam::${data.aws_caller_identity.current.account_id}:root"]
    }
  }

  statement {
    sid    = "CloudFrontDecrypt"
    effect = "Allow"
    actions = [
      "kms:Decrypt",
      "kms:Encrypt",
      "kms:GenerateDataKey*",
    ]
    resources = ["*"]
    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }
    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.media.arn]
    }
  }
}

resource "aws_kms_key_policy" "media" {
  key_id = aws_kms_key.media.id
  policy = data.aws_iam_policy_document.media_key.json
}

# Lifecycle: noncurrent versions (objects replaced or deleted) move to
# Standard-IA after 30 days and expire after 90. Current versions of
# `transcodes/` move to IA after 60 days since they're cold compared
# to originals. Originals stay Standard.
resource "aws_s3_bucket_lifecycle_configuration" "media" {
  bucket = aws_s3_bucket.media.id

  rule {
    id     = "noncurrent-versions"
    status = "Enabled"
    filter {}

    noncurrent_version_transition {
      noncurrent_days = 30
      storage_class   = "STANDARD_IA"
    }

    noncurrent_version_expiration {
      noncurrent_days = 90
    }
  }

  rule {
    id     = "cold-transcodes"
    status = "Enabled"

    filter {
      prefix = "transcodes/"
    }

    transition {
      days          = 60
      storage_class = "STANDARD_IA"
    }
  }

  # Abort multipart uploads that never completed — eats storage cost
  # otherwise (transcoder retries can leave orphan parts).
  rule {
    id     = "abort-incomplete-multipart"
    status = "Enabled"
    filter {}

    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

# CORS: allow the web frontend (postbook-ui) to GET media directly +
# PUT pre-signed uploads. Origin allowlist driven by the env's frontend
# URL — not "*".
resource "aws_s3_bucket_cors_configuration" "media" {
  bucket = aws_s3_bucket.media.id

  cors_rule {
    allowed_headers = ["*"]
    allowed_methods = ["GET", "HEAD", "PUT", "POST"]
    allowed_origins = var.cors_allowed_origins
    expose_headers  = ["ETag", "x-amz-version-id"]
    max_age_seconds = 3000
  }
}

# ─── CloudFront signing key pair ────────────────────────────────────
#
# media-service refuses to start in production without a key pair id and
# the matching private key (cmd/server/main.go buildDeliveryGate). The
# public half is registered with CloudFront; the private half goes to
# Secrets Manager (atpost/<env>/media/cloudfront-signing) for the seeder
# to copy into the media-service secret as media_cloudfront_private_key.
#
# Like modules/auth-keys with manage_values=true, a key generated here
# lands in Terraform state. To keep it out, set manage_signing_key=false,
# generate the pair offline (`openssl genrsa -out cf.pem 2048`) and pass
# the public key PEM in signing_public_key_pem.
resource "tls_private_key" "signing" {
  count     = var.manage_signing_key ? 1 : 0
  algorithm = "RSA"
  rsa_bits  = 2048
}

locals {
  signing_public_key_pem = var.manage_signing_key ? tls_private_key.signing[0].public_key_pem : var.signing_public_key_pem
  custom_domain_enabled  = var.custom_domain != null && var.acm_certificate_arn_us_east_1 != null
}

resource "aws_cloudfront_public_key" "signing" {
  name        = "atpost-${var.environment}-media-signing"
  comment     = "media-service protected-delivery signing key"
  encoded_key = local.signing_public_key_pem

  lifecycle {
    create_before_destroy = true
  }
}

resource "aws_cloudfront_key_group" "signing" {
  name    = "atpost-${var.environment}-media-signing"
  comment = "Key group trusted by the protected media behaviour"
  items   = [aws_cloudfront_public_key.signing.id]
}

resource "aws_secretsmanager_secret" "signing" {
  name                    = "atpost/${var.environment}/media/cloudfront-signing"
  description             = "CloudFront key pair id + private key for media-service protected delivery. The seeder copies both into atpost/${var.environment}/media-service."
  recovery_window_in_days = 7
  kms_key_id              = aws_kms_key.media.arn

  tags = {
    Name = "atpost-${var.environment}-media-cloudfront-signing"
  }
}

resource "aws_secretsmanager_secret_version" "signing" {
  count     = var.manage_signing_key ? 1 : 0
  secret_id = aws_secretsmanager_secret.signing.id
  secret_string = jsonencode({
    media_cloudfront_key_pair_id = aws_cloudfront_public_key.signing.id
    media_cloudfront_private_key = tls_private_key.signing[0].private_key_pem
    media_cdn_base_url           = local.custom_domain_enabled ? "https://${var.custom_domain}" : "https://${aws_cloudfront_distribution.media.domain_name}"
  })

  lifecycle {
    ignore_changes = [secret_string]
  }
}

# ─── CloudFront distribution ────────────────────────────────────────
#
# Public surface for the media bucket. CloudFront uses an Origin Access
# Control (OAC, the modern OAI replacement) to sign requests to S3;
# the bucket policy below allows only the distribution to GetObject,
# so direct s3:// access is impossible.

resource "aws_cloudfront_origin_access_control" "media" {
  name                              = "atpost-${var.environment}-media-oac"
  description                       = "CloudFront → media bucket"
  origin_access_control_origin_type = "s3"
  signing_behavior                  = "always"
  signing_protocol                  = "sigv4"
}

resource "aws_cloudfront_distribution" "media" {
  enabled         = true
  is_ipv6_enabled = true
  http_version    = "http2and3"
  comment         = "atpost-${var.environment} media CDN"
  price_class     = var.cloudfront_price_class
  aliases         = local.custom_domain_enabled ? [var.custom_domain] : []

  origin {
    origin_id                = "s3-media"
    domain_name              = aws_s3_bucket.media.bucket_regional_domain_name
    origin_access_control_id = aws_cloudfront_origin_access_control.media.id
  }

  # public/* — stable, unsigned, long-cached.
  ordered_cache_behavior {
    path_pattern           = "public/*"
    target_origin_id       = "s3-media"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true

    cache_policy_id            = "658327ea-f89d-4fab-a63d-7e88639e58f6" # CachingOptimized
    origin_request_policy_id   = "88a5eaf4-2fd4-4709-b370-b4c650ea3fcf" # CORS-S3Origin
    response_headers_policy_id = aws_cloudfront_response_headers_policy.media.id
  }

  # Everything else is protected: a valid signature from the key group is
  # required or CloudFront answers 403 before touching S3.
  default_cache_behavior {
    target_origin_id       = "s3-media"
    viewer_protocol_policy = "redirect-to-https"
    allowed_methods        = ["GET", "HEAD", "OPTIONS"]
    cached_methods         = ["GET", "HEAD"]
    compress               = true
    trusted_key_groups     = [aws_cloudfront_key_group.signing.id]

    # AWS-managed CachingOptimized policy: 24h default TTL, includes
    # gzip + brotli encoding variants in the cache key. Image / video
    # workload, so this is the right shape. Signature query parameters are
    # consumed by CloudFront and are not part of the cache key.
    cache_policy_id            = "658327ea-f89d-4fab-a63d-7e88639e58f6"
    origin_request_policy_id   = "88a5eaf4-2fd4-4709-b370-b4c650ea3fcf" # CORS-S3Origin
    response_headers_policy_id = aws_cloudfront_response_headers_policy.media.id
  }

  restrictions {
    geo_restriction {
      restriction_type = "none"
    }
  }

  viewer_certificate {
    cloudfront_default_certificate = local.custom_domain_enabled ? null : true
    acm_certificate_arn            = local.custom_domain_enabled ? var.acm_certificate_arn_us_east_1 : null
    ssl_support_method             = local.custom_domain_enabled ? "sni-only" : null
    minimum_protocol_version       = local.custom_domain_enabled ? "TLSv1.2_2021" : "TLSv1"
  }

  tags = {
    Name = "atpost-${var.environment}-media-cf"
  }
}

# Response-headers policy: HSTS + standard hardening on every cached
# response.
resource "aws_cloudfront_response_headers_policy" "media" {
  name    = "atpost-${var.environment}-media-headers"
  comment = "Security headers on media responses"

  security_headers_config {
    strict_transport_security {
      access_control_max_age_sec = 63072000 # 2 years
      include_subdomains         = true
      preload                    = true
      override                   = true
    }
    content_type_options {
      override = true
    }
    frame_options {
      frame_option = "DENY"
      override     = true
    }
    referrer_policy {
      referrer_policy = "strict-origin-when-cross-origin"
      override        = true
    }
  }

  cors_config {
    access_control_allow_credentials = false
    access_control_allow_headers {
      items = ["*"]
    }
    access_control_allow_methods {
      items = ["GET", "HEAD", "OPTIONS"]
    }
    access_control_allow_origins {
      items = var.cors_allowed_origins
    }
    access_control_max_age_sec = 3000
    origin_override            = true
  }
}

# Bucket policy: only CloudFront (via OAC) and the EKS-side service
# IRSA roles can touch the bucket. No public access.
data "aws_iam_policy_document" "media_bucket" {
  # CloudFront read via OAC.
  statement {
    sid     = "CloudFrontRead"
    effect  = "Allow"
    actions = ["s3:GetObject"]
    principals {
      type        = "Service"
      identifiers = ["cloudfront.amazonaws.com"]
    }
    resources = ["${aws_s3_bucket.media.arn}/*"]

    condition {
      test     = "StringEquals"
      variable = "AWS:SourceArn"
      values   = [aws_cloudfront_distribution.media.arn]
    }
  }

  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
    resources = [
      aws_s3_bucket.media.arn,
      "${aws_s3_bucket.media.arn}/*",
    ]
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "media" {
  bucket = aws_s3_bucket.media.id
  policy = data.aws_iam_policy_document.media_bucket.json

  depends_on = [aws_s3_bucket_public_access_block.media]
}

# Client IAM policy emitted for media-service and media-worker (uploads,
# listing, deletes, multipart). Attach to both IRSA roles.
data "aws_iam_policy_document" "media_client" {
  statement {
    sid    = "MediaBucketObjectOps"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${aws_s3_bucket.media.arn}/*"]
  }

  statement {
    sid    = "MediaBucketOps"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:GetBucketLocation",
      "s3:ListBucketMultipartUploads",
    ]
    resources = [aws_s3_bucket.media.arn]
  }

  statement {
    sid    = "MediaKMSUse"
    effect = "Allow"
    actions = [
      "kms:Encrypt",
      "kms:Decrypt",
      "kms:GenerateDataKey",
      "kms:DescribeKey",
    ]
    resources = [aws_kms_key.media.arn]
  }
}

resource "aws_iam_policy" "media_client" {
  name        = "atpost-${var.environment}-media-client"
  description = "Standard media-bucket client policy. Attach to the media-service and media-worker IRSA roles."
  policy      = data.aws_iam_policy_document.media_client.json
}
