# Node-local OTLP endpoint for the OpenTelemetry agent DaemonSet.
# internalTrafficPolicy: Local keeps each app's spans on the SAME node's agent
# (hostPort is not wired on this cluster). Selects the agent-collector pods.
# Previously applied by hand (hw/otel-agent-svc.yaml); now managed here.
#
# Typed resource (not kubernetes_manifest) — a core Service has a first-class
# resource, and unlike a manifest it doesn't need a plan-time server dry-run, so a
# fresh apply can create it in the same pass once the honeycomb namespace exists.
#
# Import: tofu import kubernetes_service_v1.otel_agent honeycomb/otel-agent
resource "kubernetes_service_v1" "otel_agent" {
  metadata {
    name      = "otel-agent"
    namespace = kubernetes_namespace_v1.honeycomb.metadata[0].name
    labels = {
      "app.kubernetes.io/name" = "opentelemetry-collector"
      "component"              = "agent-collector"
    }
  }
  spec {
    type                    = "ClusterIP"
    internal_traffic_policy = "Local"
    selector = {
      "app.kubernetes.io/instance" = "otel-collector"
      "component"                  = "agent-collector"
    }
    port {
      name        = "otlp"
      port        = 4317
      target_port = 4317
      protocol    = "TCP"
    }
    port {
      name        = "otlp-http"
      port        = 4318
      target_port = 4318
      protocol    = "TCP"
    }
  }
}
