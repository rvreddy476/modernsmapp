output "vpc_id" {
  value = aws_vpc.this.id
}

output "vpc_cidr" {
  value = aws_vpc.this.cidr_block
}

output "public_subnet_ids" {
  value = aws_subnet.public[*].id
}

output "private_subnet_ids" {
  value       = aws_subnet.private[*].id
  description = "EKS nodes + app workloads live here."
}

output "isolated_subnet_ids" {
  value       = aws_subnet.isolated[*].id
  description = "RDS / ElastiCache / MSK live here. No internet route."
}

output "availability_zones" {
  value = local.azs
}
