# Emit the AWS Ansible inventory from the real instance IPs + the border EIP, so it
# is always in sync with the infra (no hardcoding). Mirrors tf/bhyve's stub emitter.
# The generated file is gitignored (ansible/inventory/aws/.gitignore) — regenerated
# on every apply. Ansible targets it with `-i inventory/aws/hosts.ini`.
resource "local_file" "ansible_inventory" {
  filename        = "${path.module}/../../ansible/inventory/aws/hosts.ini"
  file_permission = "0644"
  content = templatefile("${path.module}/templates/ansible-hosts.ini.tftpl", {
    cp_ips     = module.control_plane[*].private_ip
    worker_ips = module.worker[*].private_ip
    border_ip  = module.border.public_ip
  })
}

output "ansible_inventory_path" {
  value = local_file.ansible_inventory.filename
}
