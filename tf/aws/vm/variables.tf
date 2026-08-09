variable "az" {
  type = string
}

variable "subnet" {
  type = object({
    id         = string
    cidr_block = string
  })
}

variable "security_groups" {
  type = list(object({
    id = string
  }))
}

variable "public_ip_address" {
  type = bool
}

variable "instance_type" {
  type = string
}

variable "ssh_key" {
  type = object({
    key_name = string
  })
}

variable "data_volume_type" {
  default = "gp3"
}

variable "data_volume_size" {
  type    = number
  default = 0
}

variable "data_volume_device" {
  type    = string
  default = "/dev/sdf"
}

# Protect this instance's EIP allocation from destroy so its public IP (and any
# external DNS pointing at it) survives teardown/rebuild cycles. Only meaningful
# when public_ip_address = true.
variable "eip_prevent_destroy" {
  type    = bool
  default = false
}

# Set false on k8s nodes so their ENI accepts packets whose dst is a Cilium LB VIP
# (10.0.20.0/24), which the border routes to the node via BGP. AWS drops dst!=ENI-IP
# traffic unless source/dest checking is off.
variable "source_dest_check" {
  type    = bool
  default = true
}

variable "environment" {
  type = string
}

variable "kind" {
  type = string
}

variable "instance_base_number" {
  type    = number
  default = 100
}

variable "instance_number" {
  type = number
}

variable "user_data" {
  type    = string
  default = null
}

# Name of an IAM instance profile to attach (null = none). Attaching/detaching is an
# in-place update on a running instance — it does NOT recreate it.
variable "iam_instance_profile" {
  type    = string
  default = null
}

# IMDS PUT-response hop limit. AWS default is 1, which lets host processes reach IMDS but
# NOT pods (a pod is one extra network hop past the host). Set to 2 on nodes that may run a
# pod using instance-profile creds via IMDS (the awscost poller). Left at 1 elsewhere, which
# emits NO metadata_options block (dynamic below) so those instances see zero diff.
variable "metadata_http_put_hop_limit" {
  type    = number
  default = 1
}

# When true, editing user_data recreates the instance so cloud-init re-runs. Cloud-init
# only runs on first boot anyway, so for the border we set this false: config changes are
# delivered to the running instance out-of-band and the updated template applies on the
# next deliberate rebuild — avoiding accidental destruction of the (EIP-bearing) border.
variable "user_data_replace_on_change" {
  type    = bool
  default = true
}

variable "tags" {
  type    = map(any)
  default = {}
}