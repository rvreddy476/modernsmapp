# 3-AZ VPC for AtPost.
#
# Tier layout per AZ:
#   public  /20  — ALB, NAT gateways, bastion (if needed)
#   private /20  — EKS nodes, app workloads
#   isolated /22 — RDS, ElastiCache, MSK (no internet route at all)
#
# /16 supernet split: 256 × /24 worth of room. With three /20s + three
# /22s per AZ and three AZs, this leaves plenty of headroom for VPC
# Peering / Transit Gateway expansion without renumbering.

data "aws_availability_zones" "available" {
  state = "available"
}

locals {
  azs = slice(data.aws_availability_zones.available.names, 0, 3)

  # /20s for public + private (4k addresses each); /22s for isolated.
  # Index by AZ position so adding a 4th AZ later doesn't reshuffle.
  public_subnets   = [for i in range(3) : cidrsubnet(var.vpc_cidr, 4, i)]
  private_subnets  = [for i in range(3) : cidrsubnet(var.vpc_cidr, 4, i + 3)]
  isolated_subnets = [for i in range(3) : cidrsubnet(var.vpc_cidr, 6, i + 24)]
}

resource "aws_vpc" "this" {
  cidr_block           = var.vpc_cidr
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "atpost-${var.environment}-vpc"
  }
}

resource "aws_internet_gateway" "this" {
  vpc_id = aws_vpc.this.id

  tags = {
    Name = "atpost-${var.environment}-igw"
  }
}

resource "aws_subnet" "public" {
  count                   = 3
  vpc_id                  = aws_vpc.this.id
  cidr_block              = local.public_subnets[count.index]
  availability_zone       = local.azs[count.index]
  map_public_ip_on_launch = true

  tags = {
    Name                     = "atpost-${var.environment}-public-${local.azs[count.index]}"
    Tier                     = "public"
    "kubernetes.io/role/elb" = "1"
  }
}

resource "aws_subnet" "private" {
  count             = 3
  vpc_id            = aws_vpc.this.id
  cidr_block        = local.private_subnets[count.index]
  availability_zone = local.azs[count.index]

  tags = {
    Name                              = "atpost-${var.environment}-private-${local.azs[count.index]}"
    Tier                              = "private"
    "kubernetes.io/role/internal-elb" = "1"
  }
}

resource "aws_subnet" "isolated" {
  count             = 3
  vpc_id            = aws_vpc.this.id
  cidr_block        = local.isolated_subnets[count.index]
  availability_zone = local.azs[count.index]

  tags = {
    Name = "atpost-${var.environment}-isolated-${local.azs[count.index]}"
    Tier = "isolated"
  }
}

# NAT gateways: one per AZ in prod (HA), one shared in staging (cost).
# Without the per-env split a single AZ outage takes down all private
# egress in prod, which would defeat the multi-AZ design.
resource "aws_eip" "nat" {
  count  = var.single_nat_gateway ? 1 : 3
  domain = "vpc"

  tags = {
    Name = "atpost-${var.environment}-nat-eip-${count.index}"
  }
}

resource "aws_nat_gateway" "this" {
  count         = var.single_nat_gateway ? 1 : 3
  allocation_id = aws_eip.nat[count.index].id
  subnet_id     = aws_subnet.public[count.index].id

  tags = {
    Name = "atpost-${var.environment}-nat-${count.index}"
  }

  depends_on = [aws_internet_gateway.this]
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.this.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.this.id
  }

  tags = {
    Name = "atpost-${var.environment}-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  count          = 3
  subnet_id      = aws_subnet.public[count.index].id
  route_table_id = aws_route_table.public.id
}

resource "aws_route_table" "private" {
  count  = 3
  vpc_id = aws_vpc.this.id

  route {
    cidr_block     = "0.0.0.0/0"
    nat_gateway_id = aws_nat_gateway.this[var.single_nat_gateway ? 0 : count.index].id
  }

  tags = {
    Name = "atpost-${var.environment}-private-rt-${local.azs[count.index]}"
  }
}

resource "aws_route_table_association" "private" {
  count          = 3
  subnet_id      = aws_subnet.private[count.index].id
  route_table_id = aws_route_table.private[count.index].id
}

# Isolated tier intentionally has NO route table beyond the local VPC
# route AWS adds automatically. RDS/ElastiCache/MSK live here and reach
# the world only via VPC endpoints (S3, ECR) or peer subnets.

# VPC endpoints for S3 + ECR. ECR pulls hit the internet otherwise,
# which (a) eats NAT bandwidth and (b) means container pull latency
# depends on the public ECR PoP. Endpoints fix both.
resource "aws_vpc_endpoint" "s3" {
  vpc_id            = aws_vpc.this.id
  service_name      = "com.amazonaws.${var.aws_region}.s3"
  vpc_endpoint_type = "Gateway"
  route_table_ids   = concat(aws_route_table.private[*].id, [aws_route_table.public.id])

  tags = {
    Name = "atpost-${var.environment}-vpce-s3"
  }
}

resource "aws_vpc_endpoint" "ecr_api" {
  vpc_id              = aws_vpc.this.id
  service_name        = "com.amazonaws.${var.aws_region}.ecr.api"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  private_dns_enabled = true
  security_group_ids  = [aws_security_group.vpc_endpoints.id]

  tags = {
    Name = "atpost-${var.environment}-vpce-ecr-api"
  }
}

resource "aws_vpc_endpoint" "ecr_dkr" {
  vpc_id              = aws_vpc.this.id
  service_name        = "com.amazonaws.${var.aws_region}.ecr.dkr"
  vpc_endpoint_type   = "Interface"
  subnet_ids          = aws_subnet.private[*].id
  private_dns_enabled = true
  security_group_ids  = [aws_security_group.vpc_endpoints.id]

  tags = {
    Name = "atpost-${var.environment}-vpce-ecr-dkr"
  }
}

resource "aws_security_group" "vpc_endpoints" {
  name        = "atpost-${var.environment}-vpce-sg"
  description = "Allow HTTPS from inside the VPC to interface endpoints"
  vpc_id      = aws_vpc.this.id

  ingress {
    description = "HTTPS from inside the VPC"
    from_port   = 443
    to_port     = 443
    protocol    = "tcp"
    cidr_blocks = [var.vpc_cidr]
  }

  egress {
    description = "Endpoint replies"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "atpost-${var.environment}-vpce-sg"
  }
}
