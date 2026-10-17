# apps/Makefile — build/deploy ALL apps in one place.
#
# Each app subdir (helloworld, visitcounter, static, wsfeed, clusterinfo, edge) has its own Makefile with
# image / apply / deploy / release targets; this aggregates them. Command-line variables
# (REGISTRY, PUSH_REGISTRY, TAG, PLATFORM) propagate to the sub-makes via MAKEFLAGS, so the
# top-level Makefile can pass provider overrides for aws (registry tunnel, arm64, etc.).
# `apply` renders the deploy.yaml image to $(REGISTRY)/<app>:$(TAG) per provider before applying,
# so the committed image line is only a default (bhyve) — no provider host is hardcoded into a live apply.
#
#   make -C apps images    ko build + push every image
#   make -C apps namespaces  create the app-layer namespaces (apps/namespaces.yaml)
#   make -C apps apply     namespaces + KafkaTopics + every app's manifests
#   make -C apps deploy     roll every Deployment to its built tag
#   make -C apps release   images + apply + deploy for every app
#   make -C apps <name>    release just one app, e.g. `make -C apps wsfeed`

# Apps grouped by namespace; APPS is their concatenation.
#  - APPS_HELLOWORLD: helloworld (in app-helloworld) produces the url-visits topic that
#    visitcounter consumes, so this group leads.
#  - APPS_LIVEFEED: the app-livefeed apps; edge fronts the feed apps, so it goes last.
# The namespaces are created up front by the `namespaces` target (apps/namespaces.yaml), so this
# order is NOT load-bearing for namespace creation — it only reflects the url-visits data flow.
APPS_HELLOWORLD := helloworld
APPS_LIVEFEED   := static visitcounter wsfeed clusterinfo edge
APPS            := $(APPS_HELLOWORLD) $(APPS_LIVEFEED)

# Build with the newest stable Go. go.dev/VERSION returns e.g. "go1.27.2"; exporting it as
# GOTOOLCHAIN makes every `go`/`ko` run below (and in the sub-makes) use that release,
# downloading it on first use. "+auto" lets a go.mod that demands something newer win.
# Offline / go.dev unreachable -> "auto" (whatever go.mod names). Override: make GO_LATEST=go1.27.1
GO_LATEST ?= $(shell curl -fsSL --max-time 5 'https://go.dev/VERSION?m=text' 2>/dev/null | head -1)
export GOTOOLCHAIN := $(if $(GO_LATEST),$(GO_LATEST)+auto,auto)

.PHONY: images namespaces topics apply deploy release go-update $(APPS)

# Raise the go.mod floor (`go` + `toolchain` lines) of every app to the latest Go and deps,
# then vet/build/test. Builds already use the newest Go (GOTOOLCHAIN above); this moves the
# committed minimum.
#   make -C apps go-update            latest toolchain + deps
#   make -C apps go-update DEPS=      toolchain only (skip go get -u)
DEPS ?= -u
go-update:
	@set -e; for a in $(APPS) awscost; do echo "== go-update: $$a =="; \
	  ( cd $$a && go get go@latest toolchain@latest && \
	    { [ -z "$(DEPS)" ] || go get $(DEPS) ./... ; } && go mod tidy && \
	    go vet ./... && go build ./... && go test ./... ); done
	@grep -H -E '^(go|toolchain) ' */go.mod

images:
	@for a in $(APPS); do echo "== image: $$a =="; $(MAKE) -C $$a image || exit 1; done

namespaces:
	kubectl apply -f namespaces.yaml

topics:
	kubectl apply -f kafka-topics.yaml

apply: namespaces topics
	@for a in $(APPS); do echo "== apply: $$a =="; $(MAKE) -C $$a apply || exit 1; done

deploy:
	@for a in $(APPS); do echo "== deploy: $$a =="; $(MAKE) -C $$a deploy || exit 1; done

release: namespaces topics
	@for a in $(APPS); do echo "== release: $$a =="; $(MAKE) -C $$a release || exit 1; done

# Build+deploy a single app by name: `make -C apps static`
$(APPS):
	$(MAKE) -C $@ release
