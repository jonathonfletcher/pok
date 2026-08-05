resource "aws_vpc" "vpc" {
  cidr_block = var.cidr
  # Both are required for the Route 53 private zone (demo.internal, dns.tf) to resolve via
  # the VPC resolver — without enable_dns_hostnames the HA API name silently NXDOMAINs.
  enable_dns_support   = true
  enable_dns_hostnames = true
  tags = {
    Name = "${var.environment}-vpc"
  }
}

resource "aws_internet_gateway" "vpc" {
  tags = {
    Name = "${var.environment}-igw"
  }
}

resource "aws_route_table" "vpc" {
  vpc_id = aws_vpc.vpc.id

  tags = {
    Name = "${var.environment}-rt"
  }
}

resource "aws_route" "vpc" {
  route_table_id         = aws_route_table.vpc.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.vpc.id
}

resource "aws_internet_gateway_attachment" "vpc" {
  vpc_id              = aws_vpc.vpc.id
  internet_gateway_id = aws_internet_gateway.vpc.id
}
