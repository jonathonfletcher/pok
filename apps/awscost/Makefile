# Makefile for the awscost app (AWS billing -> OTLP metrics poller). AWS provider ONLY.
# Mirrors apps/clusterinfo: `make image` (ko build+push), `make apply`, `make deploy`, `make release`.
# TAG from git; override with `make image TAG=v1.2.3`. On aws the top-level Makefile overrides
# REGISTRY (registry tunnel) and PLATFORM (linux/arm64); the committed default is bhyve-shaped
# only so the `apply` sed has something to rewrite — awscost is never deployed on bhyve.

REGISTRY      ?= registry.homelab.lan:5000
PUSH_REGISTRY ?= $(REGISTRY)
IMAGE         := awscost
NS            := aws-cost
TAG           ?= $(shell git describe --tags --always --dirty)
IMG_REF       := $(REGISTRY)/$(IMAGE):$(TAG)
PLATFORM      := linux/amd64
KO            := $(shell go env GOPATH)/bin/ko

.PHONY: image check namespace apply deploy release clean

# Build the image with ko and push to the registry ($(PUSH_REGISTRY)).
image:
	KO_DOCKER_REPO=$(PUSH_REGISTRY)/$(IMAGE) $(KO) build --bare --tags=$(TAG) \
	  --platform=$(PLATFORM) --push=true ./
	@echo ">> pushed $(PUSH_REGISTRY)/$(IMAGE):$(TAG)  (deploy ref: $(IMG_REF))"

check:
	@curl -s https://$(REGISTRY)/v2/$(IMAGE)/tags/list || echo "  registry unreachable"; echo

namespace:
	kubectl apply -f namespace.yaml

apply: namespace
	sed 's@image: .*/$(IMAGE):.*@image: $(IMG_REF)@' deploy.yaml | kubectl apply -f -

deploy:
	kubectl -n $(NS) set image deploy/$(IMAGE) $(IMAGE)=$(IMG_REF)
	kubectl -n $(NS) rollout status deploy/$(IMAGE) --timeout=120s

release: image apply deploy

clean:
	rm -f awscost
