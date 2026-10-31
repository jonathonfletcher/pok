# Change timeline

Ordered record of what was **added / changed / removed**, with the *why* and the
gotchas. Grouped into work phases in the order they happened. Exact per-step dates
aren't all reliable, so anchors are given where known (helm release timestamps put
the initial build ~2026-07-23; the rebuild/registry/storage work is 2026-07-30…31).

Legend: **[+]** added · **[~]** changed · **[−]** removed · **[!]** gotcha/fix

---

## A. Initial cluster (~2026-07-23)
- **[+]** `kubeadm init` — single control-plane `k8sm`, `--pod-network-cidr=10.244.0.0/16`,
  `--control-plane-endpoint=k8sm.homelab.lan`. containerd 2.2.1, Ubuntu 24.04, k8s 1.36.3.
- **[+]** Cilium 1.19.6 installed via `cilium install`.
- **[!]** Cilium defaulted to `ipam=cluster-pool` (10.0.0.0/8), conflicting with kubeadm's
  `10.244.0.0/16`. **[~]** Switched Cilium to `ipam=kubernetes` (consumes per-node podCIDRs).

## B. Observability
- **[+]** Two Honeycomb OpenTelemetry collectors via helm (agent DaemonSet + cluster Deployment).
- **[!]** Agent DaemonSet crash-looped: pipelines referenced `otlp/*` exporters but only
  `otlp_http/*` were defined. **[~]** Overlay fixed the pipeline exporter names + added
  resources (+ control-plane toleration). Cluster collector: resources added.

## C. hello-world app + trace delivery
- **[+]** Go stdlib HTTP app on :8080 (`/`, `/healthz`), OTel-instrumented (autoexport +
  otelhttp + manual `say-hello` span). Built with **ko**, distributed by importing the
  tarball into every node's containerd (`ctr -n k8s.io images import`) — no registry yet.
- **[!]** hostPort to the agent (`HOST_IP:4317`) was dead pre-KPR. **[~]** Added `otel-agent`
  ClusterIP Service with `internalTrafficPolicy: Local`; app targets its DNS name.

## D. External access (Cilium L2 LoadBalancer)
- **[!]** Cilium L2 announcements **require** kube-proxy-replacement (verified from Cilium docs).
- **[~]** Enabled kube-proxy-replacement (helm) → **[−]** removed the `kube-proxy` DaemonSet
  and flushed its iptables on every node.
- **[+]** `CiliumLoadBalancerIPPool` `lan-pool` (172.23.10.240–.249) + `CiliumL2AnnouncementPolicy`
  `lan-l2` on `enp0s5`. hello-world Service → `LoadBalancer` (172.23.10.240).
- Path proven: `dev.somegroup.net` → reverse proxy → LB .240 → app.

## E. Build workflow
- **[~]** Image tag derived from git (`git describe --tags --always --dirty`); `make deploy`
  rolls via `kubectl set image`. deploy.yaml pinned to a concrete tag.

## G. Rebuild to 3 control-plane + 7 workers (2026-07-30)
- **[~]** Renamed control-plane `k8sm` → **`k8sm1`** (Node objects are immutable → re-register:
  new kubelet cred via `kubeadm init phase kubeconfig kubelet`, delete old Node).
- **[!]** kubelet derives its node name from the **hostname**; after FQDN reboots it no longer
  matched the `system:node:<short>` cert → registration failed. **[~]** `--hostname-override=<short>`
  in `/var/lib/kubelet/kubeadm-flags.env`.
- **[!]** VM **clones shared `/etc/machine-id`** → DHCP client-id collisions → IP flapping.
  **[~]** Regenerated machine-id per node.
- **[!]** Clones also shared a baked `--hostname-override=k8sw3` + `system:node:k8sw3` cred, so
  k8sw3–k8sw7 all registered as one node. **[~]** `kubeadm reset` + rejoin each with the
  correct `--node-name`.
- **[+]** Joined k8sm2/k8sm3 (control-plane) and k8sw1–k8sw7 (workers). etcd → 3-member quorum.
- **[~]** `rollout restart ds/cilium` to resync the dataplane after the churn (ipcache/tunnels).

## H. API endpoint → HA name
- **[~]** Migrated the control-plane endpoint `k8sm.homelab.lan` → **`k8s.homelab.lan`**:
  regenerated the apiserver cert with the new SAN, updated `controlPlaneEndpoint` in
  kubeadm-config and `cluster-info`. Later the admin kubeconfig was repointed to it, so
  **OpenTofu is HA** (survives losing any one control plane).

