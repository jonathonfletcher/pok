# =============================================================================
# Cluster lifecycle — READ THIS FILE FIRST; you don't need the rest.
# =============================================================================
# Infra provider selected by platform.mk (INFRA_PROVIDER), or `make ... INFRA_PROVIDER=<p>`.
# Default: aws.
#     aws    — VMs in AWS            (tf/aws provisions + emits inventory)            [default]
#     bhyve  — VMs on FreeBSD/vm-bhyve (infra external; tf/bhyve just emits inventory)
#
# Layers, bottom to top:
#     infra     (OpenTofu) — tf/$(INFRA_PROVIDER): provision + emit the Ansible inventory
#     cluster   (Ansible)  — nodes + kubeadm                 -> ansible/
#     services  (OpenTofu) — Cilium, registry, storage, BGP  -> tf/k8s/
#     app       (ko + k8s) — all apps                        -> apps/
#
#   make platform             show the active infra provider
#   make use-bhyve|use-aws    switch the active provider (writes platform.mk)
#   make infra                OpenTofu: provision tf/$(INFRA_PROVIDER) + emit inventory
#   make cluster              build the nodes + kubeadm cluster (Ansible)
#   make services             install the in-cluster services (tf/k8s)
#   make trust                trust the (re)generated registry CA (sudo)
#   make app                  build + push + deploy ALL apps (apps/)
#   make netpol               enforce Cilium default-deny NetworkPolicies — OPTIONAL, run AFTER
#                             `make app` (the app namespaces it selects exist only after app).
#                             Deliberately NOT in `recreate`: a routine apply must never silently
#                             enforce default-deny and black-hole traffic on an incomplete allow-list.
#   make teardown             tear down infra (aws: tofu destroy, keep EIP; bhyve: cluster reset)
#   make recreate             teardown -> infra -> cluster -> services -> trust -> app  (bhyve one-shot; aws: run steps individually, tunnel between them)
#   make verify               nodes + tofu drift + app reachability
#   make border-ssh-on|off    aws: open/close border SSH (22) — setup/mgmt vs normal operation
#
# All targets are provider-aware (INFRA_PROVIDER): `cluster` skips the bhyve gate for aws,
# `services` uses per-provider tofu state + kubeconfig, and `app`/`verify`/`trust` pick the
# provider's registry/kubeconfig/endpoint. AWS additionally needs the SSH tunnel up first
# (`make tunnel` + the one-time lo alias) — see docs/README.md §6 and tf/aws/README.md §Access.
#
# MANUAL gate (bhyve only): `make cluster` generates DNS fragments then PAUSES for you to
# (1) install them on gateway.homelab.lan and (2) grow each disk0 +16G. Skipped for aws.
# =============================================================================
SHELL := /bin/bash
PLAYBOOK := cd ansible && .venv/bin/ansible-playbook
-include platform.mk
INFRA_PROVIDER ?= aws
# Per-provider variables (KCFG, REGISTRY, VERIFY_URL, the gate flags, …). Plain `include` so an
# unknown INFRA_PROVIDER fails loudly. Must come AFTER the default above.
include tf/$(INFRA_PROVIDER)/provider.mk
INFRA_DIR      := tf/$(INFRA_PROVIDER)
INVENTORY      := inventory/$(INFRA_PROVIDER)/hosts.ini

# Image tag for the deploy + the Honeycomb deploy marker (CI overrides with ci-<sha>).
TAG            ?= $(shell git describe --tags --always --dirty)
# Honeycomb deploy marker. Needs a CONFIGURATION key with "Manage Markers" (NOT the ingest key
# the collectors use) in HONEYCOMB_MARKER_KEY; the target is a no-op when it's unset, so local
# deploys don't fail. __all__ = environment-wide, so the marker lands on every dataset/board.
# MARKER_URL is optional (CI passes the workflow-run URL).
HONEYCOMB_MARKER_DATASET ?= __all__
MARKER_URL               ?=

.PHONY: help platform use-bhyve use-aws infra generate recreate cluster services netpol app trust teardown verify tunnel border-ssh-on border-ssh-off marker

