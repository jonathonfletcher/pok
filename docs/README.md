# Kubernetes cluster IaC — **bhyve + AWS**

A parameterized Infrastructure-as-Code codebase that stands up a self-hosted
**kubeadm Kubernetes cluster and its in-cluster services across multiple infrastructure
providers**. The cluster and services layers share one codebase; per-provider differences
are config values and capability flags.

Two providers today:
- **bhyve** — home lab: Ubuntu VMs on a FreeBSD/`vm-bhyve` host (`homelab.lan`).
- **aws** — demo: arm64 EC2 instances in a purpose-built VPC.

Change history: **[docs/CHANGELOG.md](CHANGELOG.md)**.

> Values below reflect the running clusters.

---

## 1. What the code does — four layers

The same four layers build a cluster on any provider, bottom to top. The provider is
selected once (§3) and threaded through all four.

| Layer | Tool | Directory | Responsibility |
|---|---|---|---|
| **infra** | OpenTofu | `tf/$(INFRA_PROVIDER)` | provision the machines/network; **emit** the Ansible inventory + a per-provider tfvars for `tf/k8s` |
| **cluster** | Ansible | `ansible/` | node OS prep, kubeadm init+joins, LVM/storage — everything *below* the API |
| **services** | OpenTofu | `tf/k8s` | the in-cluster services layer *above* the API — **one parameterized root for all providers** |
| **app** | ko + kubectl | `apps/` | build/push/deploy all apps (`helloworld` + the live-feed stack: `static`/`wsfeed`/`clusterinfo`/`visitcounter`/`edge`) |

- **`tf/<provider>` (infra):** on **bhyve** a *stub* — emits the hard-coded
  inventory and tfvars (no VM API driven; VMs pre-exist on the bhyve host).
  On **aws** *real* — VPC, three subnets, arm64 instances, the border VM (FRR +
  Caddy), security groups. **Both** emit `ansible/inventory/<provider>/hosts.ini` and
  `tf/k8s/vars/<provider>.tfvars.json`, so the layers above learn provider details only
  through those two generated files.
- **`ansible/` (cluster):** builtin-only playbooks — `prepare.yml` → `os_prep.yml` →
  `bootstrap.yml` → `storage.yml`. Provider-specific values live in
  `ansible/inventory/<provider>/group_vars/all.yml` (kubeadm endpoint/CIDRs + capability
  flags — §4).
- **`tf/k8s` (services):** installs Cilium (CNI + BGP), the private registry, TopoLVM, the
  OpenTelemetry collectors (→ Honeycomb), and the Strimzi Kafka operator, then `kubectl
  apply`s the CRs whose CRDs it just installed (LB pools, BGP, Kafka). Consumes
  `-var-file=vars/$(INFRA_PROVIDER).tfvars.json` and keeps a **separate state file per
  provider** (`terraform.tfstate` for bhyve, `aws.tfstate` for aws).

**The boundary:** OpenTofu owns everything *above* the Kubernetes API (`tf/k8s`) and the
infra *below the OS* (`tf/<provider>`); Ansible owns the OS/kubeadm/LVM band between.

---

## 2. Supported infra providers

