# Connectivity proof — reads (never mutates) a namespace that must exist on any
# k8s cluster. If `tofu plan` resolves this data source, provider auth against
# the cluster is proven. Real resources (Strimzi, Kafka, ...) come in later files.

data "kubernetes_namespace_v1" "kube_system" {
  metadata {
    name = "kube-system"
  }
}

output "cluster_connectivity_proof" {
  description = "kube-system namespace UID + name, read live from the cluster (proves provider auth at plan time)"
  value = {
    uid  = data.kubernetes_namespace_v1.kube_system.metadata[0].uid
    name = data.kubernetes_namespace_v1.kube_system.metadata[0].name
  }
}
