# Private npm registry for the web monorepo's shared packages (@atpost/*), plus
# a cached proxy of public npm so CI installs are auditable/repeatable. CI
# authenticates with `aws codeartifact login --tool npm` using the GitHub-OIDC
# CI role (the read/publish policy below is attached to it in the env).

data "aws_caller_identity" "current" {}

resource "aws_codeartifact_domain" "this" {
  domain = "atpost"

  tags = {
    Name = "atpost-${var.environment}"
  }
}

resource "aws_codeartifact_repository" "web" {
  domain      = aws_codeartifact_domain.this.domain
  repository  = "atpost-web"
  description = "atPost web monorepo packages (@atpost/*) + public npm proxy"

  # Proxy + cache public npm through this repo so the whole install is one source.
  external_connections {
    external_connection_name = "public:npmjs"
  }

  tags = {
    Name = "atpost-${var.environment}-web"
  }
}

data "aws_codeartifact_repository_endpoint" "web_npm" {
  domain     = aws_codeartifact_domain.this.domain
  repository = aws_codeartifact_repository.web.repository
  format     = "npm"
}

# Read + publish + auth-token. Attached to the CI role in the env composition.
data "aws_iam_policy_document" "rw" {
  statement {
    sid       = "GetAuthToken"
    effect    = "Allow"
    actions   = ["codeartifact:GetAuthorizationToken"]
    resources = [aws_codeartifact_domain.this.arn]
  }

  statement {
    sid       = "StsServiceBearer"
    effect    = "Allow"
    actions   = ["sts:GetServiceBearerToken"]
    resources = ["*"]
    condition {
      test     = "StringEquals"
      variable = "sts:AWSServiceName"
      values   = ["codeartifact.amazonaws.com"]
    }
  }

  statement {
    sid       = "RepoReadEndpoint"
    effect    = "Allow"
    actions   = ["codeartifact:GetRepositoryEndpoint", "codeartifact:ReadFromRepository"]
    resources = [aws_codeartifact_repository.web.arn]
  }

  statement {
    sid    = "PublishPackages"
    effect = "Allow"
    actions = [
      "codeartifact:PublishPackageVersion",
      "codeartifact:PutPackageMetadata",
    ]
    resources = [
      "arn:aws:codeartifact:${var.aws_region}:${data.aws_caller_identity.current.account_id}:package/${aws_codeartifact_domain.this.domain}/${aws_codeartifact_repository.web.repository}/*",
    ]
  }
}

resource "aws_iam_policy" "rw" {
  name   = "atpost-${var.environment}-codeartifact-rw"
  policy = data.aws_iam_policy_document.rw.json

  tags = {
    Name = "atpost-${var.environment}-codeartifact-rw"
  }
}
