# Render the k8s-cilium peering section for the OpenBSD gateway's bgpd.conf. The gateway is
# external infra (the home-lab router — not tofu/ansible-managed), so tofu can't push to it;
# it emits the config fragment for the operator to append. This is the bhyve analogue of the
# AWS border's FRR config (emitted inline in tf/aws border-cloud-init). Node IPs come from the
# same inventory locals that produce the Ansible inventory, so the peer list can't drift.
resource "local_file" "gateway_bgpd" {
  filename        = "${path.module}/rendered/bgpd-cilium.conf"
  file_permission = "0644"
  content = templatefile("${path.module}/templates/bgpd-cilium.conf.tftpl", {
    cluster_asn    = local.bgp_local_asn
    peer_asn       = local.bgp_peer_asn
    lb_pool_prefix = local.lb_pool_prefix
    nodes          = merge(local.primary_cp, local.secondary_cp, local.workers)
  })
}

output "gateway_bgpd_path" {
  value = local_file.gateway_bgpd.filename
}
