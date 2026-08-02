# OpenTofu + provider version pins.
#
# Proven on this host: OpenTofu v1.12.5 (/usr/bin/tofu). Providers resolve from
# the OpenTofu registry; exact selected versions are recorded in .terraform.lock.hcl
# by `tofu init` — commit that file so every run uses identical providers.
#
# Decision: single provider, hashicorp/aws only (see docs/README.md "Provider choice").
# No awscc — we want working `import`, stable plans (no tag-list churn), and the
# aws provider's full data-source coverage.

terraform {
  required_version = ">= 1.12.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
    # emits the Ansible inventory from the real instance IPs (ansible-inventory.tf)
    local = {
      source  = "hashicorp/local"
      version = "~> 2.0"
    }
  }

  # TODO: move state off local disk to an encrypted S3 backend before this layer
  # provisions anything real (state will hold IDs, and any secrets, in cleartext).
  # https://opentofu.org/docs/language/settings/backends/s3/
}
