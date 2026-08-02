# Emit the provider-specific inputs for the shared tf/k8s root (consumed there via
# -var-file=vars/bhyve.tfvars.json). Keeps bhyve values in tf/bhyve, not in tf/k8s.
resource "local_file" "k8s_tfvars" {
  filename        = "${path.module}/../k8s/vars/bhyve.tfvars.json"
  file_permission = "0644"
  content = jsonencode({
    cilium_devices     = "enp0s5"
    k8s_service_host   = "k8s.homelab.lan"
    lb_pool_prefix     = "172.23.20"
    bgp_local_asn      = 65010
    bgp_peer_asn       = 65000
    bgp_peer_address   = "172.23.10.100"
    bgp_cluster_name   = "bhyve"
    k8s_cluster_name   = "bhyve"
    bgp_peer_name      = "gateway"
    manage_registry    = true
    registry_node      = "k8sm1"
    registry_certs_dir = "../../registry/certs/bhyve"
  })
}
