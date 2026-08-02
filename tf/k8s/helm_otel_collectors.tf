# =============================================================================
# helm_otel_collectors.tf  —  OpenTelemetry collectors -> Honeycomb
# =============================================================================
# DECLARES: helm_release.otel_collector_agent   (DaemonSet, ns honeycomb)
#           helm_release.otel_collector_cluster (Deployment, ns honeycomb)
# PURPOSE : two OTel collector releases (chart 0.165.0); values are verbatim
#           `helm get values` in values/otel-collector*.values.yaml.
# NEEDS   : namespaces.tf (honeycomb ns) + secret.tf (honeycomb API-key Secret,
#           read via secretKeyRef). Both are referenced so tofu orders them first.
# IMPORT  : tofu import helm_release.otel_collector_agent   honeycomb/otel-collector
#           tofu import helm_release.otel_collector_cluster honeycomb/otel-collector-cluster
# DOCS    : tf/k8s/README.md
# =============================================================================
resource "helm_release" "otel_collector_agent" {
  name       = "otel-collector"
  namespace  = kubernetes_namespace_v1.honeycomb.metadata[0].name
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = "0.165.0"
  values     = [file("${path.module}/values/otel-collector.values.yaml")]

  atomic          = false
  cleanup_on_fail = false
}

resource "helm_release" "otel_collector_cluster" {
  name       = "otel-collector-cluster"
  namespace  = kubernetes_namespace_v1.honeycomb.metadata[0].name
  repository = "https://open-telemetry.github.io/opentelemetry-helm-charts"
  chart      = "opentelemetry-collector"
  version    = "0.165.0"
  values     = [file("${path.module}/values/otel-collector-cluster.values.yaml")]

  atomic          = false
  cleanup_on_fail = false
}
