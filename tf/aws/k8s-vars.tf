# Emit the provider-specific inputs for the shared tf/k8s root (consumed there via
# -var-file=vars/aws.tfvars.json). Keeps aws values in tf/aws, not in tf/k8s.
resource "local_file" "k8s_tfvars" {
  filename        = "${path.module}/../k8s/vars/aws.tfvars.json"
  file_permission = "0644"
  content = jsonencode({
    cilium_devices     = "ens5"                                                              # Nitro/arm64 primary NIC
    k8s_service_host   = "k8s.${var.internal_domain}"                                        # HA round-robin over all CPs (Route 53 private zone)
    lb_pool_prefix     = join(".", slice(split(".", aws_subnet.cilium_lb.cidr_block), 0, 3)) # first 3 octets of the reserved cilium-lb subnet (derived, not hardcoded)
    bgp_local_asn      = 65020
    bgp_peer_asn       = 65001
    bgp_peer_address   = module.border.private_ip # FRR on the border
    bgp_cluster_name   = "aws"
    k8s_cluster_name   = "aws"
    bgp_peer_name      = "border"
    manage_registry    = true  # self-hosted on cp1 (no new AWS resource — no ECR/IAM)
    registry_node      = "cp1" # kubeadm node name of the primary control plane
    registry_certs_dir = "../../registry/certs/aws"
  })
}
