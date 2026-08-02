# Namespaces for tofu-managed workloads that nothing else creates.
#   - registry        -> created in registry.tf
#   - topolvm-system  -> created via create_namespace on the topolvm release
#   - honeycomb       -> created HERE: it holds the two OTel collector releases,
#                        their credential Secret, and the otel-agent Service, none
#                        of which create it. On a fresh cluster its absence made
#                        every honeycomb resource fail.
#
# IMPORT-FIRST (adopting the already-running cluster): import before the first plan
#   tofu import kubernetes_namespace_v1.honeycomb honeycomb
resource "kubernetes_namespace_v1" "honeycomb" {
  metadata {
    name = "honeycomb"
  }
  # Deleting this namespace would take the collectors + credential with it.
  lifecycle {
    prevent_destroy = true
  }
}

# kafka -> holds the Strimzi operator (helm_kafka.tf) and the KRaft cluster CRs
# (kafka-cluster.yaml, kubectl-applied by `make services`). No prevent_destroy yet —
# still being iterated on.
resource "kubernetes_namespace_v1" "kafka" {
  metadata {
    name = "kafka"
  }
}
