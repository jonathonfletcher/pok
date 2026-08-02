# `tf/k8s/` — OpenTofu for the in-cluster services layer

Manages the **in-cluster services layer** (API endpoint is provider-specific
`var.k8s_service_host` — bhyve `k8s.homelab.lan:6443`, aws `k8s.demo.internal:6443`):
CNI, LoadBalancer config, private registry, storage CSI, observability
collectors + credential. It does **not** manage cluster bootstrap
(kubeadm, etcd, node OS, containerd, DNS) — that lives below the Kubernetes API in
Ansible (`../../ansible`). See `../../docs/README.md` §2–3.

**One parameterized root for all providers** — bhyve and aws share every
resource, differing only by a per-provider `vars/<provider>.tfvars.json` (emitted by
`tf/<provider>`) passed as `-var-file`. The provider-specific variables have
**no defaults**, so bare `tofu plan`/`apply` fail — always pass
`-var-file=vars/<provider>.tfvars.json`, or use `make ... INFRA_PROVIDER=<provider>`.

---

## Modes of operation

Three modes, all via the `Makefile` (usually the top-level `../Makefile`'s
`make services`), which selects the var-file from `INFRA_PROVIDER` (default `aws`):

| Mode | Command | When |
|------|---------|------|
| **Apply** (fresh or steady state) | `make apply INFRA_PROVIDER=<p>` | install/reconcile — one pass, no stages |
| **Verify / drift check** | `make plan INFRA_PROVIDER=<p>` (or `make validate`) | anytime — `No changes` = in sync |
| **Adopt a live cluster** into fresh state | `tofu init` + imports | rebuilding lost state (import-first) |

`make apply` is a **single pass** — no `kubernetes_manifest` resources, so
nothing needs a plan-time CRD. It runs `tofu apply -var-file=...` (which also renders
the CR files, see `rendered.tf`) then kubectl-applies the CRs whose CRDs that apply just
installed (Cilium LB pool, BGP peering, Kafka/KRaft cluster):

```makefile
# TFSTATE/KCFG are per-provider (aws -> aws.tfstate + aws-kubeconfig; else terraform.tfstate
# + ~/.kube/config). KCTL := KUBECONFIG=$(KCFG) kubectl, so every kubectl step below runs
# against the selected provider's cluster.
apply:
	tofu apply -state=$(TFSTATE) -var-file=$(TFVARFILE) -var 'kubeconfig=$(KCFG)'
	$(KCTL) wait --for=condition=established crd/ciliumloadbalancerippools.cilium.io ...  # avoid a fresh-cluster race
	$(KCTL) apply -f lb-pool.yaml        # Cilium LB pools (apps + kafka)
	$(KCTL) wait --for=condition=established crd/ciliumbgpclusterconfigs.cilium.io ...    # BGPv2 CRDs (bgpControlPlane on)
	$(KCTL) apply -f bgp.yaml            # BGP peering + advertisement (peers the router)
	$(KCTL) wait --for=condition=established crd/kafkas.kafka.strimzi.io ...             # Strimzi CRDs from helm_kafka.tf
	$(KCTL) apply -f kafka-cluster.yaml  # KafkaNodePool controller + broker + Kafka
	$(KCTL) apply -f network-policies.yaml  # Cilium default-deny + allow policies
```

The Cilium LB objects (`lb-pool.yaml`, `bgp.yaml`), Kafka CRs (`kafka-cluster.yaml`), and
network policies (`network-policies.yaml`) are plain kubectl, **not** OpenTofu resources — that keeps the apply single-pass.
Trade-off: tofu doesn't drift-track those static objects (the Strimzi *operator* itself,
and the *rendering* of `lb-pool.yaml`/`bgp.yaml`, are tofu).

---

## Files & what each declares (also in each file's header)

