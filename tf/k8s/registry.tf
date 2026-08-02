# Private OCI image registry (CNCF Distribution / registry:2), TLS. Parameterized by provider:
# count = var.manage_registry, node = var.registry_node, certs = var.registry_certs_dir.
#
# Replaces the ko-tarball -> per-node `ctr import` distribution: build once with `ko build
# --push` (bhyve registry.homelab.lan:5000, aws cp1 10.0.10.101:5000), nodes pull normally.
#
# - Pinned to var.registry_node (bhyve k8sm1, aws cp1) via nodeSelector + control-plane
#   toleration, hostNetwork so it binds <node-ip>:5000 directly.
# - Image blobs persist on hostPath /var/lib/registry on var.registry_node. NOT a TopoLVM
#   PVC: the registry pins to the primary control-plane, and on aws the control planes have
#   no TopoLVM VG (only workers carry the data disk), so a node-local PVC there would never
#   bind. Survives pod restarts, not loss of the pinned node.
# - No registry auth: it's reachable only on the cluster network (bhyve LAN / aws VPC).
# - TLS server cert (SAN per provider) from var.registry_certs_dir (registry/certs/<provider>),
#   delivered as a kubernetes.io/tls Secret. The CA is distributed to each node's containerd
#   certs.d out-of-band (see the kube_registry_trust Ansible role) — node-OS config, not via the k8s API.

resource "kubernetes_namespace_v1" "registry" {
  count = var.manage_registry ? 1 : 0
  metadata { name = "registry" }
}

resource "kubernetes_secret_v1" "registry_tls" {
  count = var.manage_registry ? 1 : 0
  metadata {
    name      = "registry-tls"
    namespace = kubernetes_namespace_v1.registry[0].metadata[0].name
  }
  type = "kubernetes.io/tls"
  data = {
    "tls.crt" = file("${path.module}/${var.registry_certs_dir}/tls.crt")
    "tls.key" = file("${path.module}/${var.registry_certs_dir}/tls.key")
  }
}

resource "kubernetes_deployment_v1" "registry" {
  count = var.manage_registry ? 1 : 0
  metadata {
    name      = "registry"
    namespace = kubernetes_namespace_v1.registry[0].metadata[0].name
    labels    = { app = "registry" }
  }
  spec {
    replicas = 1
    # Recreate, not RollingUpdate: single replica pinned to one node with hostNetwork — a rolling
    # surge pod would fail to bind :5000 (EADDRINUSE) and hang the rollout (maxUnavailable=0).
    strategy { type = "Recreate" }
    selector { match_labels = { app = "registry" } }
    template {
      metadata { labels = { app = "registry" } }
      spec {
        # Pin to the primary control-plane and bind its host network (:5000).
        host_network  = true
        dns_policy    = "ClusterFirstWithHostNet"
        node_selector = { "kubernetes.io/hostname" = var.registry_node }
        toleration {
          key      = "node-role.kubernetes.io/control-plane"
          operator = "Exists"
          effect   = "NoSchedule"
        }
        container {
          name  = "registry"
          image = "registry:2"
          port { container_port = 5000 }
          env {
            name  = "REGISTRY_HTTP_ADDR"
            value = "0.0.0.0:5000"
          }
          env {
            name  = "REGISTRY_HTTP_TLS_CERTIFICATE"
            value = "/certs/tls.crt"
          }
          env {
            name  = "REGISTRY_HTTP_TLS_KEY"
            value = "/certs/tls.key"
          }
          # No registry auth: the registry is not reachable outside the cluster network
          # (bhyve LAN / aws VPC internal subnet), so basic-auth adds no security here.
          # Default storage root for registry:2 is /var/lib/registry.
          volume_mount {
            name       = "images"
            mount_path = "/var/lib/registry"
          }
          volume_mount {
            name       = "certs"
            mount_path = "/certs"
            read_only  = true
          }
          readiness_probe {
            http_get {
              path   = "/v2/"
              port   = 5000
              scheme = "HTTPS"
            }
            initial_delay_seconds = 3
          }
        }
        # Blobs on hostPath /var/lib/registry (see header — no PVC: registry pins to a CP node).
        volume {
          name = "images"
          host_path {
            path = "/var/lib/registry"
            type = "DirectoryOrCreate"
          }
        }
        volume {
          name = "certs"
          secret { secret_name = kubernetes_secret_v1.registry_tls[0].metadata[0].name }
        }
      }
    }
  }
}
