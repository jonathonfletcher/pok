# =============================================================================
# helm_cilium.tf  —  Cilium CNI (the running dataplane)
# =============================================================================
# DECLARES: helm_release.cilium (1.19.6, ns kube-system)
# PURPOSE : the CNI — KPR (no kube-proxy), BGP control-plane (L3 LB). Values render from
#           values/cilium.values.yaml.tftpl via templatefile() (${cilium_devices} +
#           ${k8s_service_host} per provider); kept minimal-diff to avoid a CNI re-render.
# NOTES   : LOAD-BEARING. prevent_destroy + no auto-rollback/reinstall; a version
#           bump must be deliberate and drained. The LB pool/policy are NOT here —
#           they're CRDs applied by kubectl (see lb-pool.yaml / Makefile).
# IMPORT  : tofu import helm_release.cilium kube-system/cilium
# DOCS    : tf/k8s/README.md
# =============================================================================
resource "helm_release" "cilium" {
  name       = "cilium"
  namespace  = "kube-system"
  repository = "https://helm.cilium.io"
  chart      = "cilium"
  version    = "1.19.6"
  values = [templatefile("${path.module}/values/cilium.values.yaml.tftpl", {
    cilium_devices   = var.cilium_devices
    k8s_service_host = var.k8s_service_host
  })]

  # Safety rails for a load-bearing CNI: never silently roll back or reinstall.
  atomic          = false
  cleanup_on_fail = false
  force_update    = false
  recreate_pods   = false
  # Cilium upgrades need care; require an explicit version bump, not a drift-driven one.
  lifecycle {
    prevent_destroy = true
  }
}