| | **bhyve** (home lab) | **aws** (demo) |
|---|---|---|
| Machines | 3 control-plane + 7 workers, `vm-bhyve` on FreeBSD 15.1 | 3 control-plane + 3 workers, arm64 EC2 `t4g.medium` (`demo`, eu-west-1) |
| Node names / IPs | `k8sm1–3` / `k8sw1–7`, `172.23.10.150–.159` | `cp1–3` `10.0.10.101–.103` / `w1–3` `10.0.10.111–.113` |
| Kubernetes | v1.36.3, Cilium 1.19.6 (KPR, vxlan) | v1.36.3, Cilium 1.19.6 (KPR, vxlan) |
| API endpoint | `k8s.homelab.lan:6443` (DNS round-robin over the 3 CPs; not health-checked) | `k8s.demo.internal:6443` (Route 53 private-zone round-robin over the 3 CPs; not health-checked; external access via SSH tunnel) |
| LB model | **Cilium L3 / BGP** — VIP pool advertised by all nodes | **Cilium L3 / BGP** — same |
| BGP router / ASNs | OpenBSD gateway `172.23.10.100` (AS65000 ⇄ nodes AS65010) | border VM FRR `10.0.100.101` (AS65001 ⇄ nodes AS65020) |
| LB pool | `172.23.20.0/24` | `10.0.20.0/24` |
| LB data path | **active/active ECMP** across live nodes (real router) | **active/standby + failover** — VPC can't ECMP (see §5.2) |
| Registry | self-hosted on `k8sm1`, `registry.homelab.lan` (DNS+TLS) | self-hosted on `cp1`, IP-SAN cert (no ECR — see §5.1) |
| DNS zone | generated `homelab.lan` fragments (`manage_dns_zone=true`) | Route 53 **private zone `demo.internal`** — per-node A records + `k8s.` HA endpoint (`tf/aws/dns.tf`); `manage_dns_zone=false` (no in-cluster zone gen) |
| Storage backing | grow `disk0` → `ubuntu-data-lv` → `topolvm-vg` | dedicated data disk `nvme1n1` → `topolvm-vg` (workers only) |

Common to both: containerd (Ubuntu apt, **no Docker**), TopoLVM CSI (default SC
`topolvm-provisioner`), OTel→Honeycomb, Strimzi Kafka (KRaft, controller×3 + broker×3).


---

## 3. Selecting a provider

The active provider is `INFRA_PROVIDER`, read from **`platform.mk`** (git-ignored; copy
`platform.mk.example`). It drives `INFRA_DIR=tf/$(INFRA_PROVIDER)` and
`INVENTORY=inventory/$(INFRA_PROVIDER)/hosts.ini` throughout the Makefile.

```bash
make platform         # show the active provider
make use-bhyve        # switch active provider -> bhyve   (writes platform.mk)
make use-aws          # switch active provider -> aws
make infra INFRA_PROVIDER=aws   # or override per-invocation
```

---

## 4. How one `tf/k8s` + `ansible/` serves every provider

Provider differences are **only** data, in two categories:

- **Category A — config values.** Per-provider values in `tf/k8s/vars/<provider>.tfvars.json`
  (emitted by `tf/<provider>`) and `ansible/inventory/<provider>/group_vars/all.yml`:
  `cilium_devices` (`enp0s5`/`ens5`), `k8s_service_host`, `lb_pool_prefix`, the BGP ASNs +
  peer address/name, `registry_node`, `registry_certs_dir`, pod/service CIDRs, the API
  endpoint.
- **Category B — capability flags.** Booleans that toggle whole resources/roles on or off:
  - `manage_registry` — self-host the private registry (both `true` today).
  - `manage_dns_zone` — generate `homelab.lan` zone fragments (bhyve `true`, aws `false`).
  - `manage_data_lv` — grow `disk0` into a data LV first (bhyve `true`); aws has a
    dedicated disk so it is `false`, and `node_topolvm_vg` no-ops on hosts without the
    backing device (the AWS control planes).

Hence **one** `tf/k8s` and **one** `ansible/`, not a fork per provider.

---

## 5. Design compromises (demo scope)

On AWS, self-hosted equivalents replace managed AWS services (registry, LB) to reduce cost
and keep parity with bhyve, at the expense of managed HA/ops.

### 5.1 Image registry — self-hosted on `cp1`, **not** ECR
- **What:** the private CNCF Distribution registry runs on the primary control plane
  (`cp1`), as bhyve runs it on `k8sm1` — addressed by cp1's private IP with an
  IP-SAN cert (`manage_registry=true`, `registry_node=cp1`).
- **Why not ECR:** Elastic Container Registry is a managed, paid, IAM-gated service; for a
  demo the IAM wiring, per-GB/API cost, and lifecycle add complexity.
  Self-hosting reuses the bhyve mechanism verbatim and creates **zero new AWS resources**.
