resource "aws_key_pair" "vm" {
  key_name = "${var.environment}-ssh-key"
  # pathexpand() so a leading ~ works — tofu's file() does NOT expand it.
  public_key = file(pathexpand(var.ssh_public_key_filename))

}
