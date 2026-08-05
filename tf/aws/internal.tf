# Internal (private) node subnet + the two k8s node pools.
#
# All cluster nodes share one subnet (like the bhyve LAN): intra-subnet traffic is
# allowed wide-open (ip_protocol -1), which covers every inter-node k8s port
# (etcd, kubelet, Cilium vxlan/health, NodePorts, ...). From the border we open only
# 22 (proxyjump), 80/443/8080 (the reverse-proxied app), and 6443 (kube API).
#
# Nodes get an auto-assigned public IP for outbound (apt, image pulls) via the IGW —
# no NAT gateway (saves ~$32/mo). Inbound from the internet is still blocked: the SG
# admits traffic only from the border and internal subnets, never 0.0.0.0/0.

locals {
  # Ports the border instance may open to the nodes. 80/443/8080 are the Cilium LB
  # path (the LB VIPs live on worker node ENIs, which carry this SG, so allowing the
  # border here IS "border -> Cilium LB"). 22 = ProxyJump, 6443 = kube API.
  internal_ingress_rules = [
    { port = 22, protocol = "tcp" },   # SSH ProxyJump
    { port = 80, protocol = "tcp" },   # hello-world via Cilium LB
    { port = 443, protocol = "tcp" },  # hello-world TLS via Cilium LB
    { port = 8080, protocol = "tcp" }, # hello-world alt via Cilium LB
    { port = 6443, protocol = "tcp" }, # kube-apiserver
    { port = 179, protocol = "tcp" },  # BGP (Cilium <-> FRR on the border)
  ]
}

resource "aws_subnet" "internal" {
  availability_zone       = var.az
  vpc_id                  = aws_vpc.vpc.id
  map_public_ip_on_launch = true
  cidr_block              = cidrsubnet(aws_vpc.vpc.cidr_block, 8, 10)

  tags = {
    Name = "${var.environment}-internal-subnet"
  }
  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_route_table_association" "internal" {
  subnet_id      = aws_subnet.internal.id
  route_table_id = aws_route_table.vpc.id
}

resource "aws_security_group" "internal" {
  description = "${var.environment}-internal-sg"
  vpc_id      = aws_vpc.vpc.id
  tags = {
    Name = "${var.environment}-internal-sg"
  }
}

# Border -> nodes (incl. the Cilium LB VIPs, which sit on the worker ENIs governed
# by this SG). SG-to-SG reference, so it holds regardless of which node hosts a VIP.
resource "aws_vpc_security_group_ingress_rule" "border_internal_ingress_ipv4" {
  count                        = length(local.internal_ingress_rules)
  security_group_id            = aws_security_group.internal.id
  referenced_security_group_id = aws_security_group.border.id
  ip_protocol                  = local.internal_ingress_rules[count.index].protocol
  to_port                      = local.internal_ingress_rules[count.index].port
  from_port                    = local.internal_ingress_rules[count.index].port
}

resource "aws_vpc_security_group_ingress_rule" "internal_ingress_ipv4" {
  security_group_id = aws_security_group.internal.id
  cidr_ipv4         = aws_subnet.internal.cidr_block
  ip_protocol       = -1
}

resource "aws_vpc_security_group_egress_rule" "internal_egress_ipv4" {
  security_group_id = aws_security_group.internal.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = -1
}

# ---- control-plane pool (private IPs 10.0.10.101+) -----------------------------
module "control_plane" {
  count                = var.control_plane_count
  source               = "./vm/"
  az                   = var.az
  subnet               = aws_subnet.internal
  security_groups      = [aws_security_group.internal]
  data_volume_size     = var.control_plane_datadisk_size
  ssh_key              = aws_key_pair.vm
  public_ip_address    = false
  environment          = var.environment
  kind                 = "control-plane"
  instance_number      = count.index
  instance_base_number = 100
  instance_type        = var.control_plane_instance_type
  source_dest_check    = false # accept Cilium LB VIP-dst traffic routed here via BGP
  user_data            = null
}

# ---- worker pool (private IPs 10.0.10.111+) ------------------------------------
module "worker" {
  count                = var.worker_count
  source               = "./vm/"
  az                   = var.az
  subnet               = aws_subnet.internal
  security_groups      = [aws_security_group.internal]
  data_volume_size     = var.worker_datadisk_size
  ssh_key              = aws_key_pair.vm
  public_ip_address    = false
  environment          = var.environment
  kind                 = "worker"
  instance_number      = count.index
  instance_base_number = 110
  instance_type        = var.worker_instance_type
  source_dest_check    = false # accept Cilium LB VIP-dst traffic routed here via BGP
  user_data            = null
}
