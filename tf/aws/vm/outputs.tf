output "private_ip" {
  value = aws_instance.vm.private_ip
}

output "public_ip" {
  value = var.public_ip_address ? aws_eip.vm[0].public_ip : null
}

output "host_id" {
  value = aws_instance.vm.host_id
}

output "primary_network_interface_id" {
  value = aws_instance.vm.primary_network_interface_id
}

output "subnet_id" {
  value = aws_instance.vm.subnet_id
}

output "security_groups" {
  value = aws_instance.vm.security_groups
}

output "instance_id" {
  value = aws_instance.vm.id
}
