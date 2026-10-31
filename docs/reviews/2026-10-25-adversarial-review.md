# Adversarial review — 2026-10-25

Scope: Go apps (7), OpenTofu (tf/aws, tf/bhyve, tf/k8s), Ansible, build/CI/docs glue.
Method: four independent read-only reviewers, every HIGH and most MEDIUMs re-verified by hand
against source. Nothing in the repo was modified. Both clusters were torn down at the time,
so runtime claims are from code, not observation.

Tooling results: go vet clean (7/7), gofmt clean, ansible-lint production profile clean,
ansible syntax-check clean, tofu validate clean. govulncheck: 1 stdlib vuln in all apps,
5 extra x/net vulns in clusterinfo (see §Package versions).

## HIGH (fix first)

| # | Layer | Finding | Where | Fix |
|---|-------|---------|-------|-----|
| H1 | Go / versions | GO-2026-6809 (crypto/x509) in every app at committed toolchain go1.27.2; fixed in go1.27.3 which is released. clusterinfo also carries x/net v0.59.0 with 5 HTTP/2 CVEs (GO-2026-6617/6612/6611/6610/6603, fixed v0.60.0). | apps/*/go.mod `toolchain go1.27.2`; apps/clusterinfo/go.mod | `make -C apps go-update DEPS=` then `cd apps/clusterinfo && go get golang.org/x/net@latest && go mod tidy` |
| H2 | Go / safety | Unauthenticated memory-exhaustion chain: helloworld publishes r.URL.Path (up to ~1 MB) as Kafka key; visitcounter caps URL count (512) not key length; wsfeed holds all keys and seeds every browser with them. 512 × 100 KB paths OOMKills wsfeed (64Mi). | apps/helloworld/main.go:108, apps/visitcounter/main.go:30/137, apps/wsfeed/main.go:193 | Skip publish when len(path) > 256; `continue` on len(m.Key) > 256 in visitcounter |
| H3 | Go / robustness | One non-JSON Kafka value bricks the live feed forever: wsfeed wraps m.Value as json.RawMessage unvalidated, json.Marshal(envelope) fails for every client, reconnect loop until the compacted key is overwritten. Reachable by any producer bug or anyone on the plaintext, unauthenticated internal listener. | apps/wsfeed/main.go:167,198,270 | `if !json.Valid(m.Value) { log; continue }` in both consumers |
| H4 | Go / robustness | kafka-go Writer BatchTimeout left at default 1 s with synchronous single-message writes. Every sync write blocks ~1 s: clusterinfo's 500 ms ticker is defeated; visitcounter caps at ~1 visit/s and on restart replays the 24 h log at 1 event/s (10k visits ≈ 3 h of wrong counts). | apps/clusterinfo/main.go:106, apps/visitcounter/main.go:104 | `BatchTimeout: 10*time.Millisecond` (or BatchSize: 1); in visitcounter suppress publishes until `r.Lag()==0` |
| H5 | Tofu / safety | AWS FRR uses `bgp listen range` with `no bgp ebgp-requires-policy` and no prefix-list; bhyve has a prefix-list. Any peer in the range can advertise any prefix into the border. | tf/aws (frr template) | Add the same prefix-list/route-map as bhyve |
| H6 | Tofu / safety | Border SSH (22/tcp from 0.0.0.0/0) defaults to open on a fresh build. CORRECTED on fix pass: the reviewer's "broad ReplaceRoute scope" claim was wrong, iam-border.tf already restricts ec2:ReplaceRoute to the one route table ARN. The SSH default is a documented design choice (`make border-ssh-off` after build); downgraded to MEDIUM, no change made. | tf/aws/variables.tf:37, tf/aws/iam-border.tf | Run `make border-ssh-off` once the cluster is up (existing runbook) |
| H7 | Glue / usability | apps/Makefile evaluates `$(shell curl go.dev)` and `$(shell go env GOPATH)` at parse time, so even `make -n` or `make help` hits the network (~2.4 s) and the image's Go version depends on the build day. Direct `make -C apps/<app>` uses go.mod's toolchain instead: two results for one tag. | apps/Makefile:32 | Make GO_LATEST lazy (`=` not `:=` plus target-scoped), or pin in a file refreshed by `go-update` |
| H8 | Glue / docs | docs §6 rebuild path omits aws-kubeconfig creation and the tfvars copies; tofu calls lack `-input=false`. deploy.yml cannot work on a fresh self-hosted checkout (gitignored inputs, clean checkout). | docs/README.md §6, .github/workflows/deploy.yml | Document the gitignored inputs explicitly; either delete deploy.yml or make it a documented manual trigger that expects a pre-seeded runner |

## MEDIUM

### Safety
- M1 Go: wsfeed has no cap on concurrent WebSocket connections; each costs a goroutine, 256-envelope buffer and a 32 KiB deflate window (context takeover) inside 64Mi. Fix: counter + 503 above ~500, or `CompressionNoContextTakeover`. apps/wsfeed/main.go:211
- M2 Go: clusterinfo publishes kernel/kubelet/OS versions, image tags, namespaces, LB VIPs and pool CIDRs, BGP peer addresses to the public page. Intentional, but drop `kernel`, down-peer addresses and pool ranges at least. RBAC grants `get` it never uses. apps/clusterinfo/main.go:364-385,486-511,593-690; deploy.yaml:17-33
- M3 Go: no `automountServiceAccountToken: false` on helloworld/static/edge/wsfeed/visitcounter. One line each.
- M4 Tofu: registry unauthenticated on hostNetwork cp1:5000. tf/k8s/registry.tf
- M5 Tofu: Kafka internal listener tls:false, no auth; combined with H3 this is the attack path. tf/k8s/kafka-cluster.yaml:117-120
- M6 Tofu: Honeycomb key and registry certs in cleartext local state. Fix: `terraform { encryption { ... } }` (OpenTofu 1.7+). tf/k8s
- M7 Tofu: cost-reader role attached to every worker with IMDS hop limit 2; any pod can assume it. Fix: dedicated node group or IRSA-style projection.
- M8 Ansible: `kubeadm init` output registered without `no_log`; bootstrap token and cert key land in Ansible output/logs. ansible/bootstrap.yml:13
- M9 Ansible: teardown guard `confirm_teardown` is hardcoded `=yes` by the Makefile (`make teardown` and `recreate`). Makefile:144
- M10 Ansible: Play 0 (localhost) inherits `become = True` from ansible.cfg; runs include_vars/asserts as root for no reason. ansible/upgrade.yml, bootstrap.yml

### Robustness
- M11 Go: wsfeed "clean drain" never runs: Shutdown is in a goroutine, main returns first; browsers get 1006. apps/wsfeed/main.go:310-319
- M12 Go: slow-client drop loses event-driven updates permanently (no resync). apps/wsfeed/main.go:87-92
- M13 Go: clusterinfo `ServerVersion()` has no ctx and rest.Config has no Timeout; hung apiserver blocks poll until liveness kills the pod. Fix: `cfg.Timeout = 5*time.Second`. apps/clusterinfo/main.go:600
- M14 Tofu: `make services` uses `-target` on `generate`, which pulls instance modules; post-teardown it could recreate the fleet. tf/k8s
- M15 Tofu: no `depends_on` Cilium for other helm releases; first apply races CNI. tf/k8s/helm_*.tf
- M16 Ansible: containerd config has no `SystemdCgroup = true` (cgroupfs with systemd kubelet). roles/kube_containerd/templates/config.toml.j2
- M17 Ansible: apt repo minor (`kube_apt_repos_k8s_minor: v1.36`) and package pin (`kube_packages_version: 1.36.5-1.1`) are independent variables with no assert tying them. Derive one from the other.
- M18 Ansible: drain in upgrade_kubelet.yml has no rescue/uncordon; a failed install leaves the node cordoned.
- M19 Glue: `make tunnel` lacks `-o ExitOnForwardFailure=yes`; a stale tunnel silently succeeds. Makefile
- M20 Glue: `-dirty` image tag is not guarded; a dirty tree builds a tag the registry already has (documented as a no-op, not enforced).

### Usability / consistency / duplication
- M21 Go: six verbatim `initTracer` copies, five `env()`, three `serveHealth` with three different semantics, two `newReader`, with silent drift (only clusterinfo samples; edge compares errors with `!=`; visitcounter has no .ko.yaml; helloworld/Makefile has targets the others lack). Fix: `apps/internal/{otelinit,envx,health,kafkax}` via go.work.
- M22 Glue: `make help` prints lines 3–34 but the header ends at 39; `make -C apps awscost` fails (not in APPS) though go-update covers it.
- M23 Glue/CI: `go install ko@latest` unpinned; no job timeouts; CI has no ansible-lint, no tofu plan, no ko build, tests for only 2 of 7 apps.
- M24 Docs: tf/k8s/README still says apply creates network-policies; ansible/README has old pins; docs/README has duplicate "## 8."; GOTOOLCHAIN statement inverted; "Status: applied and running" while torn down; helloworld/Makefile + README say apply creates the Namespace (moved to apps/namespaces.yaml); app.js comments still describe JetStream/NATS/MQTT and "72h" (MAX_AGE_MS is 7 d).
- M25 Ansible: duplicate machine-id logic and handlers across roles; stale node counts in README; cri-tools unpinned.

## LOW
- L1 Go: helloworld visit writer is Async with RequireNone, so the Completion callback only ever sees connection errors. Set RequireOne.
- L2 Go: no IdleTimeout/WriteTimeout on any http.Server (edge is the public door).
- L3 Go: `imagePullPolicy: Always` on immutable tags makes cold boot depend on the in-cluster registry. IfNotPresent.
- L4 Go: awscost binds :9782 on hostNetwork on every interface. ADDR=127.0.0.1:9782.
- L5 Go: static logs raw decoded path (log injection) and FileServer renders directory listings.
- L6 Go: awscost sync Float64Gauge + cumulative export re-exports terminated instances' last cost hourly until restart; QUERIES.md MAX totals over-count after scale-down. ObservableGauge or delta temporality.
- L7 Go: envelope `subject == topic` always; no schema/version field; browser special-cases cluster/versions.
- L8 Go: awscost Pricer.mu is dead (single goroutine); visitcounter header claims window semantics it never enforces (no decrement on expiry).
- L9 Ansible: no retries on apt/kubeadm network steps; admin.conf not refreshed after upgrade.
- L10 Glue: apps/Makefile clusterinfo is the only module `go get -u` skipped (x/net 0.59, x/sys 0.48, compress 1.19.1, lz4 4.1.15, prom 1.24.1 vs 0.61/0.49/1.20.3/4.1.33/1.25.0 elsewhere).

## Package versions (2026-10-25)

| Component | Pinned | Available | Note |
|-----------|--------|-----------|------|
| Go toolchain | go1.27.2 | go1.27.3 | fixes GO-2026-6809; `make -C apps go-update DEPS=` |
| golang.org/x/net (clusterinfo) | v0.59.0 | v0.61.0 | 5 HTTP/2 CVEs fixed in 0.60.0 |
| Kubernetes | 1.36.5 | 1.37.1 | held: Cilium 1.20 supports ≤1.36; Cilium 1.21 pre-release |
| Cilium | 1.20.3 | 1.20.4 | patch, safe |
| Strimzi / Kafka | 1.3.0 / 4.3.1 | current | — |
| TopoLVM | 17.3.0 | current | — |
| metrics-server chart | 3.14.0 | current | — |
| otel-collector chart | 0.176.0 | 0.177.0 | patch |
| tofu provider aws | 6.69.1 | 6.70.0 | patch |
| tofu provider kubernetes | 2.38.0 | 3.4.0 | major, deferred |
| tofu provider helm | 3.3.0 | current | — |
| OpenTofu | 1.13.1 | current | — |
| Ansible / ansible-lint | 2.21.5 / 26.10.1 | current | — |
| containerd (apt) | pinned | ~24.04.4 | patch |
| cilium-cli | v0.20.1 | current | — |
| actions/checkout | v7 | v7.0.1 | ok |
| actions/setup-go | v7 | v7.0.0 | ok |
| setup-opentofu | v2 | v2.0.2 | ok |
| ko | @latest (unpinned) | v0.19.1 | pin |
| aws-sdk-go-v2 (awscost) | 1.47.3 + services | 1.47.4 + minor service bumps | `go get -u` |
| Host go env | GOTOOLCHAIN=go1.27.2 pinned | — | apps/Makefile export overrides; set `go env -w GOTOOLCHAIN=auto` |

## Three best things per layer
- Go: uniform pod hardening (RuntimeDefault seccomp, non-root, RO FS, drop ALL, limits, PDBs, topology spread); per-app health semantics are deliberate (heartbeat-before-Kafka, liveness-only visitcounter); awscost is cleanly testable (narrow interfaces, pure Estimator, honest QUERIES.md).
- Tofu: bhyve/aws symmetry with one `generate` layer; `make border-ssh-on|off` toggle; EIP excluded from destroy so DNS survives teardown.
- Ansible: capability-flag roles; upgrade.yml derives target from one variable and asserts ≤1 minor per run; lint-clean at production profile.
- Glue: build-time newest Go without Dependabot; ko with git-describe tags; docs that record what was proven on which date.

## Suggested order
1. H1 (10 minutes, no risk) → 2. H2 + H3 + H4 together (one PR, three small guards) → 3. H5 + H6 before the next AWS rebuild → 4. H7 + M22 (Makefile) → 5. M8/M9/M10/M16/M17/M18 (Ansible, before next `make upgrade-k8s`) → 6. M21 dedup once the three Go fixes have landed → 7. docs sweep (H8, M24).
