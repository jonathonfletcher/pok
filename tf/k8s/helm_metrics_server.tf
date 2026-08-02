# =============================================================================
# helm_metrics_server.tf  —  Kubernetes Metrics API (metrics.k8s.io)
# =============================================================================
# DECLARES: helm_release.metrics_server (metrics-server, ns kube-system)
# PURPOSE : serves the resource Metrics API (node/pod CPU + memory usage) that
#           backs `kubectl top` and the clusterinfo node-metrics/pod-metrics aspects.
# NOTES   : kubeadm kubelets use self-signed serving certs, so --kubelet-insecure-tls;
#           nodes are addressed by InternalIP. Provider-neutral (both bhyve + aws).
# DOCS    : tf/k8s/README.md
# =============================================================================
resource "helm_release" "metrics_server" {
  name       = "metrics-server"
  namespace  = "kube-system"
  repository = "https://kubernetes-sigs.github.io/metrics-server/"
  chart      = "metrics-server"
  version    = "3.12.2"
  values = [yamlencode({
    args = [
      "--kubelet-insecure-tls",
      "--kubelet-preferred-address-types=InternalIP",
    ]
    resources = {
      requests = { cpu = "25m", memory = "64Mi" }
      limits   = { memory = "128Mi" }
    }
  })]
}
