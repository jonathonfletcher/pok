# Render the kubectl-applied Cilium CRs from provider variables. The Makefile's
# `apply` runs `tofu apply` (which writes these) THEN `kubectl apply -f`s them, so the
# rendered files exist in time. Generated + gitignored — edit templates/, not the output.
# (kubectl apply is object-based, so comment changes here never churn the cluster.)
resource "local_file" "lb_pool" {
  filename        = "${path.module}/lb-pool.yaml"
  file_permission = "0644"
  content = templatefile("${path.module}/templates/lb-pool.yaml.tftpl", {
    lb_pool_prefix = var.lb_pool_prefix
  })
}

resource "local_file" "bgp" {
  filename        = "${path.module}/bgp.yaml"
  file_permission = "0644"
  content = templatefile("${path.module}/templates/bgp.yaml.tftpl", {
    lb_pool_prefix   = var.lb_pool_prefix
    bgp_local_asn    = var.bgp_local_asn
    bgp_peer_asn     = var.bgp_peer_asn
    bgp_peer_address = var.bgp_peer_address
    bgp_cluster_name = var.bgp_cluster_name
    bgp_peer_name    = var.bgp_peer_name
  })
}
