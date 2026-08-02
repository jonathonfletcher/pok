# Route 53 PRIVATE hosted zone for in-VPC DNS + an HA Kubernetes API endpoint.
#
# The VPC resolver answers these names for anything INSIDE the VPC (nodes, border); they
# do not resolve from outside (the build host reaches the API via the SSH tunnel). This is
# the AWS analog of bhyve's homelab.lan zone on the OpenBSD gateway.
#
# k8s.<domain> is a round-robin A record over ALL control-plane IPs — the analog of
# bhyve's k8s.homelab.lan. It is the kube control_plane_endpoint, so every kubelet /
# controller resolves the API to one of the live CPs instead of the old single cp1 IP
# (which was a SPOF that defeated the 3-control-plane HA). Baked in at kubeadm init.

resource "aws_route53_zone" "internal" {
  name = var.internal_domain
  vpc {
    vpc_id = aws_vpc.vpc.id
  }
  tags = { environment = var.environment }
}

# Per-node A records (names match the kubeadm node names: cp1..cpN, w1..wN, border).
resource "aws_route53_record" "control_plane" {
  count   = var.control_plane_count
  zone_id = aws_route53_zone.internal.zone_id
  name    = "cp${count.index + 1}.${var.internal_domain}"
  type    = "A"
  ttl     = 60
  records = [module.control_plane[count.index].private_ip]
}

resource "aws_route53_record" "worker" {
  count   = var.worker_count
  zone_id = aws_route53_zone.internal.zone_id
  name    = "w${count.index + 1}.${var.internal_domain}"
  type    = "A"
  ttl     = 60
  records = [module.worker[count.index].private_ip]
}

resource "aws_route53_record" "border" {
  zone_id = aws_route53_zone.internal.zone_id
  name    = "border.${var.internal_domain}"
  type    = "A"
  ttl     = 60
  records = [module.border.private_ip]
}

# HA API endpoint — round-robin over every control plane (bhyve k8s.homelab.lan parity).
resource "aws_route53_record" "k8s_api" {
  zone_id = aws_route53_zone.internal.zone_id
  name    = "k8s.${var.internal_domain}"
  type    = "A"
  ttl     = 30
  records = module.control_plane[*].private_ip
}
