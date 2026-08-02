# OpenTofu driver for the cluster services layer.
#
# Single-pass: there are no kubernetes_manifest resources, so `tofu apply` needs no
# staging. The Cilium LB pool (lb-pool.yaml), BGP peering/advertisement (bgp.yaml),
# and Kafka CRs (kafka-cluster.yaml) are applied by kubectl right after the apply,
# once the CRDs it installed have registered.
#
# Provider-specific inputs come from vars/<INFRA_PROVIDER>.tfvars.json, which
# tf/<provider> emits (this root assumes no provider). The top-level Makefile passes
# INFRA_PROVIDER; default here is aws.
INFRA_PROVIDER ?= aws
TFVARFILE      := vars/$(INFRA_PROVIDER).tfvars.json
# Per-provider state + kubeconfig so `make ... INFRA_PROVIDER=aws` never applies AWS vars
# into bhyve's state or drives the bhyve cluster. bhyve keeps the historical default
# terraform.tfstate + ~/.kube/config; aws uses aws.tfstate + the tunnelled aws-kubeconfig
# (bring the SSH tunnel up first — tf/aws/README.md §Access).
TFSTATE := $(if $(filter aws,$(INFRA_PROVIDER)),aws.tfstate,terraform.tfstate)
KCFG  := $(if $(filter aws,$(INFRA_PROVIDER)),$(abspath ../aws/aws-kubeconfig),$(HOME)/.kube/config)
KCTL  := KUBECONFIG=$(KCFG) kubectl

.PHONY: init plan apply fmt validate

# Auto-init guard: plan/apply/validate depend on `.terraform` (order-only), so a fresh checkout
# runs `tofu init` first instead of failing on missing plugins. Keyed on `.terraform/`, not
# `*.tfstate` — this root uses per-provider state files (aws.tfstate / terraform.tfstate), whose
# absence just means "not applied for that provider yet", never "uninitialized".
.terraform:
	tofu init

init:
	tofu init

plan: | .terraform
	tofu plan -state=$(TFSTATE) -var-file=$(TFVARFILE) -var 'kubeconfig=$(KCFG)'

apply: | .terraform
	tofu apply -state=$(TFSTATE) -var-file=$(TFVARFILE) -var 'kubeconfig=$(KCFG)'
	# On a fresh cluster the Cilium operator registers these CRDs a few seconds after
	# install — wait for them so the kubectl apply below can't race ("no matches for kind").
	$(KCTL) wait --for=condition=established --timeout=120s \
	  crd/ciliumloadbalancerippools.cilium.io
	$(KCTL) apply -f lb-pool.yaml
	# BGP L3 LoadBalancer: wait for the BGPv2 CRDs (registered when bgpControlPlane
	# is enabled), then apply the peering + advertisement (peers the provider's router).
	$(KCTL) wait --for=condition=established --timeout=120s \
	  crd/ciliumbgpclusterconfigs.cilium.io crd/ciliumbgppeerconfigs.cilium.io crd/ciliumbgpadvertisements.cilium.io
	$(KCTL) apply -f bgp.yaml
	# Kafka: the Strimzi operator (installed above) registers these CRDs — wait, then
	# apply the KRaft cluster CRs (controller + broker pools + Kafka).
	$(KCTL) wait --for=condition=established --timeout=180s \
	  crd/kafkas.kafka.strimzi.io crd/kafkanodepools.kafka.strimzi.io
	$(KCTL) apply -f kafka-cluster.yaml
	# Network policies: Cilium default-deny + explicit allows for the app namespaces (the
	# Cilium CRDs are established above). Applied last so a mistake is easy to roll back with
	# `kubectl delete -f network-policies.yaml`.
	$(KCTL) apply -f network-policies.yaml

fmt:
	tofu fmt

validate: | .terraform
	tofu validate
