# =============================================================================
# providers.tf  —  kubernetes + helm provider wiring
# =============================================================================
# DECLARES: provider "kubernetes", provider "helm"
# PURPOSE : both authenticate via the admin kubeconfig (var.kubeconfig).
# NOTES   : the kubeconfig MUST target k8s.homelab.lan:6443 (round-robins all 3
#           CPs, whose apiserver certs carry that SAN) so tofu survives losing any
#           one CP. helm provider v3 uses attribute syntax (kubernetes = {} object).
# DOCS    : tf/k8s/README.md
# =============================================================================

provider "kubernetes" {
  config_path = var.kubeconfig
}

# helm provider v3 uses attribute syntax (a `kubernetes = {}` object, not a block).
provider "helm" {
  kubernetes = {
    config_path = var.kubeconfig
  }
}
