# Private S3 bucket with a client IAM policy.
#
# Used for the non-media buckets the services write through their IRSA
# role: live recordings (live-service-v2 + LiveKit egress), commerce
# invoices (commerce-service) and food files (food-service). Public access
# is blocked, objects are encrypted with SSE-S3 by default or SSE-KMS when
# a key ARN is passed, incomplete multipart uploads are aborted, and the
# optional lifecycle knobs keep storage cost bounded.
#
# The bucket name carries a random suffix so environments never collide on
# the global S3 namespace; values files take the name from the env output,
# never a hard-coded string.

resource "random_id" "suffix" {
  byte_length = 4
}

resource "aws_s3_bucket" "this" {
  bucket = "atpost-${var.environment}-${var.purpose}-${random_id.suffix.hex}"

  tags = {
    Name    = "atpost-${var.environment}-${var.purpose}"
    Service = var.service_tag
  }
}

resource "aws_s3_bucket_public_access_block" "this" {
  bucket                  = aws_s3_bucket.this.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_ownership_controls" "this" {
  bucket = aws_s3_bucket.this.id
  rule {
    object_ownership = "BucketOwnerEnforced"
  }
}

resource "aws_s3_bucket_versioning" "this" {
  bucket = aws_s3_bucket.this.id
  versioning_configuration {
    status = var.versioning ? "Enabled" : "Suspended"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "this" {
  bucket = aws_s3_bucket.this.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = var.kms_key_arn == null ? "AES256" : "aws:kms"
      kms_master_key_id = var.kms_key_arn
    }
    bucket_key_enabled = var.kms_key_arn != null
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "this" {
  bucket = aws_s3_bucket.this.id

  rule {
    id     = "abort-incomplete-multipart"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }

  dynamic "rule" {
    for_each = var.versioning ? [1] : []
    content {
      id     = "noncurrent-versions"
      status = "Enabled"
      filter {}
      noncurrent_version_expiration {
        noncurrent_days = var.noncurrent_version_expiration_days
      }
    }
  }

  dynamic "rule" {
    for_each = var.transition_to_ia_after_days == null ? [] : [1]
    content {
      id     = "cold-objects-to-ia"
      status = "Enabled"
      filter {}
      transition {
        days          = var.transition_to_ia_after_days
        storage_class = "STANDARD_IA"
      }
    }
  }

  dynamic "rule" {
    for_each = var.expire_after_days == null ? [] : [1]
    content {
      id     = "expire-objects"
      status = "Enabled"
      filter {}
      expiration {
        days = var.expire_after_days
      }
    }
  }

  depends_on = [aws_s3_bucket_versioning.this]
}

# TLS only.
data "aws_iam_policy_document" "bucket" {
  statement {
    sid     = "DenyInsecureTransport"
    effect  = "Deny"
    actions = ["s3:*"]
    principals {
      type        = "AWS"
      identifiers = ["*"]
    }
    resources = [
      aws_s3_bucket.this.arn,
      "${aws_s3_bucket.this.arn}/*",
    ]
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "this" {
  bucket = aws_s3_bucket.this.id
  policy = data.aws_iam_policy_document.bucket.json

  depends_on = [aws_s3_bucket_public_access_block.this]
}

# Client policy: what a service that owns this bucket needs. ListBucket
# covers the minio-go BucketExists probe the services run at boot.
data "aws_iam_policy_document" "client" {
  statement {
    sid    = "BucketObjectOps"
    effect = "Allow"
    actions = [
      "s3:GetObject",
      "s3:GetObjectVersion",
      "s3:PutObject",
      "s3:DeleteObject",
      "s3:AbortMultipartUpload",
      "s3:ListMultipartUploadParts",
    ]
    resources = ["${aws_s3_bucket.this.arn}/*"]
  }

  statement {
    sid    = "BucketOps"
    effect = "Allow"
    actions = [
      "s3:ListBucket",
      "s3:GetBucketLocation",
      "s3:ListBucketMultipartUploads",
    ]
    resources = [aws_s3_bucket.this.arn]
  }

  dynamic "statement" {
    for_each = var.kms_key_arn == null ? [] : [1]
    content {
      sid    = "KMSUse"
      effect = "Allow"
      actions = [
        "kms:Encrypt",
        "kms:Decrypt",
        "kms:GenerateDataKey",
        "kms:DescribeKey",
      ]
      resources = [var.kms_key_arn]
    }
  }
}

resource "aws_iam_policy" "client" {
  name        = "atpost-${var.environment}-${var.purpose}-client"
  description = "Read/write access to the atpost-${var.environment}-${var.purpose} bucket. Attach to the owning service's IRSA role."
  policy      = data.aws_iam_policy_document.client.json
}

# Read-only policy for a service that imports from this bucket but never
# writes it (media-service turning live recordings into videos).
data "aws_iam_policy_document" "reader" {
  statement {
    sid       = "BucketRead"
    effect    = "Allow"
    actions   = ["s3:GetObject", "s3:GetObjectVersion"]
    resources = ["${aws_s3_bucket.this.arn}/*"]
  }

  statement {
    sid       = "BucketList"
    effect    = "Allow"
    actions   = ["s3:ListBucket", "s3:GetBucketLocation"]
    resources = [aws_s3_bucket.this.arn]
  }

  dynamic "statement" {
    for_each = var.kms_key_arn == null ? [] : [1]
    content {
      sid       = "KMSDecrypt"
      effect    = "Allow"
      actions   = ["kms:Decrypt", "kms:DescribeKey"]
      resources = [var.kms_key_arn]
    }
  }
}

resource "aws_iam_policy" "reader" {
  name        = "atpost-${var.environment}-${var.purpose}-reader"
  description = "Read-only access to the atpost-${var.environment}-${var.purpose} bucket."
  policy      = data.aws_iam_policy_document.reader.json
}
