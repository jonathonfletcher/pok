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
#   make -C apps apply     KafkaTopics + every app's manifests
#   make -C apps deploy     roll every Deployment to its built tag
#   make -C apps release   images + apply + deploy for every app
#   make -C apps <name>    release just one app, e.g. `make -C apps wsfeed`

# Apps in two namespace groups; APPS is their concatenation (order within each is load-bearing).
#  - APPS_HELLOWORLD: helloworld owns the `app-helloworld` Namespace and produces the url-visits
#    topic that visitcounter consumes, so this group leads.
#  - APPS_LIVEFEED: all live in the shared `app-livefeed` Namespace. static owns that Namespace —
#    its deploy.yaml is the ONLY place that creates it — so it MUST be first in the group;
#    otherwise the next app-livefeed app applies into a missing namespace and errors
#    "namespaces \"app-livefeed\" not found" (invisible once the namespace already exists).
#    edge fronts the feed apps, so it goes last.
APPS_HELLOWORLD := helloworld
APPS_LIVEFEED   := static visitcounter wsfeed clusterinfo edge
APPS            := $(APPS_HELLOWORLD) $(APPS_LIVEFEED)

.PHONY: images topics apply deploy release $(APPS)

images:
	@for a in $(APPS); do echo "== image: $$a =="; $(MAKE) -C $$a image || exit 1; done

topics:
	kubectl apply -f kafka-topics.yaml

apply: topics
	@for a in $(APPS); do echo "== apply: $$a =="; $(MAKE) -C $$a apply || exit 1; done

deploy:
	@for a in $(APPS); do echo "== deploy: $$a =="; $(MAKE) -C $$a deploy || exit 1; done

release: topics
	@for a in $(APPS); do echo "== release: $$a =="; $(MAKE) -C $$a release || exit 1; done

# Build+deploy a single app by name: `make -C apps static`
$(APPS):
	$(MAKE) -C $@ release
