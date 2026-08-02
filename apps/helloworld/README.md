# `apps/helloworld/` — the helloworld app

A Go HTTP app used to exercise the cluster end to end (registry pull →
scheduling → edge reverse proxy → external reach). It is **not** part of the
platform: the cluster + services are built by `../../Makefile` (`make cluster` /
`make services`); this is what you deploy *onto* the finished platform (`make app`).

## Deploy / release

From the repo root: **`make app`** (= `make -C apps/helloworld release`). Or directly:

```bash
cd apps/helloworld
export PATH="$(go env GOPATH)/bin:$PATH"     # ko lives in $GOPATH/bin
make trust        # ONE-TIME per build host: trust the registry CA for ko (sudo)
git tag vX.Y.Z    # the image tag; see "tag scheme" below
make release      # = image (ko build+push) -> apply (create objects) -> deploy (roll)
```

`make image` pushes to `$(PUSH_REGISTRY)/helloworld:<tag>` (Makefile sets
`KO_DOCKER_REPO=$(PUSH_REGISTRY)/$(IMAGE)`); only the deploy ref `IMG_REF` uses
`$(REGISTRY)`. `PUSH_REGISTRY ?= $(REGISTRY)`, so they match on bhyve, but on aws
push goes via an SSH tunnel port while nodes pull on `:5000` — e.g.
`PUSH_REGISTRY=10.0.10.101:15000` with `REGISTRY=10.0.10.101:5000`. `REGISTRY ?=
registry.homelab.lan:5000` in the Makefile is **bhyve-specific** — override for aws,
e.g. `make release REGISTRY=10.0.10.101:5000`.

Individual targets: `make image` (ko build + push to `$(PUSH_REGISTRY)/helloworld:<tag>`),
`make apply` (renders the deploy.yaml image to `$(REGISTRY)/helloworld:$(TAG)` per provider, then applies — creates ns/Deployment/Service),
`make deploy` (`kubectl set image` + `rollout status`), `make check` (list pushed tags).
On a fresh cluster use `make release` (or `make apply` once before `make deploy`,
since `deploy` only re-points an existing Deployment).

## Tag scheme (and the dirty-tree caveat)

`TAG = git describe --tags --always --dirty`. A clean tagged commit → an immutable
ref like `helloworld:v1.1.0`; an uncommitted tree → `helloworld:v1.1.0-dirty`. **While the tree is
dirty the ref doesn't change between rebuilds**, so `kubectl set image` to the same
string is a no-op and nothing rolls. For real releases, commit + `git tag`; for
throwaway iteration, pass an explicit tag (`make release TAG=dev-$(date +%s)`).

## What it depends on (all provided by the layers below)

- **registry** up at `$(REGISTRY)` (bhyve `registry.homelab.lan:5000`) and nodes trusting its CA — from
  `make services` (registry) + `make cluster` (the `kube_registry_trust` role).
- **Cilium LB / edge** — the apps-pool is a **single IP** `<prefix>.240` (lb-pool.yaml
  start=stop=.240), prefix = `var.lb_pool_prefix`,
  advertised via **BGP** (`tf/k8s/lb-pool.yaml` + `tf/k8s/bgp.yaml`, applied by
  `make services`). `deploy.yaml`'s Service is `type: ClusterIP`: the `edge` reverse proxy
  owns `<prefix>.240` and routes `/hw` here. For a standalone deploy (no edge stack), set
  the Service to `type: LoadBalancer` to take `<prefix>.240` directly (aws 10.0.20.240).
- **build host CA trust** for `ko`/`curl` — `make trust` (separate from the nodes'
  containerd trust). It installs `$(REGISTRY_CA)`, which defaults to the **bhyve** CA
  (`../../registry/certs/bhyve/ca.crt`) — override for aws:
  `make trust REGISTRY_CA=../../registry/certs/aws/ca.crt`.

## Verify

```bash
kubectl -n app-helloworld get pods -o wide                                   # Running, 0 restarts
kubectl -n app-helloworld get deploy helloworld -o jsonpath='{..image}{"\n"}'  # helloworld:<your tag>
make check                                                               # tag present in the registry
curl -s http://172.23.20.240/hw/                                         # hello world (via edge on the VIP; direct if standalone)
curl -s https://dev.somegroup.net/hw/                                    # public path (reverse proxy -> edge -> helloworld)
./verify-spans.sh                                                        # OTLP spans reaching the agent
```

## Files

```
main.go        the app
deploy.yaml    Namespace + Deployment + Service (type: ClusterIP; edge fronts it at /hw. Use LoadBalancer for a standalone deploy)
Makefile       build / image / apply / deploy / release / trust / check
.ko.yaml       ko base image pin
verify-spans.sh  checks app spans reach the node-local OTel agent
```
