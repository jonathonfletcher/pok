output "border_public_ip_address" {
  value = module.border.public_ip
}

output "control_plane_private_ips" {
  value = module.control_plane[*].private_ip
}

output "worker_private_ips" {
  value = module.worker[*].private_ip
}

output "cilium_lb_subnet_cidr" {
  description = "Range the Cilium LoadBalancer IP pool draws from (see cilium-lb.tf)."
  value       = aws_subnet.cilium_lb.cidr_block
}
