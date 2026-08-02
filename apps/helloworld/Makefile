# Makefile for the helloworld app
#
#   make build   - compile the static Go binary (./helloworld) for local runs
#   make image   - ko build + PUSH to the private registry as $(IMG_REF)
#   make check   - list pushed tags of $(IMAGE) from the registry
#   make apply   - kubectl apply -f deploy.yaml (create ns/Deployment/Service)
#   make deploy  - point the running Deployment at $(IMG_REF) (triggers a rollout)
#   make release - image + apply + deploy (full push of a new tagged build)
#   make trust   - install the registry CA into THIS host's system trust store (sudo)
#   make clean   - remove local build artifacts
#
# On a FRESH cluster the objects don't exist yet, so `make deploy` (which only
# re-points an existing Deployment) would fail. Use `make release`, or run
# `make apply` once before `make deploy`.
#
# Images are distributed via the in-cluster registry (registry.homelab.lan:5000),
# NOT by importing tarballs to each node. `ko build --push` uploads once; every
# node's containerd pulls over TLS (see tf/k8s/registry.tf and the registry
# runbook). The build host (k8sm1) trusts the registry CA via update-ca-certificates.
#
# TAG comes from git: `git describe --tags --always --dirty`.
#   - clean tagged commit  -> vX.Y.Z         (immutable; `make deploy` auto-rolls)
#   - uncommitted changes  -> vX.Y.Z-dirty   (behaves like a floating tag; see note on `deploy`)
# Override on the CLI if needed:  make image TAG=v1.2.3

APP        := helloworld
# Provider-specific: override per provider, e.g. `make release REGISTRY=<aws-registry>`.
# (Full registry de-provider-ization — ECR vs the bhyve private registry — is Category B.)
REGISTRY   ?= registry.homelab.lan:5000
# Where `make image` PUSHES (defaults to REGISTRY). On aws the build host reaches the
# registry only through an SSH tunnel on a different port than the nodes' in-VPC pull path,
# so push and deploy refs differ — e.g. PUSH_REGISTRY=10.0.10.101:15000 (tunnel) while
# REGISTRY=10.0.10.101:5000 (node pull). Same registry backend, same repo/tag.
PUSH_REGISTRY ?= $(REGISTRY)
# Registry CA for `make trust`. Certs are per-provider under registry/certs/<provider>/;
# default is bhyve — override for aws, e.g. `make trust REGISTRY_CA=../../registry/certs/aws/ca.crt`.
REGISTRY_CA ?= ../../registry/certs/bhyve/ca.crt
IMAGE      := helloworld
TAG        ?= $(shell git describe --tags --always --dirty)
IMG_REF    := $(REGISTRY)/$(IMAGE):$(TAG)
PLATFORM   := linux/amd64
NS         := app-helloworld

# ko installs into $(go env GOPATH)/bin, which may not be on PATH; reference it directly.
KO := $(shell go env GOPATH)/bin/ko

.PHONY: all build image check apply deploy release trust clean

all: build

# Build the app as a static binary (CGO off) for local runs. ko compiles its own
# binary for the image, so this is not a prerequisite of `image`.
build:
	CGO_ENABLED=0 go build -o $(APP) .

# Build the container image with ko and PUSH it to the private registry.
# --bare makes the ref exactly $(REGISTRY)/$(IMAGE):$(TAG); --tags sets the tag.
image:
	KO_DOCKER_REPO=$(PUSH_REGISTRY)/$(IMAGE) $(KO) build --bare --tags=$(TAG) \
	  --platform=$(PLATFORM) --push=true ./
	@echo ">> pushed $(PUSH_REGISTRY)/$(IMAGE):$(TAG)  (deploy ref: $(IMG_REF))"

# List the tags the registry holds for $(IMAGE). (k8sm1 trusts the registry CA
# via the system trust store, so no --cacert is needed here.)
check:
	@echo ">> tags of $(IMAGE) in $(REGISTRY):"
	@curl -s https://$(REGISTRY)/v2/$(IMAGE)/tags/list || echo "  registry unreachable"
	@echo

# Create/refresh the app objects (namespace, Deployment, Service). Idempotent.
# Required on a fresh cluster BEFORE `deploy` (which only re-points an existing
# Deployment). The image pinned in deploy.yaml must already be in the registry.
apply:
	sed 's@image: .*/$(IMAGE):.*@image: $(IMG_REF)@' deploy.yaml | kubectl apply -f -

# update-cluster: point the running Deployment at this build's tag. `set image`
# mutates the pod template, triggering a rollout on its own.
#   NOTE: with a *-dirty tag the ref does not change between rebuilds, so `set image`
#   is a no-op and nothing rolls -- commit + `git tag vX.Y.Z` for real deploys.
deploy:
	kubectl -n $(NS) set image deploy/helloworld helloworld=$(IMG_REF)
	kubectl -n $(NS) rollout status deploy/helloworld --timeout=120s

# Full release of a new tagged build on a (possibly fresh) cluster: push the image,
# ensure the objects exist, then roll the Deployment to this tag.
release: image apply deploy

# Trust the private registry CA on THIS build host. ko/curl use the system trust
# store, which is SEPARATE from the nodes' containerd certs.d (Ansible sets that,
# not this). Ubuntu/Debian; needs sudo. Run once per build host.
trust:
	sudo cp $(REGISTRY_CA) /usr/local/share/ca-certificates/registry-homelab.crt
	sudo update-ca-certificates

clean:
	rm -f $(APP)
