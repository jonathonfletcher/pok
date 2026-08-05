locals {
  # Always-open border ingress (the public web front door). SSH (22) is separate + toggleable —
  # see aws_vpc_security_group_ingress_rule.border_ssh.
  border_ingress_rules = [
    {
      port     = 80,
      protocol = "tcp"
    },
    {
      port     = 443,
      protocol = "tcp"
    },
  ]
}

resource "aws_subnet" "border" {
  availability_zone       = var.az
  vpc_id                  = aws_vpc.vpc.id
  map_public_ip_on_launch = true
  cidr_block              = cidrsubnet(aws_vpc.vpc.cidr_block, 8, 100)
  tags = {
    Name = "${var.environment}-border-subnet"
  }
  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_route_table_association" "border" {
  subnet_id      = aws_subnet.border.id
  route_table_id = aws_route_table.vpc.id
}

resource "aws_security_group" "border" {
  name   = "${var.environment}-border-sg"
  vpc_id = aws_vpc.vpc.id
  tags = {
    Name = "${var.environment}-border-sg"
  }
}

resource "aws_vpc_security_group_ingress_rule" "border_ingress_ipv4" {
  count             = length(local.border_ingress_rules)
  security_group_id = aws_security_group.border.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = local.border_ingress_rules[count.index].protocol
  to_port           = local.border_ingress_rules[count.index].port
  from_port         = local.border_ingress_rules[count.index].port
}

# SSH (22/tcp) from the internet — TOGGLEABLE to limit exposure. Open for setup/management,
# closed during normal operation. Flip with `make border-ssh-on|off` (var.border_allow_ssh);
# default open so the initial cluster build + SSH ProxyJump to the nodes work.
resource "aws_vpc_security_group_ingress_rule" "border_ssh" {
  count             = var.border_allow_ssh ? 1 : 0
  security_group_id = aws_security_group.border.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "tcp"
  from_port         = 22
  to_port           = 22
}

resource "aws_vpc_security_group_egress_rule" "border_egress_ipv4" {
  security_group_id = aws_security_group.border.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"
}

# BGP: the Cilium nodes initiate to FRR on the border (TCP/179). Allow it from the
# internal SG (single-hop eBGP — AWS doesn't decrement TTL cross-subnet, verified).
resource "aws_vpc_security_group_ingress_rule" "border_bgp_from_internal" {
  security_group_id            = aws_security_group.border.id
  referenced_security_group_id = aws_security_group.internal.id
  ip_protocol                  = "tcp"
  from_port                    = 179
  to_port                      = 179
}

data "aws_region" "current" {}

module "border" {
  source            = "./vm/"
  az                = var.az
  subnet            = aws_subnet.border
  security_groups   = [aws_security_group.border]
  ssh_key           = aws_key_pair.vm
  public_ip_address = true
  environment       = var.environment
  kind              = "border"
  instance_number   = 0
  instance_type     = var.border_instance_type
  # Keep the border's public IP across rebuilds so pok.somegroup.net stays valid.
  eip_prevent_destroy = true
  # LB-failover agent needs ec2:ReplaceRoute (IMDS creds); attaching is in-place.
  iam_instance_profile = aws_iam_instance_profile.border.name
  # Don't recreate the border on cloud-init edits (see vm/variables.tf) — the EIP-bearing
  # border is too precious to destroy on a template change; changes land on next rebuild.
  user_data_replace_on_change = false
  user_data = templatefile("${path.module}/templates/border-cloud-init.yaml.tftpl", {
    proxy_hostname = var.border_proxy_hostname
    proxy_upstream = var.border_proxy_upstream
    # FRR/BGP: border is AS 65001, peers Cilium nodes (AS 65020) that connect from
    # the internal subnet. Single-hop eBGP (no multihop — TTL is preserved in-VPC).
    border_asn       = 65001
    cluster_asn      = 65020
    node_subnet_cidr = aws_subnet.internal.cidr_block
    # LB-failover agent (docs/README.md §5.2): repoints the Cilium-LB route to a live node.
    lb_route_table_id = aws_route_table.vpc.id
    lb_cidr           = aws_subnet.cilium_lb.cidr_block
    aws_region        = data.aws_region.current.region
    vpc_id            = aws_vpc.vpc.id
    agent_script      = file("${path.module}/border/lb-failover.sh")
    agent_unit        = file("${path.module}/border/lb-failover.service")
  })
}
