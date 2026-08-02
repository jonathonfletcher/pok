# AWS provider configuration.
#
# Credentials are NOT set here — the provider reads them from the ambient
# environment (AWS_PROFILE / AWS_ACCESS_KEY_ID+SECRET, or an SSO session).
# Keep it that way: no keys in the repo, no keys in tfvars.
#
# default_tags stamps every taggable resource with the environment tag, so
# individual resources only need to add what's specific to them. (awscc has no
# default-tags concept.)

provider "aws" {
  region = var.region

  default_tags {
    tags = {
      environment = var.environment
      managed-by  = "opentofu"
    }
  }
}