help:
	@sed -n '3,33p' Makefile

platform:                                 ## show the active infra provider
	@echo "INFRA_PROVIDER=$(INFRA_PROVIDER)   (infra: $(INFRA_DIR)   inventory: ansible/$(INVENTORY))"

use-bhyve:                                 ## switch active provider -> bhyve
	@echo 'INFRA_PROVIDER := bhyve' > platform.mk && echo "INFRA_PROVIDER -> bhyve"

use-aws:                                   ## switch active provider -> aws
	@echo 'INFRA_PROVIDER := aws' > platform.mk && echo "INFRA_PROVIDER -> aws"

infra:                                     ## OpenTofu: provision tf/$(INFRA_PROVIDER) + emit the Ansible inventory
	$(MAKE) -C $(INFRA_DIR) apply

recreate: teardown infra cluster services trust app   ## full rebuild chain (bhyve one-shot; aws needs the tunnel between steps)

cluster:                                  ## Ansible: control-node artifacts -> OS prep -> kubeadm -> storage
	$(PLAYBOOK) -i $(INVENTORY) prepare.yml
ifneq ($(MANUAL_GATE),)
	@echo ""
	@echo ">>> MANUAL GATES — do these now, then press Enter:"
	@echo "    1. install ansible/dns/*.k8s.zone on gateway.homelab.lan (\$$INCLUDE, bump SOA, reload named)"
	@echo "    2. grow each bhyve VM's disk0 +16G and reboot it"
	@read -p ">>> Enter when both are done (Ctrl-C to abort)... " _
endif
	$(PLAYBOOK) -i $(INVENTORY) os_prep.yml
	$(PLAYBOOK) -i $(INVENTORY) bootstrap.yml
	$(PLAYBOOK) -i $(INVENTORY) storage.yml
	@echo ">>> cluster built. Nodes are NotReady until 'make services' installs Cilium."

generate:                                 ## regenerate tf/$(INFRA_PROVIDER) inventory + tf/k8s var-file from state (no infra changes)
	$(MAKE) -C $(INFRA_DIR) generate

services: generate                        ## OpenTofu: Cilium + registry + storage + BGP, then the LB/BGP/Kafka CRs (regenerates the provider var-file first)
	$(MAKE) -C tf/k8s apply INFRA_PROVIDER=$(INFRA_PROVIDER)

netpol:                                   ## enforce Cilium default-deny NetworkPolicies (OPTIONAL; run AFTER `make app`; not in `recreate`)
	$(MAKE) -C tf/k8s netpol INFRA_PROVIDER=$(INFRA_PROVIDER)

trust:                                    ## trust the (re)generated registry CA on this build host (sudo)
	$(MAKE) -C apps/helloworld trust REGISTRY_CA=$(REGISTRY_CA)

app:                                      ## build + push + deploy ALL apps (apps/Makefile) + a Honeycomb deploy marker
	# Provider values (registry, push endpoint, arch, kubeconfig) come from
	# tf/$(INFRA_PROVIDER)/provider.mk. aws pushes via the registry tunnel (:15000) + pulls
	# in-VPC (:5000), so `make tunnel` must be up first (see tf/aws/README.md §Access).
	KUBECONFIG=$(KCFG) $(MAKE) -C apps release \
	  REGISTRY=$(REGISTRY) PUSH_REGISTRY=$(PUSH_REGISTRY) PLATFORM=$(PLATFORM) TAG=$(TAG)
ifeq ($(INFRA_PROVIDER),aws)
	# awscost is AWS-only (IMDS creds + EC2/Cost Explorer): released here, never in the bhyve loop.
	# Needs tf/aws applied first (cost-reader instance profile + IMDS hop limit on all workers).
	KUBECONFIG=$(KCFG) $(MAKE) -C apps/awscost release \
	  REGISTRY=$(REGISTRY) PUSH_REGISTRY=$(PUSH_REGISTRY) PLATFORM=$(PLATFORM) TAG=$(TAG)
