# TopoLVM — CSI driver providing dynamic local PVs from each node's topolvm-vg
# (a dedicated VG backed by ubuntu-data-lv; created by the Ansible node_topolvm_vg role).
# Scheduling uses CSI Storage Capacity Tracking (no webhook / no cert-manager) —
# see docs/CHANGELOG.md §N.
resource "helm_release" "topolvm" {
  name             = "topolvm"
  namespace        = "topolvm-system"
  create_namespace = true
  repository       = "https://topolvm.github.io/topolvm"
  chart            = "topolvm"
  version          = "17.0.0"
  values           = [file("${path.module}/values/topolvm.values.yaml")]

  wait    = true
  timeout = 600
}
