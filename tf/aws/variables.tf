variable "region" {
  type = string
}

variable "az" {
  type = string
}

variable "environment" {
  type = string
}

variable "cidr" {
  type = string
}

variable "internal_domain" {
  type    = string
  default = "demo.internal" # Route 53 private zone (in-VPC DNS + HA API endpoint)
}

variable "ssh_public_key_filename" {
  type = string
}

# ---- border (internet-facing bastion + Caddy reverse proxy) --------------------
variable "border_instance_type" {
  type    = string
  default = "t4g.micro" # 1 GiB — plenty for Caddy + SSH ProxyJump
}

variable "border_datadisk_size" {
  type    = number
  default = 0
}

variable "border_allow_ssh" {
  description = "Whether the border SG allows inbound SSH (22/tcp) from the internet. Toggle with `make border-ssh-on|off`, which persists the choice in border-ssh.auto.tfvars.json (auto-loaded, so it survives `make infra`). This default applies only when that file is absent (fresh build)."
  type        = bool
  default     = true # open on a fresh build so the initial ProxyJump/cluster build works
}

# Caddy reverse-proxy config baked into the border's cloud-init (both optional).
variable "border_proxy_hostname" {
  type        = string
  default     = ""
  description = "Public DNS name for Caddy auto-HTTPS (e.g. dev.somegroup.net). Empty = serve plain HTTP on :80."
}

variable "border_proxy_upstream" {
  type        = string
  default     = ""
  description = "Reverse-proxy target host:port — the Cilium LB IP (from the LB subnet) for hello-world. Empty = placeholder page until the cluster exists."
}

# ---- control-plane pool --------------------------------------------------------
variable "control_plane_count" {
  type    = number
  default = 3
}

variable "control_plane_instance_type" {
  type    = string
  default = "t4g.small" # 2 vCPU / 2 GiB — kubeadm floor; bump to t4g.medium for headroom
}

variable "control_plane_datadisk_size" {
  type    = number
  default = 0
}

# ---- worker pool ---------------------------------------------------------------
variable "worker_count" {
  type    = number
  default = 3
}

variable "worker_instance_type" {
  type    = string
  default = "t4g.medium" # 4 GiB — the Kafka broker+controller sizing (kafka-cluster.yaml) needs it
}

variable "worker_datadisk_size" {
  type    = number
  default = 0
}