| File | Declares | Purpose |
|------|----------|---------|
| `providers.tf` | kubernetes + helm providers | auth via `var.kubeconfig` (no cloud creds) |
| `versions.tf` | `required_version` + provider constraints | kubernetes `~> 2.38`, helm `~> 3.0`, local `~> 2.0` (exact versions locked in `.terraform.lock.hcl`) |
| `variables.tf` | `kubeconfig`, `honeycomb_api_key` (sensitive), + provider-specific vars (below) | inputs |
| `connectivity.tf` | `data.kubernetes_namespace_v1.kube_system` + output | read-only auth/connectivity probe |
| `namespaces.tf` | `kubernetes_namespace_v1.{honeycomb,kafka}` | ns for the collectors + secret + otel service; kafka ns |
| `helm_cilium.tf` | `helm_release.cilium` | Cilium CNI — load-bearing (`prevent_destroy`) |
| `helm_otel_collectors.tf` | `helm_release` otel_collector_agent + _cluster | two OTel collectors → Honeycomb |
| `helm_metrics_server.tf` | `helm_release.metrics_server` | metrics-server (Metrics API for clusterinfo / `kubectl top`) |
| `helm_kafka.tf` | `helm_release.strimzi` | Strimzi Kafka operator (watches `kafka` ns) |
| `kafka-cluster.yaml` | `KafkaNodePool` controller + broker, `Kafka` main | **kubectl-applied** (not tofu) — KRaft cluster CRs |
| `helm_topolvm.tf` | `helm_release.topolvm` | TopoLVM CSI (consumes the Ansible `topolvm-vg`) |
| `secret.tf` | `kubernetes_secret_v1.honeycomb` | Honeycomb API key the collectors read |
| `otel-agent-svc.tf` | `kubernetes_service_v1.otel_agent` | node-local OTLP endpoint (Local traffic policy) |
| `registry.tf` | `kubernetes_{namespace,secret,deployment}_v1` (registry), all `count = var.manage_registry ? 1 : 0` | private `registry:2`, hostNetwork, pinned to `var.registry_node` (bhyve k8sm1, aws cp1), TLS, hostPath |
| `rendered.tf` | `local_file.{lb_pool,bgp}` | renders `lb-pool.yaml` + `bgp.yaml` from `templates/` using provider vars (generated, gitignored) |
| `templates/lb-pool.yaml.tftpl` | `CiliumLoadBalancerIPPool` template | source for `lb-pool.yaml`; pool CIDR `${lb_pool_prefix}.0/24` |
| `templates/bgp.yaml.tftpl` | `CiliumBGPClusterConfig` + `PeerConfig` + `Advertisement` template | source for `bgp.yaml`; ASNs/peer parameterized |
| `lb-pool.yaml` | `CiliumLoadBalancerIPPool` (`apps-pool`, single IP `.240`) | **generated + kubectl-applied** — `${lb_pool_prefix}.0/24`; serviceSelector excludes Strimzi |
| `bgp.yaml` | `CiliumBGPClusterConfig` + `PeerConfig` + `Advertisement` | **generated + kubectl-applied** — nodes peer the provider router, advertise LB VIPs |
| `network-policies.yaml` | `CiliumNetworkPolicy` (default-deny + allow) | **kubectl-applied** (not tofu) — per-namespace default-deny plus the allow rules |
| `Makefile` | init / plan / apply / validate (via `-var-file`) | the modes above |
| `values/cilium.values.yaml.tftpl` | Cilium values template | `templatefile()` interpolating `${cilium_devices}` + `${k8s_service_host}` |
| `values/{otel-collector,otel-collector-cluster,kafka-operator,topolvm}.values.yaml` | verbatim `helm get values` per release | zero-diff source of truth for those Helm charts |
| `vars/<provider>.tfvars.json` | per-provider variable values | **GITIGNORED** — emitted by `tf/<provider>`; the `-var-file` |
| `terraform.tfvars.json` | real secret values (honeycomb key) | **GITIGNORED** — never commit |

**Provider-specific variables** (declared in `variables.tf` / `registry.tf`, **no defaults**,
supplied via `vars/<provider>.tfvars.json`): `cilium_devices`, `k8s_service_host`,
`lb_pool_prefix`, `bgp_local_asn`, `bgp_peer_asn`, `bgp_peer_address`, `bgp_cluster_name`,
`bgp_peer_name`, `manage_registry`, `registry_node`, `registry_certs_dir`.

