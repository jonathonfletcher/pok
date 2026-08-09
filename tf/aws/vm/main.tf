data "aws_ami" "vm" {
  most_recent = true
  owners      = ["amazon"]

  filter {
    name   = "name"
    values = ["ubuntu/images/hvm-ssd-*/ubuntu-noble-24.04-*-server-*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "state"
    values = ["available"]
  }

  filter {
    name   = "architecture"
    values = ["arm64"]
  }
}


resource "aws_ebs_volume" "vm" {
  count             = var.data_volume_size > 0 ? 1 : 0
  availability_zone = var.az
  size              = var.data_volume_size
  type              = var.data_volume_type
  final_snapshot    = false

  tags = merge({
    Name = "${var.environment}-${var.kind}-${format("%02d", 1 + var.instance_number)}-vm-datadisk"
  }, var.tags)
}


resource "aws_volume_attachment" "vm" {
  count       = var.data_volume_size > 0 ? 1 : 0
  device_name = var.data_volume_device
  volume_id   = aws_ebs_volume.vm[0].id
  instance_id = aws_instance.vm.id
}


# EIP is split into a standalone allocation + a separate association (not the
# combined `instance =` form) on purpose: it lets teardown drop the association
# while KEEPING the unassociated allocation. An unassociated EIP has no "mapped
# public address", so the internet gateway can still detach and the rest tears down
# cleanly. `destroy-keep-eip` excludes just this allocation.
resource "aws_eip" "vm" {
  count  = var.public_ip_address ? 1 : 0
  domain = "vpc"
  tags = merge({
    Name = "${var.environment}-${var.kind}-${format("%02d", 1 + var.instance_number)}"
  }, var.tags)

  # When true, the allocation (public IP) survives teardown so external DNS
  # (e.g. pok.somegroup.net) stays valid across rebuilds. OpenTofu enforces a
  # variable here. A full `tofu destroy` then ERRORS on this resource by design.
  lifecycle {
    prevent_destroy = var.eip_prevent_destroy
  }
}

resource "aws_eip_association" "vm" {
  count               = var.public_ip_address ? 1 : 0
  allocation_id       = aws_eip.vm[0].id
  instance_id         = aws_instance.vm.id
  allow_reassociation = true
}


resource "aws_instance" "vm" {
  availability_zone = var.az

  ebs_optimized = false
  instance_type = var.instance_type
  ami           = data.aws_ami.vm.id

  root_block_device {
    volume_size = 32
    volume_type = "gp3"
    tags = merge({
      Name = "${var.environment}-${var.kind}-${format("%02d", 1 + var.instance_number)}"
      Kind = "${var.kind}"
    }, var.tags)
  }

  subnet_id                   = var.subnet.id
  vpc_security_group_ids      = [for sg in var.security_groups : sg.id]
  private_ip                  = cidrhost(var.subnet.cidr_block, var.instance_base_number + 1 + var.instance_number)
  associate_public_ip_address = true
  ipv6_address_count          = 0
  source_dest_check           = var.source_dest_check

  monitoring = true

  # Only emitted when a hop limit > 1 is requested (all workers, which may host the awscost poller), so every other
  # instance keeps AWS's default metadata options and shows no diff. hop_limit=2 lets a pod reach
  # IMDSv2 for instance-profile creds; http_tokens=required keeps it IMDSv2-only.
  dynamic "metadata_options" {
    for_each = var.metadata_http_put_hop_limit > 1 ? [1] : []
    content {
      http_endpoint               = "enabled"
      http_tokens                 = "required"
      http_put_response_hop_limit = var.metadata_http_put_hop_limit
    }
  }

  key_name                    = var.ssh_key.key_name
  iam_instance_profile        = var.iam_instance_profile
  user_data                   = var.user_data
  user_data_replace_on_change = var.user_data_replace_on_change

  tags = merge({
    Name = "${var.environment}-${var.kind}-${format("%02d", 1 + var.instance_number)}"
    Kind = "${var.kind}"
  }, var.tags)

  lifecycle {
    ignore_changes = [ami, launch_template, tags, private_ip, user_data]
    # prevent_destroy = true
  }
}
