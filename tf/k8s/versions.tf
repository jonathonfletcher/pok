# OpenTofu + provider version pins.
#
# Proven on this host: OpenTofu v1.12.5 (/usr/bin/tofu). Providers resolve from
# the OpenTofu registry; exact selected versions are recorded in .terraform.lock.hcl
# by `tofu init` — commit that file so every run uses identical providers.

terraform {
  required_version = ">= 1.12.0"

  required_providers {
    # Manage raw Kubernetes objects (namespaces, manifests) on the cluster.
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 2.38"
    }
    # Install helm charts (Strimzi operator, etc.) — mirrors the cluster's
    # existing helm-managed pattern (cilium, otel collectors).
    helm = {
      source  = "hashicorp/helm"
      version = "~> 3.0"
    }
    # Render the kubectl-applied CRs (lb-pool.yaml, bgp.yaml) from provider vars.
    local = {
      source  = "hashicorp/local"
      version = "~> 2.0"
    }
  }
}
