# One private ECR repo per service. Image tag immutability is on so a
# tagged image can't be silently replaced — required for the GitOps
# deploy story (ArgoCD pins by tag; mutable tags = silent drift).
#
# CRITICAL/HIGH CVE scanning on push gives Phase-1 (C5) a real gate.
# We don't yet block deploy on CVE results — that's Phase 2 once we've
# tuned the noise from the false-positive rate.

resource "aws_ecr_repository" "this" {
  for_each = toset(var.repositories)

  name                 = "atpost/${each.key}"
  image_tag_mutability = "IMMUTABLE"

  image_scanning_configuration {
    scan_on_push = true
  }

  encryption_configuration {
    encryption_type = "AES256"
  }

  tags = {
    Name    = "atpost-${var.environment}-ecr-${each.key}"
    Service = each.key
  }
}

# Lifecycle: keep the last 50 tagged images + expire untagged after 7 days.
# Without this, ECR storage cost grows unboundedly — CI pushes a new tag
# per commit, so a busy month is hundreds of images per repo.
resource "aws_ecr_lifecycle_policy" "this" {
  for_each   = aws_ecr_repository.this
  repository = each.value.name

  policy = jsonencode({
    rules = [
      {
        rulePriority = 1
        description  = "Keep last 50 tagged images"
        selection = {
          tagStatus      = "tagged"
          tagPatternList = ["*"]
          countType      = "imageCountMoreThan"
          countNumber    = 50
        }
        action = { type = "expire" }
      },
      {
        rulePriority = 2
        description  = "Expire untagged after 7 days"
        selection = {
          tagStatus   = "untagged"
          countType   = "sinceImagePushed"
          countUnit   = "days"
          countNumber = 7
        }
        action = { type = "expire" }
      },
    ]
  })
}
