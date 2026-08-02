# Makefile for the wsfeed app (Kafka -> WebSocket bridge).
# Mirrors apps/helloworld: `make image` (ko build+push), `make apply`, `make deploy`, `make release`.
# TAG from git; override with `make image TAG=v1.2.3`. Registry defaults to bhyve.

REGISTRY      ?= registry.homelab.lan:5000
PUSH_REGISTRY ?= $(REGISTRY)
IMAGE         := wsfeed
NS            := app-livefeed
TAG           ?= $(shell git describe --tags --always --dirty)
IMG_REF       := $(REGISTRY)/$(IMAGE):$(TAG)
PLATFORM      := linux/amd64
KO            := $(shell go env GOPATH)/bin/ko

.PHONY: image check apply deploy release clean

# Build the image with ko and push to the registry ($(PUSH_REGISTRY)).
image:
	KO_DOCKER_REPO=$(PUSH_REGISTRY)/$(IMAGE) $(KO) build --bare --tags=$(TAG) \
	  --platform=$(PLATFORM) --push=true ./
	@echo ">> pushed $(PUSH_REGISTRY)/$(IMAGE):$(TAG)  (deploy ref: $(IMG_REF))"

check:
	@curl -s https://$(REGISTRY)/v2/$(IMAGE)/tags/list || echo "  registry unreachable"; echo

apply:
	sed 's@image: .*/$(IMAGE):.*@image: $(IMG_REF)@' deploy.yaml | kubectl apply -f -

deploy:
	kubectl -n $(NS) set image deploy/$(IMAGE) $(IMAGE)=$(IMG_REF)
	kubectl -n $(NS) rollout status deploy/$(IMAGE) --timeout=120s

release: image apply deploy

clean:
	rm -f wsfeed