endif
	$(MAKE) marker    # deploy succeeded -> mark it (no-op without HONEYCOMB_MARKER_KEY)

marker:                                   ## emit a Honeycomb deploy marker (no-op without HONEYCOMB_MARKER_KEY)
ifeq ($(strip $(HONEYCOMB_MARKER_KEY)),)
	@echo ">>> honeycomb marker skipped (HONEYCOMB_MARKER_KEY unset)"
else
	@resp=$$(mktemp); \
	code=$$(curl -s -o $$resp -w '%{http_code}' -X POST https://api.honeycomb.io/1/markers/$(HONEYCOMB_MARKER_DATASET) \
	  -H "X-Honeycomb-Team: $(HONEYCOMB_MARKER_KEY)" \
	  -d '{"message":"deploy $(TAG) [$(INFRA_PROVIDER)]","type":"deploy","url":"$(MARKER_URL)"}'); \
	if [ "$$code" = "200" ] || [ "$$code" = "201" ]; then \
	  echo ">>> honeycomb deploy marker: $(TAG) [$(INFRA_PROVIDER)] (HTTP $$code)"; \
	else \
	  echo ">>> WARNING: honeycomb marker POST failed (HTTP $$code, non-fatal). A 401 means"; \
	  echo "    HONEYCOMB_MARKER_KEY is not a Configuration key with 'Manage Markers' (the"; \
	  echo "    collector ingest key does NOT work for markers). Response:"; \
	  sed 's/^/    /' $$resp; echo; \
	fi; \
	rm -f $$resp
endif

teardown:                                 ## tear down infra ($(INFRA_PROVIDER)); external-infra providers also reset the cluster
ifneq ($(RESET_ON_TEARDOWN),)
	# Infra is external (not tofu-managed), so teardown = kubeadm-reset the cluster + wipe the
	# local tf/k8s state + registry PKI (they described the now-destroyed cluster).
	$(PLAYBOOK) -i $(INVENTORY) teardown.yml -e confirm_teardown=yes
	rm -f tf/k8s/terraform.tfstate tf/k8s/terraform.tfstate.backup
	rm -rf registry/certs/$(INFRA_PROVIDER)
endif
	$(MAKE) -C $(INFRA_DIR) teardown

tunnel:                                   ## aws: bring up the SSH tunnel(s) through the border (API 16443 + registry 15000)
ifeq ($(INFRA_PROVIDER),aws)
	@echo "one-time prereq: sudo ip addr add 10.0.10.101/32 dev lo   (for the registry forward's IP-SAN cert)"
	ssh -fN -L 16443:10.0.10.101:6443 -L 10.0.10.101:15000:10.0.10.101:5000 -J ubuntu@$(BORDER_GW) ubuntu@10.0.10.101 \
	  && echo "aws tunnels up: 16443->cp1:6443 (API), 15000->cp1:5000 (registry)"
else
	@echo "no tunnel needed for INFRA_PROVIDER=$(INFRA_PROVIDER)"
endif

verify:                                   ## nodes + tofu drift + app reachability (provider-aware)
	KUBECONFIG=$(KCFG) kubectl get nodes
	$(MAKE) -C tf/k8s plan INFRA_PROVIDER=$(INFRA_PROVIDER)
	@curl -s -o /dev/null -w 'front door $(VERIFY_URL) -> HTTP %{http_code}\n' --max-time 8 $(VERIFY_URL) || true

border-ssh-on:                            ## aws: open border SSH (22) for setup/management
ifeq ($(INFRA_PROVIDER),aws)
	$(MAKE) -C tf/aws ssh-open
else
	@echo "border-ssh-on: no-op for INFRA_PROVIDER=$(INFRA_PROVIDER) (no border security group)"
endif

border-ssh-off:                           ## aws: close border SSH (22) during normal operation
ifeq ($(INFRA_PROVIDER),aws)
	$(MAKE) -C tf/aws ssh-close
else
	@echo "border-ssh-off: no-op for INFRA_PROVIDER=$(INFRA_PROVIDER) (no border security group)"
endif
