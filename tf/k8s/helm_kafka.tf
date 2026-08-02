# =============================================================================
# helm_kafka.tf  —  Strimzi Kafka operator (KRaft)
# =============================================================================
# DECLARES: helm_release.strimzi (strimzi-kafka-operator 1.1.0, ns kafka)
# PURPOSE : the operator that manages Kafka / KafkaNodePool CRs. The actual KRaft
#           cluster (controller pool + broker pool) lives in kafka-cluster.yaml,
#           kubectl-applied by `make services` AFTER the operator registers its CRDs
#           — same pattern as lb-pool.yaml, so `tofu apply` stays single-pass.
# NEEDS   : namespaces.tf (kafka ns); storage via topolvm-provisioner (helm_topolvm.tf).
# IMPORT  : tofu import helm_release.strimzi kafka/strimzi-kafka-operator
# DOCS    : tf/k8s/README.md
# =============================================================================
resource "helm_release" "strimzi" {
  name       = "strimzi-kafka-operator"
  namespace  = kubernetes_namespace_v1.kafka.metadata[0].name
  repository = "https://strimzi.io/charts/"
  chart      = "strimzi-kafka-operator"
  version    = "1.1.0"
  values     = [file("${path.module}/values/kafka-operator.values.yaml")]

  atomic          = false
  cleanup_on_fail = false
}
