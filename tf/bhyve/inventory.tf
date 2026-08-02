# The bhyve cluster's node addresses — the single hardcoded source of truth (these
# are static DHCP reservations on the gateway, keyed by MAC). Emitted as the Ansible
# inventory so bhyve and AWS share the same "tofu produces the inventory" workflow.
locals {
  primary_cp = {
    k8sm1 = "172.23.10.150"
  }
  secondary_cp = {
    k8sm2 = "172.23.10.151"
    k8sm3 = "172.23.10.152"
  }
  workers = {
    k8sw1 = "172.23.10.153"
    k8sw2 = "172.23.10.154"
    k8sw3 = "172.23.10.155"
    k8sw4 = "172.23.10.156"
    k8sw5 = "172.23.10.157"
    k8sw6 = "172.23.10.158"
    k8sw7 = "172.23.10.159"
  }
}

resource "local_file" "inventory" {
  filename        = "${path.module}/../../ansible/inventory/bhyve/hosts.ini"
  file_permission = "0644"
  content = templatefile("${path.module}/templates/hosts.ini.tftpl", {
    primary_cp   = local.primary_cp
    secondary_cp = local.secondary_cp
    workers      = local.workers
  })
}

output "inventory_path" {
  value = local_file.inventory.filename
}