- **Cost:** single-node registry — no HA. If `cp1` is down, *new* image
  pulls fail (already-running pods are unaffected).

### 5.2 LoadBalancer — Cilium L3 BGP, **active/standby with automatic failover**, not an NLB
- **The model (both providers):** Cilium **L3 LoadBalancer**. Every node advertises
  each LoadBalancer VIP (from `10.0.20.0/24` on AWS, `172.23.20.0/24` on bhyve) via BGP —
  no NodePorts, no L2/ARP.
- **bhyve active/active:** the OpenBSD gateway is a real BGP router, so it
  installs each VIP with **ECMP across all live nodes** and withdraws any node whose BGP
  session drops.
- **The AWS gap:** the "router" between the border and the nodes is the **VPC fabric**,
  which does **not** speak BGP and whose route tables **cannot ECMP** a prefix (one route =
  one target ENI); bhyve's active/active has no native AWS equivalent. (AWS's own
  "multipath VIP" pattern achieves spread via *multiple route tables keyed by source subnet*
  plus a programmatic next-hop updater; the
  source-subnet trick buys nothing for our **single** ingress source, the border.)
- **The choice — active/standby with automatic failover:** a BGP-aware agent on the border
  (which already peers every node's Cilium BGP) rewrites the VPC route
  `10.0.20.0/24 → <a live node's ENI>` whenever a node's BGP session drops. This is the
  AWS-documented "BGP utility that programmatically provisions next hops." The nodes
  already run `source_dest_check=false` so the elected ENI accepts VIP-dst traffic;
  Cilium's eBPF LB then DNATs to the backing pod on **any** node.
- **Why not an NLB:** an NLB gives per-flow active/active across all nodes but is a managed,
  paid resource with its own target-group/health-check lifecycle; the BGP failover model
  keeps one ingress node at a time and reuses the bhyve LB model.
- **Cost:** no per-flow load spread across nodes (one node carries the
  pool at a time); a failover briefly interrupts in-flight connections while the route is
  repointed.
- **Status:** built and verified — the agent (`tf/aws/border/lb-failover.sh` + IAM in
  `iam-border.tf`) runs on the border and repoints `aws_route.cilium_lb` on BGP session
  loss (`network_interface_id` is `ignore_changes`d so tofu doesn't fight it).

### 5.3 Other demo-scope caveats
- **Registry is a SPOF on cp1.** Single `registry:2` replica, hostNetwork on the primary
  control plane — so registry load also lands on a CP (also the API/etcd host).
  For production, move it to a worker (`registry_node=w1`) or a managed registry.
- **Build-host residue for AWS pushes.** Building/pushing helloworld to the in-VPC registry
  needs, on the build host: a `10.0.10.101/32` **lo alias** (so the IP-SAN cert matches the
  tunnel) and the aws registry CA in the system trust store. The alias *shadows* the real
  cp1 IP from this host — only matters when building images for AWS. (See
  `tf/aws/README.md` §Access.)
- **Kafka storage headroom is tight.** Each worker's 20 GB `topolvm-vg` holds one broker
  (10Gi) + one controller (2Gi) ≈ 12Gi. Larger replicas/volumes require raising
  `worker_datadisk_size`.
- **Local tofu state holds secrets in cleartext.** `aws.tfstate` / `terraform.tfvars.json`
  are git-ignored but unencrypted on disk (no S3+KMS backend yet — `versions.tf` TODO).
  Not suitable for shared/production use.

---

## 6. Recreate from scratch

One entrypoint — the top-level `Makefile`. Set the provider (§3), then in order:

```bash
cd <repo-root>
make use-aws            # or make use-bhyve
make infra              # OpenTofu tf/<provider>: provision + emit inventory/tfvars
make cluster            # Ansible: OS prep -> kubeadm -> storage
make services           # OpenTofu tf/k8s: Cilium + registry + TopoLVM + OTel + Kafka + CRs
make trust              # trust the (re)generated registry CA on THIS build host (sudo)
make app                # ko build + push + deploy helloworld
make verify             # nodes + tofu drift + app reachability
#  bhyve only: make recreate  (= teardown -> infra -> cluster -> services -> trust -> app)
```

- **bhyve `make cluster` has a MANUAL GATE:** `prepare.yml` generates DNS fragments and the
  registry PKI, then **pauses** for you to (1) install `ansible/dns/*.k8s.zone` on
  `gateway.homelab.lan` (`$INCLUDE`, bump SOA, reload named) and (2) grow each `disk0`
  +16 G and reboot. `kubeadm init` fails if `k8s.homelab.lan` doesn't resolve.
- **aws** has no DNS gate (`manage_dns_zone=false`), but `make services`/`app`/`verify`
  need the **SSH tunnel** up first (the private API + registry are off-VPC). Order:
  ```bash
  make use-aws
  make infra
  make cluster                              # bhyve manual gate is auto-skipped for aws
  sudo ip addr add 10.0.10.101/32 dev lo    # once — registry forward's IP-SAN cert
  make tunnel                               # API 16443 + registry 15000, via the border
  make services                             # uses aws.tfstate + tf/aws/aws-kubeconfig
  make trust && make app                    # arm64; push :15000, nodes pull :5000
  make verify                               # curls https://pok.somegroup.net/
  ```
  The endpoint is `k8s.demo.internal:6443` (Route 53 HA); `aws-kubeconfig` uses the tunnel
  (`server: https://127.0.0.1:16443`, `tls-server-name: k8s.demo.internal`). Nodes: ProxyJump
  via the border. `cluster`/`services`/`app`/`verify` are provider-aware in the Makefiles —
  only the tunnel + one-time lo alias are manual prereqs.
- **`make teardown`** is provider-branched: bhyve does a kubeadm reset + wipes `tf/k8s`
  state and registry PKI, then the (no-op) `tf/bhyve teardown`; aws runs `tofu destroy`
  keeping the Elastic IP (`prevent_destroy`, so the external DNS record survives rebuilds).

Layer detail: **[ansible/README.md](../ansible/README.md)**, **[tf/k8s/README.md](../tf/k8s/README.md)**,
**[tf/aws/README.md](../tf/aws/README.md)**, **[apps/helloworld/README.md](../apps/helloworld/README.md)**.

---

## 7. Component inventory & who manages it

| Component | Version | Managed by | Where |
|---|---|---|---|
| kubeadm control plane / etcd / kubelet | v1.36.3 | **Ansible** `bootstrap.yml` | `ansible/bootstrap.yml` |
| Node OS, hostnames, containerd, `certs.d`, LVM | — | **Ansible** roles | `ansible/roles/*` |
| DNS records (bhyve only) | — | **Ansible** `infra_dns_zone` (`manage_dns_zone`) | `ansible/roles/infra_dns_zone` |
| Cilium CNI + BGP control-plane | 1.19.6 | **OpenTofu** `helm_release` | `tf/k8s/helm_cilium.tf` |
| Cilium LB pools / BGP peering / advertisement | — | **kubectl** (`make services`) | `tf/k8s/{lb-pool,bgp}.yaml` (rendered) |
| Cilium network policies (default-deny + allow) | — | **kubectl** (`make services`) | `tf/k8s/network-policies.yaml` |
| OTel collectors (agent + cluster) | chart 0.165.0 | **OpenTofu** `helm_release` | `tf/k8s/helm_otel_collectors.tf` |
| metrics-server | chart 3.12.2 | **OpenTofu** `helm_release` | `tf/k8s/helm_metrics_server.tf` |
| Honeycomb API-key Secret | — | **OpenTofu** `kubernetes_secret` | `tf/k8s/secret.tf` |
| Private registry (Distribution) | `registry:2` | **OpenTofu** (`manage_registry`) | `tf/k8s/registry.tf` |
| TopoLVM (CSI) | chart 17.0.0 | **OpenTofu** `helm_release` | `tf/k8s/topolvm.tf` |
| Strimzi Kafka operator + KRaft cluster | chart 1.1.0 | **OpenTofu** + **kubectl** | `tf/k8s/helm_kafka.tf`, `kafka-cluster.yaml` |
| Per-node data LV + `topolvm-vg` | — | **Ansible** (`manage_data_lv`) | `ansible/roles/{node_data_lv,node_topolvm_vg}` |
| AWS VPC / subnets / instances / border | — | **OpenTofu** | `tf/aws/*` |
| apps | git-tagged | `make app` | `apps/` (`helloworld` + live-feed stack) |

---

## 8. Key gotchas (detail in the CHANGELOG)
- **AWS VPC can't ECMP a route** and only routes to real ENIs — the Cilium LB pool
  (`10.0.20.0/24`) has no ENIs, so LB traffic needs a VPC route `→ node ENI` +
  `source_dest_check=false` (both in `tf/aws`); active/active isn't possible (§5.2).
- **Cilium LB IPs are L3/BGP-advertised** from a *routed* prefix off the node subnet —
  L2/ARP can't route a VIP inside the node subnet and doesn't port to AWS. **Cilium pins
  don't reserve** an IP: the apps pool's `serviceSelector` keeps Strimzi off `.240` (Kafka is
  internal-only — no LB VIP).
- **kubelet node name comes from the hostname**, not the cert — use `--hostname-override`.
- **containerd does NOT honor the default registry `config_path`** for the CRI image
  plugin — set it explicitly in `config.toml` + restart.
- **A self-signed CA needs `basicConstraints: CA:TRUE`** — Go/containerd enforce it.
- **bhyve:** node IPs are **MAC-reserved** on the gateway — machine-id/DUID does *not*
  affect them (don't chase IP issues there); and disks precede NICs in PCI slots, so
  *grow* `disk0`, don't add one. **aws:** `/dev/sdf` shows as `nvme1n1` on Nitro/arm64.
- **LVM `scan_lvs=0`** refuses an LV as a PV — set `scan_lvs=1` for nested VGs (bhyve).
- **`kubernetes_manifest` reads CRD schemas at plan time** — the operator must exist before
  planning its CRs (hence CRs are `kubectl`-applied, not tofu).

---

## 9. Repository layout
```
ansible/        cluster layer — node prep + kubeadm + storage      -> ansible/README.md
  inventory/    per-provider inventory (emitted) + group_vars
  roles/        builtin-only node-prep roles
tf/             OpenTofu roots (one state file each)
  k8s/          parameterized in-cluster services layer             -> tf/k8s/README.md
  aws/          AWS infra: VPC, subnets, instances, border          -> tf/aws/README.md
    vm/         reusable EC2 instance module
    border/     border-side agents (LB failover)
  bhyve/        stub infra — emits hard-coded inventory + tfvars
registry/       private registry PKI (per-provider cert dirs)       -> registry/README.md
apps/           applications                                        -> apps/helloworld/README.md
docs/           change timeline (CHANGELOG)
```
Top-level: `Makefile` (provider-aware entrypoint), `platform.mk` (selects the
provider), `README.md`.

## 10. Runbook index
- **[docs/CHANGELOG.md](CHANGELOG.md)** — full ordered timeline (added/changed/removed).
- **[tf/k8s/README.md](../tf/k8s/README.md)** — services layer: run, verify (drift = `tofu plan`), state per provider.
- **[tf/aws/README.md](../tf/aws/README.md)** — AWS infra: VPC/border/instances, tunnel + ProxyJump access.
- **[ansible/README.md](../ansible/README.md)** — node prep + storage playbooks (builtin-only, venv).
- **[registry/README.md](../registry/README.md)** — private registry stand-up + maintenance.
- **[apps/helloworld/README.md](../apps/helloworld/README.md)** — helloworld build/deploy onto the Cilium BGP LoadBalancer.
