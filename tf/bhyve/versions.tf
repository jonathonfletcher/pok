# bhyve "infra" is vm-bhyve on a FreeBSD host — NOT tofu-managed. This root is a
# deliberate STUB: its only job is to emit the Ansible inventory (hardcoded node
# addresses) via the local provider, mirroring how tf/aws emits its inventory from
# real resources. No cloud/vm-bhyve provider is wired.

terraform {
  required_version = ">= 1.12.0"

  required_providers {
    local = {
      source  = "hashicorp/local"
      version = "~> 2.0"
    }
  }
}
