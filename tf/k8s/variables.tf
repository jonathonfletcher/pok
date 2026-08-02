# Input variables.

variable "kubeconfig" {
  description = "Path to the kubeconfig used to reach the cluster"
  type        = string
  default     = "~/.kube/config"
}

# ---- provider-specific (supplied via -var-file=vars/<INFRA_PROVIDER>.tfvars.json,
#      which tf/<provider> emits). No defaults on purpose: tf/k8s must not assume a
#      provider. ------------------------------------------------------------------
variable "cilium_devices" {
  description = "NIC(s) Cilium manages / announces on (e.g. enp0s5 bhyve, ens5 aws)."
  type        = string
}

variable "k8s_service_host" {
  description = "kube-apiserver host Cilium's KPR connects to (control-plane endpoint host)."
  type        = string
}

variable "lb_pool_prefix" {
  description = "First 3 octets of the routed /24 LB pool (apps VIP .240; Kafka is internal-only, no VIP). e.g. 172.23.20 / 10.0.20."
  type        = string
}

variable "bgp_local_asn" {
  description = "Cilium's local ASN (bhyve 65010, aws 65020)."
  type        = number
}

variable "bgp_peer_asn" {
  description = "The router's ASN Cilium peers with (bhyve gateway 65000, aws border 65001)."
  type        = number
}

variable "bgp_peer_address" {
  description = "The BGP peer address (bhyve gateway 172.23.10.100, aws border 10.0.100.101)."
  type        = string
}

variable "bgp_cluster_name" {
  description = "metadata.name of the CiliumBGPClusterConfig (bhyve / aws)."
  type        = string
}

variable "bgp_peer_name" {
  description = "Name of the BGP peer (bhyve 'gateway', aws 'border'); peerConfig is <name>-peer."
  type        = string
}

variable "manage_registry" {
  description = "Whether this provider self-hosts the private registry on a node. true for bhyve + aws (both cp1-hosted)."
  type        = bool
}

variable "registry_node" {
  description = "kubernetes.io/hostname the registry Deployment pins to (bhyve k8sm1; aws cp1). No default: supplied per provider via vars/<provider>.tfvars.json."
  type        = string
}

variable "registry_certs_dir" {
  description = "Dir (relative to this module) with the registry tls.crt/tls.key; per provider, gitignored. Supplied via vars/<provider>.tfvars.json (registry/certs/<provider>)."
  type        = string
}

variable "honeycomb_api_key" {
  description = <<-EOT
    Honeycomb ingest API key (the x-honeycomb-team header value). Backs the
    honeycomb Secret (key api-key) that the OpenTelemetry collectors read via
    secretKeyRef. NEVER commit the value — set it in terraform.tfvars.json, which
    is gitignored. A placeholder lives in terraform.tfvars.json.example.
  EOT
  type        = string
  sensitive   = true
}