## I. etcd member rename
- **[~]** etcd member `k8sm` → **`k8sm1`** (etcd can't rename in place): snapshot first,
  `member remove` → wipe data dir → `member add` → rejoin. Done against a surviving quorum.

## J. OpenTofu adoption of the software layer (import-first)
- **[+]** `tf/k8s/` now manages, adopted with **zero disruption** via `tofu import`:
  Cilium, both OTel collectors, the LB CRDs (`lan-pool`/`lan-l2`), and the Honeycomb Secret.
- **[+]** `honeycomb_api_key` variable (value in gitignored `terraform.tfvars`).
- **[!]** `.gitignore` had an **inline comment** on `*.tfvars` (gitignore has no inline
  comments) → tfvars weren't actually ignored. **[~]** Fixed; verified with `git check-ignore`.

## K. Private image registry
- **[+]** CNCF Distribution `registry:2` on k8sm1 via OpenTofu (`tf/k8s/registry.tf`): hostNetwork
  (binds 172.23.10.150:5000), hostPath `/var/lib/registry`, TLS from a self-signed CA.
- **[+]** Per-node containerd config (`registry/registry-node-setup.sh`): `certs.d/<host>/hosts.toml`
  + CA, **and an explicit `config_path` in `config.toml`**.
- **[!]** containerd **2.2.1 does not honor the *default* registry `config_path`** for the CRI
  image plugin (even though `containerd config dump` shows it) → kubelet pulls failed
  `x509: unknown authority` while `ctr --hosts-dir` worked. Fixed by the explicit config + restart.
- **[!]** Self-signed CA must have `CA:TRUE` (Go/containerd enforce it; curl accepted it either way).
- **[~]** `apps/helloworld/Makefile` + `deploy.yaml` switched from tarball/`ctr import` to `ko build --push`;
  app rolled onto `registry.homelab.lan:5000/hw:v1.0.3`. **[−]** Retired per-node tarball distribution.

## L. Ansible node-prep layer (builtin-only)
- **[+]** `ansible/` with a Python venv (`ansible-core` + `ansible-lint`, pinned) and roles:
  `os_update`, `prereqs` (packages, kernel modules, sysctl, swap-off), `apt_repos`, `cilium_cli`.
- **[~]** `apt_repos` migrated off the **deprecated** `apt_repository` to `deb822_repository`
  (added `python3-debian` prereq). Helm repo corrected from **baltocdn (defunct — returns bare
  `OK`)** to **Buildkite Packages**.
- **[!]/[~]** Fixes surfaced only by real runs (syntax-check/lint don't render templates):
  removed the `yaml` stdout callback (needs `community.general`; kept **builtin-only**),
  role-prefixed register vars, `no-handler` → real handler, and `ansible_swaptotal_mb` →
  `ansible_facts.swaptotal_mb` (fact-injection deprecation).
- **[−]** Removed the **`download.docker.com`** apt repo (containerd already present) — from
  group_vars, the legacy-removal regex, and all 10 nodes' `sources.list.d`.

## M. Storage — per-node data LV
- Considered adding a second disk; **[!]** rejected because **bhyve assigns PCI slots
  disks-before-NICs**, so a new `disk1` shifts the NIC `enp0s5`→`enp0s6` and breaks netplan
  (proven from the bhyve `-s` device lines). Also rejected a systemd `.link` MAC-pin workaround.
- **[~]** Chosen approach: **grow the existing `disk0` zvol** (no new PCI device) → Ansible
  `data_lv` role: `growpart /dev/vda 3` → `pvresize /dev/vda3` → `lvcreate ubuntu-data-lv` (16 GB).
- **[!]** Guard added after a bug: an ungrown node's GPT slack could make a useless sliver LV —
  role now requires ≥15 GiB free or fails cleanly.
- **[+]** `ubuntu-data-lv` (16 GB) created on all 10 nodes.

## N. Storage — TopoLVM (CSI)
- **[!]** TopoLVM provisions from a **VG's free space**, but `ubuntu-data-lv` consumed it →
  chose a **dedicated nested VG** (`topolvm-vg`) backed by that LV.
- **[!]** LVM `scan_lvs=0` refuses an LV as a PV. **[~]** `topolvm_vg` role sets `scan_lvs=1`
  via `lvmlocal.conf`, then `pvcreate`/`vgcreate topolvm-vg` on all 10 nodes.
- **[+]/[−]** **cert-manager v1.21.1** (`tf/k8s/cert-manager.tf`) was added for TopoLVM's
  mutating webhook, then **removed**: TopoLVM chart 17.0.0 is installed **without** the
  webhook, so cert-manager is not a dependency. `tf/k8s/cert-manager.tf` no longer exists.
- **[+]** **TopoLVM** chart 17.0.0 via OpenTofu (`tf/k8s/topolvm.tf`): lvmd DaemonSet, device-class
  `ssd`→`topolvm-vg` (`spare-gb: 1`), default StorageClass **`topolvm-provisioner`** (ext4,
  WaitForFirstConsumer, expandable). Verified end-to-end (PVC bound, LV carved, reclaimed).

## O. Kafka (KRaft, Strimzi) (~2026-08-01)
- **[+]** **Strimzi Kafka operator 1.1.0** via OpenTofu (`tf/k8s/helm_kafka.tf`, ns `kafka`,
  `watchNamespaces: [kafka]`). Strimzi 1.x is **KRaft-only** — no ZooKeeper.
- **[+]** KRaft cluster CRs in `tf/k8s/kafka-cluster.yaml` (kubectl-applied by `make services`
  after the operator registers its CRDs — same single-pass pattern as `lb-pool.yaml`,
  since a plan-time CRD can't exist for `kubernetes_manifest`): **KafkaNodePool
  `controller` ×3** (2Gi) + **`broker` ×3** (10Gi, pod anti-affinity across workers),
  **Kafka `main`** (v4.3.0, metadataVersion 4.3-IV0, replication factor 3 / min ISR 2).
- **[+]** Storage: per-node LVM via the existing **`topolvm-provisioner`** StorageClass
  (`jbod` volumes, `kraftMetadata: shared`, `deleteClaim: false`) — no Ansible changes.
- **[+]** Listeners: internal plain `9094` for in-cluster clients; **two external
  LoadBalancer listeners** — plain `9092` + tls `9093`. Their two bootstrap Services
  **share `172.23.10.241`** (distinct ports) via Cilium LB-IPAM
  (`lbipam.cilium.io/sharing-key: kafka-ext-bootstrap` + `lbipam.cilium.io/ips`);
  per-broker Services auto-assign from the pool (`.242+`).
- **[!]** Early design error (corrected): assumed plain+tls external access "needs ~10
  LB IPs" — wrong. One IP serves many ports and Cilium lets Services share an IP; the
  whole external surface fits inside `.241–.249`.

## P. LoadBalancer: L2 announcement → L3 BGP (~2026-08-01)
- **[~]** Switched Cilium LB from **L2/ARP announcement** to an **L3 LoadBalancer via BGP**,
  so the *same* model works on AWS (an AWS VPC is L3 — no ARP for unowned IPs). Cilium
  `bgpControlPlane.enabled: true`; the `CiliumL2AnnouncementPolicy` is gone.
- **[+]** `tf/k8s/bgp.yaml` (Cilium BGPv2, `apiVersion cilium.io/v2` as of 1.19):
  `CiliumBGPClusterConfig` (all nodes, AS **65010**, peer `172.23.10.100` AS **65000**) +
  `CiliumBGPPeerConfig` + `CiliumBGPAdvertisement` (all LoadBalancer VIPs as /32).
- **[~]** LB pool **moved off the LAN** to the routed **`172.23.20.0/24`** (an in-subnet VIP
  can't be L3-routed — directly-connected wins). Gateway routes it to the nodes via BGP.
- **[+]** OpenBSD gateway (`homelab`, 7.9): appended a `k8s-cilium` bgpd group (AS 65010,
  all 10 nodes) with `allow from … prefix 172.23.20.0/24 or-longer` / `deny to`; existing
  `aws` VPN group left intact. pf: `<ip4_k8s_cilium>` table widened the `internal` in/out
  catchalls so the routed VIPs forward. `bgpd` enabled + started.
- **[!]** **Cilium pins don't reserve** — an unpinned Service grabs the lowest free pool IP
  first, so Kafka's broker Services stole hello-world's pinned `.240`. **[~]** Split into two
  pools by `serviceSelector`: `apps-pool` `.240` (non-Strimzi) + `kafka-pool` `.241–.249`
  (`strimzi.io/cluster: main`). hello-world → `172.23.20.240`; Kafka bootstrap pin dropped
  (auto-shares a kafka-pool IP via the sharing-key).
- **[!]** Fallout from the `k8s-tf → tf/k8s` move: repo-root-relative paths gained a level —
  `registry_certs_dir` and `apps/helloworld`'s `trust` CA copy needed `../` → `../../`.

## Q. AWS infra + L3 BGP via FRR on the border (~2026-08-01)
- **[+]** `tf/aws` (hashicorp/aws only, arm64/Graviton): VPC `10.0.0.0/16`, border (t4g.micro,
  bastion + Caddy, EIP `prevent_destroy`), 3 control-plane + 3 worker t4g.small nodes
  (workers +20 GiB data disk), reserved `10.0.20.0/24` Cilium-LB subnet. Structure/`vm`
  module adapted from codeberg jonathonfletcher/simple-aws-awscc-tf (aws variant).
- **[+]** Border runs **FRR** (AS 65001) as the AWS analogue of the OpenBSD gateway's bgpd:
  `bgp listen range 10.0.10.0/24 peer-group CILIUM` (remote-as 65020) — dynamic peers, no
  node IPs enumerated; zebra installs the learned LB VIPs so Caddy can reach them.
- **[+]** Cilium BGP manifests for AWS staged in `tf/aws/manifests/` (AS 65020 → border
  65001, pool `10.0.20.0/24`) — applied when the AWS cluster exists.
- **[+]** Nodes `source_dest_check = false` so ENIs accept VIP-dst packets; SG opens tcp/179.
- **[!]** **Verified AWS does NOT decrement TTL between VPC subnets** (SYN-ACK arrived TTL 64) →
  single-hop eBGP works cross-subnet; **no `ebgpMultihop`**, no network redesign.
- **[!]** Same dpkg-conffile trap as Caddy for the `frr` package (pre-written `/etc/frr/frr.conf`)
  → install with `--force-confold`. And FRR needs the peer-group defined **before**
  `bgp listen range` references it (ordering) — both fixed in the cloud-init template.
- **[~]** `environment` renamed `simple` → `demo` (matches demo.somegroup.net); a full infra
  rebuild (key-pair/SG names are ForceNew) — harmless pre-cluster, EIP + IPs preserved.

## R. Multi-provider layout — INFRA_PROVIDER + tofu-emitted inventories (~2026-08-01)
- **[~]** The Ansible inventory is now **tofu-generated per provider** (both gitignored):
  `tf/aws` renders `inventory/aws/hosts.ini` from the real instance IPs + border EIP
  (ProxyJump via the bastion); `tf/bhyve` is a STUB (hashicorp/local only, no vm-bhyve
  provider) that renders `inventory/bhyve/hosts.ini` from hardcoded node IPs. Playbooks are
  unchanged (same group names); `ansible.cfg` defaults to `inventory/bhyve/hosts.ini`.
- **[+]** Top `Makefile` is **provider-aware** via `INFRA_PROVIDER` (`platform.mk`, gitignored;
  `platform.mk.example` committed): `make platform`, `use-bhyve`/`use-aws`, `infra`
  (`tofu -C tf/$(INFRA_PROVIDER)`), and a branched `teardown` (aws: `tofu destroy` keep-EIP;
  bhyve: Ansible reset + no-op `tf/bhyve teardown`). (`cluster`/`services`/`verify`/`app`
  were later made provider-aware — see §U.)

---

## S. AWS cluster + services deployed; Cilium-LB data-path decision (~2026-08-01)
- **[+]** The **AWS cluster is up**: 3 control-plane (`cp1–3` `10.0.10.101–.103`) + 3 workers
  (`w1–3` `.111–.113`), arm64, k8s **v1.36.3**. Built by running the layers against the
  private API over an **SSH tunnel** (`server: https://127.0.0.1:16443`, `tls-server-name:
  10.0.10.101`, ProxyJump via the border EIP). 51 pods Running once Cilium installed.
- **[+]** **AWS services deployed** from `tf/k8s` against `aws.tfstate`
  (`-var-file=vars/aws.tfvars.json`): Cilium (+BGP), TopoLVM (workers' `nvme1n1`),
  OTel→Honeycomb, Strimzi + KRaft Kafka (broker×3/controller×3), self-hosted registry on
  `cp1`. LB/BGP/Kafka CRs `kubectl`-applied. **No new AWS resources** for the services layer.
- **[x]** **registry_node fix** — was `ip-10-0-10-101` (the AWS *default* hostname) but the
  nodes are named `cp1..w3`, so the registry pod stayed `Pending` (no matching hostname
  label). `tf/aws/k8s-vars.tf` → `registry_node = "cp1"`; pod rescheduled onto cp1, Ready.
- **[~]** **State caveat:** `aws.tfstate` currently tracks only the `local_file`s (+ the new
  route). The helm/namespace/registry objects exist on the cluster but were created
  out-of-band (an earlier apply created them; a later apply hit "already exists"). The
  services **run** but are **not yet fully tracked** in `aws.tfstate` — reconcile via
  `tofu import` (or teardown + clean re-apply) before trusting drift detection there.
- **[+]** **Cilium BGP established** on all 3 CPs with the border FRR (AS65020 ⇄ AS65001),
  advertising the 7 LB `/32`s; Kafka LB VIPs assigned from `10.0.20.241–.247`.
- **[!]** **AWS LB data-path finding** — the border (subnet `10.0.100.0/24`) can reach real
  node ENIs but **not** the Cilium LB pool `10.0.20.0/24`: that subnet owns **no ENIs**, so
  the VPC fabric blackholes it, and a BGP nexthop in the node subnet isn't on-link for the
  border (verified: `ip route` → "invalid gateway"; `ip nht resolve-via-default` didn't
  help). VPC route tables also **cannot ECMP**, so bhyve's active/active has no native AWS
  equivalent.
- **[+]** **Interim route** `aws_route.cilium_lb` — `10.0.20.0/24 → cp1 ENI`
  (`tf/aws/cilium-lb.tf`). Makes the pool reachable (nodes already `source_dest_check=false`;
  the border→node SG already opens 80/443/8080). **This is a SPOF** (single ENI) — a standby
  target only, pending the failover agent.
- **[decision]** **LB = active/standby with automatic failover, NOT an NLB.** A BGP-aware
  agent on the border repoints the VPC route to a live node on BGP session loss. Chosen to
  avoid NLB cost/complexity and keep parity with bhyve (pure Cilium L3, no NodePorts) — the
  same tradeoff as self-hosting the registry instead of ECR. Documented in `docs/README.md` §5.2.
  (Agent built and verified in §U.)
- **[~]** **README** rewritten as a multi-provider document (what the repo is, the four
  layers, provider comparison table, provider selection, parameterization, §5 compromises);
  `Makefile` help-note corrected (AWS cluster/services ARE built; a few targets still
  bhyve-shaped when run end-to-end).

---

## T. HA API endpoint (Route 53) + control-plane resize + AWS rebuild (~2026-08-01)
- **[+]** **Route 53 private hosted zone `demo.internal`** (`tf/aws/dns.tf`, VPC-associated):
  A records for every node (`cp1–3`, `w1–3`, `border`) + **`k8s.demo.internal`** = a
  round-robin A over all 3 control-plane IPs. Resolves via the VPC resolver on every node —
  the AWS analog of bhyve's `homelab.lan` zone / `k8s.homelab.lan`.
- **[~]** **`control_plane_endpoint` → `k8s.demo.internal:6443`** (HA) in aws group_vars;
  added `apiserver_cert_extra_sans` (the name + per-CP names + the 3 CP IPs), wired into
  `kubeadm init --apiserver-cert-extra-sans` (conditional, so bhyve is unchanged). Cilium's
  `k8s_service_host` points at the same name. `internal_domain` is a `tf/aws` variable.
- **[~]** **CP + worker instances → `t4g.medium`** (4GB) — `t4g.small` (2GB) was starving
  the control plane. Applied as an in-place resize (stop/start).

---

## U. BGP-driven LB failover agent + AWS rebuild outcome + hello-world (~2026-08-01)
- **[+]** **LB failover agent on the border** (`tf/aws/border/lb-failover.sh` + `.service`,
  `iam-border.tf`): watches FRR's BGP sessions and `ec2:ReplaceRoute`s `10.0.20.0/24` onto
  a live node's ENI when the current target's session drops — active/standby (AWS can't
  ECMP; docs/README.md §5.2). One new **free** IAM role (scoped: ReplaceRoute on this RT +
  read-only Describe). Verified live: cp1's session was down, the agent moved the route
  cp1→cp3 automatically, then stayed sticky.
- **[!]** Agent bugs found + fixed while bringing it up: (1) wrong `jq` path — use
  `show ip bgp summary json` (nests peers under `.ipv4Unicast.peers`), not the flat
  `show bgp ipv4 unicast summary`; (2) Ubuntu 24.04 has **no apt `awscli`** → install
  awscli **v2** (arm64) from the zip; (3) set the unit's `PATH` (awscli v2 lands in
  `/usr/local/bin`); (4) empty-peer list produced a phantom node — emit nothing, not a
  blank line; (5) `aws_route.cilium_lb` needs `lifecycle.ignore_changes=[network_interface_id]`
  so tofu doesn't revert the agent's failover on the next apply.
- **[~]** **AWS rebuilt** to fix the SPOF endpoint + undersize: all instances → `t4g.medium`
  (in-place resize = stop/start), `kubeadm reset` → re-`init` with `k8s.demo.internal`
  + cert SANs → fresh `tf/k8s` services (clean `aws.tfstate`). Result: 6 nodes Ready,
  BGP from all 6 to the border, registry on cp1, LB VIPs assigned.
- **[!]** **`enable_dns_hostnames` gotcha:** a Route 53 **private** zone needs BOTH
  `enable_dns_support` AND `enable_dns_hostnames` = true on the VPC; the VPC had hostnames
  **off** (aws_vpc default), so `k8s.demo.internal` NXDOMAIN'd and the freshly-built cluster
  couldn't reach its own endpoint. Fixed in `network.tf`; ~45s to propagate. (Also: the
  joins during the first bootstrap silently didn't register because the endpoint didn't
  resolve then — re-ran `bootstrap.yml` after the DNS fix and all 6 joined.)
- **[+]** **hello-world on AWS (arm64):** built with `ko --platform=linux/arm64` on the
  build host and pushed to the cp1 registry **through an SSH tunnel** (`10.0.10.101:15000`
  → cp1 `:5000`, via a `10.0.10.101` loopback alias so the IP-SAN cert matches — the local
  `:5000` is taken by this host's own bhyve registry). Deployed referencing `:5000` (nodes
  pull in-VPC). `type: LoadBalancer` → apps-pool `10.0.20.240`. Caddy on the border set to
  `reverse_proxy 10.0.20.240:80` (`border_proxy_upstream` in tfvars). **Verified:**
  `https://demo.somegroup.net/` returns `hello world via hello-world-…`.
- **[~]** SSH tunnels documented (tf/aws README §Access): local port = service port +
  10000 — kube API `16443`→`6443`, registry `15000`→`5000`.
- **[!]/[~]** **Kafka broker storage — fixed.** Brokers were `Pending` ("not enough free
  storage") because **`kubeadm reset` doesn't touch LVM**, so TopoLVM LVs from the
  pre-rebuild cluster survived and filled the workers' 20 GB `topolvm-vg` (a stale 10 GB
  broker LV + orphaned controller LVs left only ~6 GB free). Fix: (1) removed the orphaned
  LVs on each worker (keeping only in-use PV handles) + restarted `topolvm-node` to
  re-advertise capacity → 10Gi broker PVCs provisioned, all 6 Kafka pods Ready, `Kafka`
  CR `Ready=True`; (2) **`teardown.yml` now `lvremove`s all `topolvm-vg` LVs** so a rebuild
  starts with an empty VG (the systemic fix).

---

## V. Application layer — Kafka-backed live cluster feed (~2026-08-01, bhyve)
- **[+]** A web app modelled on emf.somegroup.net (styles/layout/JS reused): a latest-value-
  per-subject table over a WebSocket, with theme/timezone toggles, sort, and filter. Five
  Go apps under `apps/`, all ko-built to the bhyve registry:
  - **`static`** — pure Go file server for the emf-styled frontend (`web/` embedded;
    `style.css` + `app.js` + toggles reused verbatim; `index.html` adapted).
  - **`clusterinfo`** — every 5s, in-cluster client-go polls nodes/pods/namespaces and
    publishes to Kafka `cluster-info` (keyed by aspect); read-only SA/RBAC.
  - **`hw`** — hello-world now publishes one `url-visits` event per request (keyed by path).
  - **`wsfeed`** — consumes both topics (single-partition, no consumer group so every
    replica holds full state), aggregates a per-URL count, serves `/ws` in the emf envelope
    `{subject,topic,ts,data}` (latest-per-subject seed + live).
  - **`edge`** — stdlib reverse proxy (app-layer ingress; none installed): one front-door
    LoadBalancer VIP (`172.23.20.240`) routing `/ws`→wsfeed, `/hw`→hello-world, `/*`→static,
    so the browser gets a same-origin `/ws`.
- **[+]** Kafka: `apps/kafka-topics.yaml` — `cluster-info` (compacted, latest-per-aspect) and
  `url-visits` (24h event log), single-partition ×3 replicas. In-cluster bootstrap
  `main-kafka-bootstrap.kafka.svc:9094` (plain); client `segmentio/kafka-go` (pure Go).
- **[!]** Strimzi here serves `KafkaTopic` as `kafka.strimzi.io/v1` (not `v1beta2`).
- **[~]** For the bhyve demo, helloworld's Service was patched to ClusterIP so `edge` takes the
  apps-pool VIP; the shared `apps/helloworld/deploy.yaml` is unchanged (LoadBalancer for aws).
- **Verified:** WS seed + 5s live updates (`cluster/nodes|pods|namespaces`) and per-URL
  counts (`visit/hw/…`) at `http://172.23.20.240/`. External `dev.somegroup.net` serves the
  page but its reverse proxy doesn't pass the WebSocket upgrade (HTTP/2 `426`) — an RP-config
  item, not an app defect.

## W. Live-feed on AWS + app hardening (~2026-08-02)
Supersedes the §V current-state where they differ.
- **[+]** Live-feed stack deployed to **aws** (arm64): `pok.somegroup.net` → border Caddy →
  `edge` VIP `10.0.20.240`, AWS-native (no cross-provider proxying). `dev.somegroup.net` (bhyve)
  WebSocket upgrade fixed in the external nginx.
- **[~]** AWS public website renamed `demo.somegroup.net` → `pok.somegroup.net` (both resolve to
  the border EIP; border Caddy serves both during transition). `index.html` links to
  `github.com/jonathonfletcher/pok` (`.site-link`, ↗ glyph). Border Caddy now `encode zstd gzip`
  (compresses outbound where the client accepts it; `Vary: Accept-Encoding`).
- **[~]** App hardening: `imagePullPolicy: Always` (avoids stale-tag pulls); PDBs +
  topologySpread on `edge`/`wsfeed`; `edge` OTel-instrumented (request-path spans). New
  **`visitcounter`** app aggregates `url-visits` → compacted `visit-counts` (single authoritative
  counter); `wsfeed` now relays `visit-counts`, so per-URL counts are consistent across replicas.
- **[~]** `edge` owns the single apps-pool VIP (`.240`) on **both** providers; `helloworld`
  Service is now `type: ClusterIP` in the manifest (was LoadBalancer, which raced `edge` for
  `.240`) — reached via `edge` at `/hw`. `wsfeed` and `static` are ClusterIP behind `edge`.
- **[~]** `clusterinfo`: 500ms tick, ONE random aspect/tick of 14 (adds restarts, capacity,
  deployments, services, pvcs + 6 cilium views), `RequireAll` acks, `/healthz` liveness on :8080.
- **[~]** `wsfeed`: origin allow-list via `ALLOWED_ORIGINS` (default `demo,dev`; deploy adds
  `pok`) replacing allow-all; `c.CloseRead` disconnect handling; `maxVisitURLs=512` cap on the
  public `/hw/*` visit cardinality.
- **[!]** Image tags from `git describe --dirty` collide across uncommitted rebuilds; with
  `imagePullPolicy: IfNotPresent` nodes run stale images — deploy with a unique `TAG=`.
- **[~]** ansible `bootstrap.yml`: join token/certificate-key tasks `no_log: true`; Play 2 also
  tagged `kubeadm_join` so joins can run standalone. `Makefile` teardown wipes
  `registry/certs/bhyve`. `registry_certs_dir`/`registry_node` no longer carry tf defaults.
- **Docs:** READMEs + comments fact-checked, de-opinioned, trimmed.

---

## Removed / retired (summary)
- kube-proxy DaemonSet (replaced by Cilium KPR) — §D
- **Cilium L2 announcement** (`CiliumL2AnnouncementPolicy`), replaced by L3 BGP — §P
- **External Kafka LB listeners + `kafka-pool` (`.241–.249`)** — Kafka is now internal-only (single apps VIP `.240`) — §O, §P
- Per-node image tarball distribution (replaced by the registry) — §K
- `download.docker.com` apt repo — §L
- Old node names/identities (`k8sm`, cloned `k8sw3` identities) — §G, §I
- Endpoint `k8sm.homelab.lan` (superseded by `k8s.homelab.lan`) — §H
- Rejected-and-never-applied: adding a second bhyve disk; systemd `.link` NIC-name pin — §M

---

## De-provider-ization — one parameterized `tf/k8s` + `ansible` per `INFRA_PROVIDER` (DONE)

**Goal:** no infra-provider assumption anywhere *except* `tf/<provider>` and
`inventory/<provider>`. Keep a SINGLE `tf/k8s` (and single set of ansible
roles/playbooks); feed all provider-specific values in as variables. Today `tf/k8s`
and parts of `ansible` are bhyve-hardcoded (enp0s5, homelab, 172.23.x, registry on
k8sm1) — see the review that produced this plan.

**How provider values reach the shared layers (decision):** each `tf/<provider>`
emits a per-provider tfvars for `tf/k8s` (symmetric with how it already emits the
Ansible inventory); the Makefile selects it by `INFRA_PROVIDER`
(`tofu -C tf/k8s apply -var-file=<provider>.tfvars`). Ansible provider values live in
`inventory/<provider>/group_vars/`.

**Mechanism (built):** each `tf/<provider>` emits `tf/k8s/vars/<provider>.tfvars.json`
(gitignored) via `local_file`; `tf/k8s` has provider vars with NO defaults; the top
Makefile passes `INFRA_PROVIDER` → `tofu -C tf/k8s … -var-file=vars/$(INFRA_PROVIDER).tfvars.json`.

**Tasks (piecewise):**
- [x] **Cilium values** — `devices` + `k8sServiceHost` are now `${...}` in
      `values/cilium.values.yaml.tftpl`, fed by `var.cilium_devices`/`var.k8s_service_host`.
      Verified zero drift on the live bhyve CNI (`tofu plan` = No changes). NIT: the
      values file still has one bhyve-specific *comment* (kept byte-identical to avoid a
      helm re-render of the load-bearing CNI); clean it on the next deliberate Cilium change.
- [x] **kubeadm (ansible/bootstrap.yml)** — `control_plane_endpoint`, `pod_cidr`,
      `service_cidr` moved to `inventory/<provider>/group_vars/all.yml` (bhyve + aws).
- [x] **LB pool + BGP** — pool CIDR + BGP peer/ASN/names are `tf/k8s` variables;
      `lb-pool.yaml`/`bgp.yaml` are now rendered per-provider from `templates/*.tftpl`
      (`rendered.tf` `local_file`, gitignored). Verified: bhyve render is byte-identical
      (same CR names → zero churn); aws render (name `aws`, AS 65020, peer `border`
      `10.0.100.101`) confirmed via `tofu console`.
- [x] Removed `tf/aws/manifests/*` — folded into the one parameterized `tf/k8s`.
- [x] **apps/helloworld** — `REGISTRY` is overridable (`?=`); dropped the `io.cilium/lb-ipam-ips`
      pin (single-IP apps-pool auto-assigns `<prefix>.240` on both providers).
- [x] **Registry** (Category B) — **parameterized** (both providers self-host on the
      primary CP; no ECR/IAM, so no new AWS resource). `registry.tf` is count-guarded on
      `manage_registry` (still a flag) and node-pinned via `var.registry_node` (bhyve
      `k8sm1`, aws `cp1`); `registry_certs_dir` is **per-provider**
      (`registry/certs/<provider>/`, avoiding CN/SAN collision). Ansible: `kube_registry_pki`
      CN/SAN/dir + `kube_registry_trust` host/CA in `inventory/<provider>/group_vars`
      (aws addresses cp1 by IP `10.0.10.101:5000`, IP-SAN cert). Live bhyve migrated via
      `state mv` to `[0]` + certs moved to `certs/bhyve/` → No changes.
- [x] **DNS** (Category B) — `infra_dns_zone` (prepare.yml) is `when: manage_dns_zone`;
      bhyve=true, aws=false. AWS uses Route 53 / VPC DNS, no zone generation.

**Category A + B complete.** `tf/k8s` + the ansible playbooks now assume no infra
provider — everything provider-specific comes from `tf/<provider>` (emitted tfvars) or
`inventory/<provider>/group_vars`. Residual, app-layer only (pending the AWS app flow):
the `trust` make target + `apps/helloworld` REGISTRY default are still bhyve-shaped, and the
one bhyve-specific *comment* in `cilium.values.yaml.tftpl` (kept for CNI byte-identity).

**Not in scope / already correct:** the `INFRA_PROVIDER` selector, `tf/<provider>`
inventory + tfvars emitters, and the FRR/bgpd peering configs (those legitimately live per
provider). `kafka-cluster.yaml` is already provider-agnostic (VIPs come from the pool).

## X. Dependency refresh, step 1+2 of the upgrade path (2026-10-17)
Repo-side bumps only; nothing applied to a live cluster yet. Cilium and the Kubernetes node
packages are deliberately untouched (consecutive-minor rules; see the upgrade path notes).
- **[~]** Go toolchain `1.26` → `1.27` in all seven `apps/*/go.mod` + CI `setup-go`. OTel SDK
  1.44 → 1.47, contrib 0.69 → 0.72, `k8s.io/*` 0.36.3 → 0.37.1 (clusterinfo), aws-sdk-go-v2
  refreshed (awscost). kafka-go and coder/websocket were already current.
- **[!]** `go get -u ./...` in clusterinfo over-upgraded `kube-openapi` to a
  `structured-merge-diff/v7` build that `apimachinery` 0.37.1 (still on v6) cannot compile
  against. **[~]** clusterinfo is upgraded with targeted `go get <direct dep>@<version>`
  instead, letting MVS pick the k8s-compatible transitive set.
- **[~]** Helm chart pins (`tf/k8s`): metrics-server 3.12.2 → 3.14.0, TopoLVM 17.0.0 → 17.3.0,
  opentelemetry-collector 0.165.0 → 0.176.0 (both releases), Strimzi 1.1.0 → 1.3.0 (still
  supports the Kafka 4.3.0 the CR declares, so no broker upgrade is forced). Chart value keys
  we override were diffed old vs new: no renames.
- **[~]** Provider lockfiles via `tofu init -upgrade`: aws 6.57.1 → 6.69.1, helm 3.2.0 → 3.3.0,
  local 2.9.0 → 2.9.1. kubernetes stays 2.38.0 (last 2.x; 3.x is a plugin-framework major).
- **[~]** ansible-core 2.21.2 → 2.21.5, ansible-lint 26.6.0 → 26.10.1.
- **[~]** GitHub actions: checkout v4 → v7, setup-go v5 → v7, setup-opentofu v1 → v2.
  **[!]** checkout ≥ v5 needs a Node 24-capable runner (≥ 2.327.1) — verify the self-hosted
  `k8s-build` runner before merging, or `deploy.yml` breaks on the next push to `main`.
- **[~]** "Proven on OpenTofu v1.12.5" notes → v1.13.1 (the host's actual version).
- **[~]** Cilium chart 1.19.6 → 1.19.9 (bugfix-only patch; the 1.19 → 1.20 minor is a separate,
  preflighted step). cilium-cli pin v0.19.7 → v0.20.1. `docs/README.md` row 57 still says
  1.19.6 on purpose: that table describes the *running* clusters, which haven't been rolled yet.
- **[!]** This refresh was prepared on a host with no tofu state, no kubeconfig and no AWS
  tunnel, so `make services` (the actual rollout) must run from the build host.
- **[+]** `ansible/upgrade.yml` + `make upgrade-k8s` — the Ansible layer only knew init/join;
  this is the missing in-place kubeadm upgrade: primary CP `kubeadm upgrade apply`, then
  secondary CPs and workers `kubeadm upgrade node`, each `serial: 1` with drain → kubelet/
  kubectl bump → restart → uncordon → wait Ready (shared steps in `ansible/tasks/upgrade_*.yml`).
  Pre-flight refuses to skip a minor. Same validation status as `bootstrap.yml`:
  `--syntax-check` + `ansible-lint` (production profile), not yet run against a live cluster.
- **[~]** `kube_packages_version` 1.36.3-1.1 → 1.36.5-1.1 (patch). Fresh builds get 1.36.5;
  live clusters get there via `make upgrade-k8s`. The 1.37 minor waits on Cilium 1.20.
- **[+]** **aws rebuilt from scratch on these pins (2026-10-17)**: `make infra` (59 added, EIP
  52.50.172.94 reused) → `make cluster` → `make services` → `make app` → `make verify` (nodes
  Ready, no drift, front door 200). Live: k8s v1.36.5, Cilium 1.19.9, metrics-server 3.14.0,
  TopoLVM 17.3.0, otel-collector 0.176.0, Strimzi 1.3.0 / Kafka 4.3.0. bhyve untouched.
- **[!]** The build host's `go env` file pins `GOTOOLCHAIN=go1.26.5`, which blocks the
  automatic 1.27 toolchain download `ko` needs now that go.mod says 1.27 — the first `make app`
  failed on it. Worked around with `GOTOOLCHAIN=go1.27.2` in the environment; the self-hosted
  deploy runner needs `go env -w GOTOOLCHAIN=auto` (or Go 1.27.2 installed) before CI deploys.
- **[!]** `make tunnel` uses `BORDER_GW=pok.somegroup.net`; the A record had been removed after
  the September teardown, so the tunnel was brought up with `BORDER_GW=52.50.172.94` until the
  record was restored.
- **[~]** **Cilium 1.19.9 → 1.20.3 on aws** (2026-10-17). 1.20 "Action Required" items checked:
  no proxylib L7 (`kafka`/`l7proto`) policy rules, no `CiliumNodeConfig` v2alpha1, no custom
  CNI config, no Gateway API, no mutual auth. Chart values we override only gained keys.
  Preflight (`helm install cilium-preflight … --set preflight.enabled=true`, 6/6 + 1/1, clean
  logs) → uninstall → `tofu apply` (plan guarded to exactly `helm_release.cilium` in-place).
  Post: `cilium status` OK 6/6, all 6 BGP sessions re-established, VIP + front door 200.
- **[~]** Kafka 4.3.0 → 4.3.1 on aws (`kafka-cluster.yaml` `spec.kafka.version`; `metadataVersion`
  stays `4.3-IV0`). Strimzi rolled the 6 pods one at a time in ~3 min, `Kafka` stayed Ready.
- **[!]** **Kubernetes 1.37 is ON HOLD**: Cilium 1.20's compatibility table lists k8s 1.33–1.36
  only. Revisit when a Cilium release lists 1.37; then bump `kube_apt_repos_k8s_minor` to
  `v1.37` + `kube_packages_version` to `1.37.1-1.1`, re-run `os_prep.yml --tags kube_apt_repos`,
  and `make upgrade-k8s`. Until then 1.36.x patches only.

## Y. aws torn down (2026-10-18)
- **[−]** `make teardown` (tofu destroy, EIP excluded): 59 resources destroyed, border EIP
  52.50.172.94 kept (unassociated). pok.somegroup.net is dark until the next `make infra`.
- **[~]** Cleaned the files that described the destroyed stack: `tf/k8s/aws.tfstate*`,
  `tf/k8s/vars/aws.tfvars.json`, `ansible/inventory/aws/hosts.ini`, `tf/aws/aws-kubeconfig`,
  and `tf/aws/border-ssh.auto.tfvars.json` (so the fresh-build default `border_allow_ssh=true`
  applies on the next `make infra`; last rebuild inherited a stale `true`).
- **[!]** `make teardown` typed from the Claude Code `!` prompt stops at tofu's "yes" prompt
  (no interactive stdin) and destroys nothing; run it from a terminal, or
  `TF_CLI_ARGS_destroy="-auto-approve -input=false" make teardown`.
- Rebuild path unchanged: `aws login` → `make infra` → `make cluster` → kubeconfig + tunnel →
  `make trust` → `make services` → `make app` → `make verify` → `make border-ssh-off`
  (docs/README.md §6; CHANGELOG §X has the 2026-10-17 run).


## Z. Adversarial review fix pass (2026-10-25)

Source: `docs/reviews/2026-10-25-adversarial-review.md` (four independent reviewers, every
HIGH re-verified by hand). Both clusters torn down throughout, so every change here is
validated by vet/test/lint/validate, not by a live run.

- **[!] Go:** GO-2026-6809 (crypto/x509) hit all 7 apps at `toolchain go1.27.2`; clusterinfo
  also carried `x/net v0.59.0` with five HTTP/2 CVEs. `make -C apps go-update DEPS=` moved
  every go.mod to `go 1.27.3` (the `toolchain` line is dropped when equal), and clusterinfo's
  lagging indirects (x/net 0.61, x/sys 0.49, compress 1.20.3, lz4 4.1.33, prom 1.25) were
  bumped to match the other apps. govulncheck clean afterwards.
- **[!] Go:** kafka-go `Writer` struct-literal default is `BatchTimeout: 1s`, so every
  synchronous single-message write blocked ~1s: clusterinfo's 500ms ticker dropped ticks,
  visitcounter capped at ~1 visit/s and replayed the 24h log at 1 event/s on every restart.
  Both writers now set `BatchTimeout: 10ms`; visitcounter accumulates the replay silently
  until `Reader.Lag()==0`, then publishes each URL once.
- **[~] Go:** wsfeed validates Kafka values with `json.Valid` before embedding them as
  `json.RawMessage` (one bad value used to fail `Marshal` for every client, forever).
- **[~] Go:** visit keys are bounded at 256 bytes end to end (helloworld refuses to publish
  longer paths; visitcounter and wsfeed drop them) — closes an unauthenticated
  memory-exhaustion path through `/hw/<long path>` into wsfeed's 64Mi limit.
- **[~] Go:** clusterinfo `rest.Config.Timeout = 5s` (Discovery `ServerVersion()` takes no
  ctx); helloworld's async visit writer uses `RequireOne` so `Completion` sees broker errors.
- **[~] aws:** border FRR now filters dynamic peers with a prefix-list/route-map pair
  (only `aws_subnet.cilium_lb` prefixes in, nothing out), matching the bhyve gateway.
  `no bgp ebgp-requires-policy` removed. The review's "ReplaceRoute scope is broad" claim
  was wrong: `iam-border.tf` already scopes it to the one route table.
- **[~] build:** `apps/go-toolchain.mk` is the single definition of "which Go builds the
  images", included by apps/Makefile and every app Makefile. Resolved lazily and once per
  make tree, exported only by Go-running targets, so `make -n`/`help`/`apply`/`deploy` are
  offline. `make -C apps awscost` works. `make help` prints the whole header. `make tunnel`
  fails loudly on a stale forward (`ExitOnForwardFailure=yes`).
- **[~] Makefile:** `make teardown` / `make recreate` need `CONFIRM=yes`; the playbook's
  `confirm_teardown` guard is passed through instead of hardcoded.
- **[~] ansible:** `kubeadm init` output is `no_log` (join token + certificate-key) with a
  redacted report; localhost pre-flight play is `become: false`; the kubelet upgrade's
  drain→apt→restart runs under a rescue that uncordons the node; containerd config sets
  runc `SystemdCgroup = true` (fresh-build change, untested on a live node); kube_apt_repos
  asserts the repo minor matches the kube_packages pin.
- **[!] host:** the build host's `go env` had `GOTOOLCHAIN=go1.27.2` pinned again; reset to
  `auto`. The Makefile export overrides it either way.
- Open from the review (not in this pass): wsfeed connection cap and drain-on-shutdown,
  Kafka internal listener TLS/auth, registry auth, tofu state encryption, cost-reader role
  scope, `automountServiceAccountToken: false`, Go helper dedup (`apps/internal`), CI gaps
  (ansible-lint, tofu plan, ko build, tests), remaining stale docs.

### Z.2 second pass (2026-10-25, later)

- **[~] wsfeed:** per-client outbound queue is now latest-value-per-subject in arrival order
  (was a 256-deep channel that silently dropped for slow clients and never resent): no update
  is ever lost, memory per client is bounded by the subject count. `/ws` admission cap
  (`WS_MAX_CLIENTS`, default 256) returns 503 above it. SIGTERM now really drains: serve in a
  goroutine, `Shutdown` on the main path, `GoingAway` close frame to every session, wait for
  them (5s), then the deferred reader close + tracer flush actually run. Unit tests for the
  hub (coalescing, cap, drain).
- **[~] apps:** `IdleTimeout: 60s` on every `http.Server`; `automountServiceAccountToken:
  false` on every pod except clusterinfo (the only one that talks to the API);
  `imagePullPolicy: IfNotPresent` on the immutable git-describe tags so a restart no longer
  depends on the in-cluster registry; awscost binds `127.0.0.1:9782` under hostNetwork with
  the probe's `host:` set to match.
- **[~] tf/k8s:** every other `helm_release` has `depends_on = [helm_release.cilium]`
  (fresh apply used to race the CNI).
- **[~] CI:** `timeout-minutes` on every job; deploy.yml pins `ko@v0.19.1`.
- **[~] docs:** tf/k8s/README no longer claims `apply` installs network policies (that is
  `make netpol`); helloworld README/Makefile no longer claim `apply` creates the Namespace;
  app.js comments stop describing JetStream/NATS/MQTT and "72h" (window is 7 days);
  ansible/README pins corrected to 2.21.5 / 26.10.1.

### Z.3 Cilium 1.20.4 (2026-10-26)

- **[~]** `tf/k8s/helm_cilium.tf` pin 1.20.3 → 1.20.4 (patch, GA 2026-10-23). Lands on the next
  rebuild; on a live cluster it is a `cilium` preflight + `make services`. k8s stays 1.36.5:
  Cilium 1.21 (needed for 1.37) is still `1.21.0-pre.3`, no GA.

### Z.4 Kubernetes 1.36.6 (2026-10-31)

- **[~]** `kube_packages_version` 1.36.5-1.1 → 1.36.6-1.1 (patch, GA 2026-10-28; deb confirmed on
  pkgs.k8s.io v1.36). Lands on the next rebuild; on a live cluster it is `make upgrade-k8s`.
  1.37 (now 1.37.2) still held: Cilium 1.21 remains `1.21.0-pre.3`.
