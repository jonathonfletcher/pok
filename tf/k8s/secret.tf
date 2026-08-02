# The Honeycomb ingest credential the OTel collectors read via secretKeyRef
# (secret name "honeycomb", key "api-key" in namespace honeycomb).
#
# IMPORT-FIRST: this Secret already exists and the collectors depend on it.
# Import adopts the live object; a recreate would briefly delete the collectors'
# credential, so prevent_destroy guards against that.
#
# Import:
#   tofu import kubernetes_secret_v1.honeycomb honeycomb/honeycomb

resource "kubernetes_secret_v1" "honeycomb" {
  metadata {
    name      = "honeycomb"
    namespace = kubernetes_namespace_v1.honeycomb.metadata[0].name
  }
  type = "Opaque"
  data = {
    "api-key" = var.honeycomb_api_key
  }

  lifecycle {
    prevent_destroy = true
  }
}
