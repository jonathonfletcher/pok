# Shared bhyve BGP / LB values — single source of truth for both the tf/k8s tfvars below
# and the OpenBSD gateway's bgpd.conf section (gateway.tf).
locals {
  lb_pool_prefix   = "172.23.20"     # /24 the gateway routes to the nodes; Cilium VIPs are .0/24
  bgp_local_asn    = 65010           # the k8s nodes' ASN (cluster side)
  bgp_peer_asn     = 65000           # the OpenBSD gateway's ASN
  bgp_peer_address = "172.23.10.100" # the gateway (BGP peer for the nodes)
}

# Emit the provider-specific inputs for the shared tf/k8s root (consumed there via
# -var-file=vars/bhyve.tfvars.json). Keeps bhyve values in tf/bhyve, not in tf/k8s.
resource "local_file" "k8s_tfvars" {
  filename        = "${path.module}/../k8s/vars/bhyve.tfvars.json"
  file_permission = "0644"
  content = jsonencode({
    cilium_devices     = "enp0s5"
    k8s_service_host   = "k8s.homelab.lan"
    lb_pool_prefix     = local.lb_pool_prefix
    bgp_local_asn      = local.bgp_local_asn
    bgp_peer_asn       = local.bgp_peer_asn
    bgp_peer_address   = local.bgp_peer_address
    bgp_cluster_name   = "bhyve"
    k8s_cluster_name   = "bhyve"
    bgp_peer_name      = "gateway"
    manage_registry    = true
    registry_node      = "k8sm1"
    registry_certs_dir = "../../registry/certs/bhyve"
  })
}