**Convention:** everything in OpenTofu uses typed `_v1` resources — **no
`kubernetes_manifest`**. CRD objects (the two Cilium LB CRs, the BGP CRs, the
Kafka/KRaft CRs) live in `lb-pool.yaml` / `bgp.yaml` / `kafka-cluster.yaml`, applied
by kubectl to keep `tofu apply` single-pass.

Not managed here: the `helloworld` app — it has its own git-tag deploy flow in
`../../apps/helloworld` (`make release`); OpenTofu would fight that workflow.

---

## Prerequisites

- **OpenTofu** ≥ 1.12 (developed on v1.12.5): `tofu version`.
- **kubectl admin access** via `~/.kube/config` (override with `-var kubeconfig=/path`).
  Confirm first: `kubectl get nodes`.
- **`vars/<provider>.tfvars.json`** — per-provider inputs, emitted by `tf/<provider>`
  (gitignored). Bare tofu commands fail without it.
- **`terraform.tfvars.json`** with the Honeycomb key (gitignored):
  ```bash
  cp terraform.tfvars.json.example terraform.tfvars.json   # then set "honeycomb_api_key": "hcaik_..."
  ```
- Providers are pinned in `.terraform.lock.hcl` (commit it) and installed by `tofu init`.

State is **local** and **per-provider** — the `Makefile` passes `-state=$(TFSTATE)`, where
`TFSTATE` is `aws.tfstate` for `INFRA_PROVIDER=aws` and `terraform.tfstate` otherwise, so
aws and bhyve never share a state file (both gitignored). State contains rendered Secret
data — never commit it. Migrate to an encrypted remote backend for team use.

## Verify / drift check (`make plan`)

`tofu plan -var-file=vars/<provider>.tfvars.json` reads every managed object live and
diffs it against config:

- **`No changes.`** → services layer exactly as declared (healthy steady state).
- **`0 to add, N to change, 0 to destroy`** → drift (a manual `helm upgrade`/`kubectl edit`).
  Reconcile with `make apply` (push config back), or edit the `.tf` /
  `values/*` to accept it, then re-plan to `No changes`.
- **`must be replaced` / `destroy`** → stop. `prevent_destroy` guards Cilium, the
  honeycomb Secret, and the honeycomb namespace — `apply` errors rather than delete them.

Scripted (exit 0 = in sync, 2 = drift): `tofu plan -detailed-exitcode -var-file=vars/<provider>.tfvars.json`.

> **Cilium is the running dataplane.** A `helm upgrade` restarts it and can disrupt
> networking. `prevent_destroy` (`helm_cilium.tf:31`) makes `apply` error rather than
> replace the release.

## Adopt a live cluster into fresh state (import-first)

If `terraform.tfstate` is lost, re-import (config is already written; imports are
idempotent). The registry resources are `count`-guarded, so import them at indexed
addresses:

```bash
tofu init
tofu import helm_release.cilium                 kube-system/cilium
tofu import helm_release.otel_collector_agent   honeycomb/otel-collector
tofu import helm_release.otel_collector_cluster honeycomb/otel-collector-cluster
tofu import helm_release.topolvm                topolvm-system/topolvm
tofu import helm_release.strimzi                kafka/strimzi-kafka-operator
tofu import kubernetes_namespace_v1.kafka          kafka
tofu import kubernetes_namespace_v1.honeycomb      honeycomb
tofu import kubernetes_secret_v1.honeycomb         honeycomb/honeycomb
tofu import kubernetes_service_v1.otel_agent       honeycomb/otel-agent
# registry (count-guarded -> [0]):
tofu import 'kubernetes_namespace_v1.registry[0]'  registry
tofu import 'kubernetes_secret_v1.registry_tls[0]' registry/registry-tls
tofu import 'kubernetes_deployment_v1.registry[0]' registry/registry
tofu plan -state=<provider>.tfstate -var-file=vars/<provider>.tfvars.json   # confirm "No changes"
```

Pass `-state=<provider>.tfstate` (aws → `aws.tfstate`, else `terraform.tfstate`) **and**
`-var-file=vars/<provider>.tfvars.json` to every `import`/`plan` above — otherwise they hit
the default `terraform.tfstate`, not the provider's state.
