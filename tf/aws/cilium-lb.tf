# Dedicated subnet reserved for the Cilium LoadBalancer IP pool.
#
# WHY a subnet: an AWS VPC is pure L3 — there is no broadcast domain, so Cilium's
# on-prem L2/ARP announcement mode cannot hand out LB VIPs here. Instead the LB IPs
# must be real VPC addresses drawn from a subnet CIDR (an L3 LB). This carves that
# range out so nothing else (nodes, border) allocates from it.
#
# SCOPE: this is the network reservation only. Making a given LB IP reachable is a
# CLUSTER-layer task (settled when Cilium is installed on the AWS nodes), via one of:
#   - Cilium BGP control-plane advertising the VIPs, or
#   - assigning the VIP as an ENI secondary IP on the elected node, or
#   - a VPC route-table /32 pointing at the node's ENI.
# We don't build that here — tf/aws is infrastructure only.
#
# The border's Caddy upstream (var.border_proxy_upstream) points at an address in
# this range once the cluster + Cilium LB exist (e.g. 10.0.20.240:80).

resource "aws_subnet" "cilium_lb" {
  availability_zone = var.az
  vpc_id            = aws_vpc.vpc.id
  cidr_block        = cidrsubnet(aws_vpc.vpc.cidr_block, 8, 20)

  tags = {
    Name = "${var.environment}-cilium-lb-subnet"
  }
  lifecycle {
    ignore_changes = [tags]
  }
}

resource "aws_route_table_association" "cilium_lb" {
  subnet_id      = aws_subnet.cilium_lb.id
  route_table_id = aws_route_table.vpc.id
}

# The final hop AWS needs to make the LB pool reachable. The cilium_lb range owns no
# ENIs, so the VPC's implicit local route can't deliver dst=10.0.20.x to anything.
# This /24 (more specific than the local route) sends that traffic to cp1's ENI, where
# Cilium's eBPF LB DNATs it to the backing pod. Works because the nodes already run
# with source_dest_check=false and the border->node SG already opens 80/443/8080
# (both in internal.tf). This is the AWS analog of "border can reach the internal
# subnet": internal has real ENIs so the local route suffices; cilium_lb needs this.
resource "aws_route" "cilium_lb" {
  route_table_id         = aws_route_table.vpc.id
  destination_cidr_block = aws_subnet.cilium_lb.cidr_block
  # Initial/standby target = cp1. The border's lb-failover agent (border/lb-failover.sh)
  # rewrites this target at runtime as nodes come and go, so tofu must NOT revert it —
  # otherwise every apply fights the agent. tofu owns the route's existence; the agent
  # owns which live node it points at.
  network_interface_id = module.control_plane[0].primary_network_interface_id
  lifecycle {
    ignore_changes = [network_interface_id]
  }
}
