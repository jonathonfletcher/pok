# `tf/aws/` — AWS infrastructure (OpenTofu)

The **infra** layer for the `aws` provider (docs/README.md §1): a VPC, three subnets,
six arm64 EC2 nodes, and an internet-facing **border** VM (FRR + Caddy + an LB-failover
agent). **Emits** the Ansible inventory and the `tf/k8s` tfvars, so layers above never
learn provider details. Structure and the `vm/` module are adapted from
**[jonathonfletcher/simple-aws-awscc-tf](https://codeberg.org/jonathonfletcher/simple-aws-awscc-tf)**
(the `aws/` variant). A **separate root** from `tf/k8s/`, own state and provider; the two
never share a plan.

> Status: **applied and running.** A live cluster's infra — 7 `aws_instance`
> (3 control-plane + 3 workers + border), border EIP → **pok.somegroup.net** (`demo`,
> eu-west-1; raw IP via `tofu output border_public_ip_address`).

> Risk: **state is local and holds real IDs (and any secrets) in cleartext.** Move it to
> an encrypted S3 backend (`versions.tf` TODO) — outstanding, not a pre-apply step.

## What it builds
One **VPC** (`10.0.0.0/16`) + internet gateway + route table + default route. Three
subnets, all on that route table:

| Subnet | CIDR | ENIs | Purpose |
|---|---|---|---|
| `border` | `10.0.100.0/24` | border VM | public bastion / reverse proxy / BGP router |
| `internal` | `10.0.10.0/24` | 6 nodes | k8s control-plane + workers |
| `cilium_lb` | `10.0.20.0/24` | none | reserved for the Cilium LoadBalancer IP pool |

`cilium_lb` has no ENIs, so the VPC local route can't deliver to it —
`aws_route.cilium_lb` sends `10.0.20.0/24 → cp1's ENI`, where Cilium's eBPF LB DNATs to
the pod (the LB data-path target; see failover below).

Two node pools, each an instance of the local **`./vm/`** module (Ubuntu 24.04 arm64 AMI,
gp3 root, optional EBS data volume, deterministic private IP):
- `module.control_plane` — `var.control_plane_count` (3), private IPs `10.0.10.101+`.
- `module.worker` — `var.worker_count` (3), private IPs `10.0.10.111+`, `nvme1n1` data disk.

Both pools run **`source_dest_check = false`** so a node's ENI accepts packets whose dst
is a Cilium LB VIP (routed to it, not to the ENI's own IP).

The **border** (`module.border`, `vm/` with `public_ip_address=true`): FRR (AS 65001,
peers the nodes' Cilium at AS 65020), Caddy (reverse proxy to the LB VIP), and the
**lb-failover** systemd agent — all installed by cloud-init
(`templates/border-cloud-init.yaml.tftpl`). Carries an IAM instance role
(`iam-border.tf`, `ec2:ReplaceRoute` on this route table + read-only `ec2:Describe*`),
a **preserved EIP** (`eip_prevent_destroy=true`), and `user_data_replace_on_change=false`
so a template edit doesn't destroy the EIP-bearing instance.

**Security groups:**
- **border SG** — ingress `80/443` from `0.0.0.0/0` (always) + `22` **toggleable**
  (`make border-ssh-on|off` / `var.border_allow_ssh`; open by default for setup, close during
  normal operation), plus `179` (BGP) from the internal SG; egress all.
- **internal SG** — wide-open intra-subnet traffic (`ip_protocol -1` from the
  `10.0.10.0/24` CIDR, covering every inter-node k8s port), **plus** six ports —
  `22, 80, 443, 8080, 6443, 179` — from the **border SG** (an SG-to-SG reference, so it
  holds regardless of which node hosts a VIP); egress all. No `0.0.0.0/0` ingress.

**DNS — Route 53 (`dns.tf`):** a private hosted zone **`demo.internal`**
(`var.internal_domain`), VPC-associated, resolved by the VPC resolver on every node. Holds
per-node A records (`cp1–3`, `w1–3`, `border`) and **`k8s.demo.internal`** — a round-robin
A over all 3 control-plane IPs, the **HA kube API endpoint** (the AWS analog of bhyve's
`k8s.homelab.lan`; replaces the former single-cp1-IP SPOF). It doesn't resolve outside the
VPC — external API access is via the SSH tunnel (see Access).

## Access
Nodes have no public inbound; everything enters through the border EIP. Off-VPC access:
one SSH session through the border with two `-L` forwards (**local port = service port +
10000**), plus a separate ProxyJump for node SSH.

- **Nodes (SSH):** ProxyJump — `ssh -J ubuntu@<border-EIP> <node-priv-ip>` (the emitted
  Ansible inventory bakes this into `ansible_ssh_common_args`).

- **kube API (`16443`→`6443`) + registry (`15000`→`5000`) — one SSH command:**
  ```bash
  sudo ip addr add 10.0.10.101/32 dev lo   # once: registry cert is IP-SAN-pinned, so the
                                           # local end of the 15000 forward must BE 10.0.10.101
  ssh -fN -L 16443:10.0.10.101:6443 -L 10.0.10.101:15000:10.0.10.101:5000 \
      -J ubuntu@pok.somegroup.net ubuntu@10.0.10.101
  ```
  - **kube API:** in-cluster the endpoint is `k8s.demo.internal:6443` (Route 53 round-robin
    over all 3 CPs — HA). `aws-kubeconfig` points at `server: https://127.0.0.1:16443` +
    `tls-server-name: k8s.demo.internal` (that name + the 3 CP IPs are in the apiserver cert
    SANs via `apiserver_cert_extra_sans`). Use `KUBECONFIG=tf/aws/aws-kubeconfig kubectl …`
    or the `tf/k8s` apply. If cp1 is down, retarget the tunnel at `10.0.10.102`/`.103`.
  - **Creating `aws-kubeconfig`** (once, after the control plane is up — nothing generates it
    for you): pull cp1's admin config through the jump host, then retarget it at the tunnel:
    ```bash
    ssh -J ubuntu@pok.somegroup.net ubuntu@10.0.10.101 'sudo cat /etc/kubernetes/admin.conf' > tf/aws/aws-kubeconfig
    kubectl --kubeconfig tf/aws/aws-kubeconfig config set-cluster kubernetes \
      --server https://127.0.0.1:16443 --tls-server-name k8s.demo.internal
    ```
  - **registry (build-time):** port `15000` avoids this build host's own bhyve registry on
    `:5000`. Trust `registry/certs/aws/ca.crt` once, then **push** to `10.0.10.101:15000`
    and **deploy** referencing `10.0.10.101:5000` — same backend, nodes pull in-VPC over
    `:5000`. (See `apps/hw` for the ko build.)

## LB data-path & failover
The VPC fabric can't ECMP a prefix (one route = one ENI), so the `cilium_lb` route points
at a **single** node ENI — a SPOF. The border's **lb-failover** agent
(`border/lb-failover.sh`, `border/lb-failover.service`) watches FRR's BGP sessions and
`ec2:ReplaceRoute`s `10.0.20.0/24` onto a live node whenever the current target's session
drops — active/standby with automatic failover. Full rationale: docs/README.md **§5.2**.

## Provider choice — `hashicorp/aws` only

The source repo implements the same infra twice to compare the two AWS providers.
We use **`aws` only** (no `awscc`):

| | `hashicorp/aws` (chosen) | `hashicorp/awscc` |
|---|---|---|
| Origin | hand-written, mature | auto-generated from the AWS Cloud Control / CloudFormation schemas |
| Resource names | `aws_vpc`, `aws_instance` | `awscc_ec2_vpc`, `awscc_ec2_instance` (CFN type names) |
| Tags | map `{k=v}` | list `[{key,value}]` — **re-orders every apply → phantom diffs** |
| `import` | works | **does not work** (per source repo) |
| Data sources | rich (AMI/AZ/VPC lookups) | thin — still needs `aws` for `data "aws_ami"` |
| New AWS features | can lag until schema is written | same-day (generated) |
| default_tags | yes | no |

The providers *can* be mixed in one config if we ever hit a resource `aws` doesn't cover.

## Prerequisites
- **OpenTofu** ≥ 1.12 (proven: v1.12.5). Providers: `hashicorp/aws ~> 6.0` +
  `hashicorp/local ~> 2.0`; versions pinned in `.terraform.lock.hcl` (commit it).
- **AWS credentials in the environment** — `AWS_PROFILE`, static keys, or SSO. None are
  stored in this repo. Confirm: `aws sts get-caller-identity`.
- An **SSH public key**; `ssh_public_key_filename` is **required** (no default) — set it
  in `terraform.tfvars.json`.
- `cp terraform.tfvars.json.example terraform.tfvars.json` and edit (gitignored).

## Usage
Via the top-level `Makefile` (`make infra`), or directly:
```bash
cd tf/aws
tofu init                    # installs providers, writes .terraform.lock.hcl
tofu plan                    # review (drift check on the live cluster)
tofu apply                   # provisions/updates; also emits inventory + tfvars
tofu output                  # see below
make teardown                # destroy everything EXCEPT the border EIP
```
Outputs: `border_public_ip_address`, `control_plane_private_ips`, `worker_private_ips`,
`cilium_lb_subnet_cidr`, `ansible_inventory_path`.

> A plain `tofu destroy` **errors by design** on the EIP (`prevent_destroy`). Use
> `make teardown` (= `destroy-keep-eip` = `tofu destroy -exclude=module.border.aws_eip.vm[0]`)
> so the border's public IP — and `pok.somegroup.net` — survive the rebuild; re-`apply`
> re-associates the same allocation.

## Files
| File | Declares |
|------|----------|
| `versions.tf` | `required_version`; `hashicorp/aws ~> 6.0` + `hashicorp/local ~> 2.0`; S3-backend TODO |
| `providers.tf` | `provider "aws"` (region + `default_tags`); creds from env |
| `variables.tf` | region, az, environment, cidr, `internal_domain`, ssh key, instance types/counts, Caddy proxy config |
| `network.tf` | VPC, internet gateway, route table, default route, gateway attachment |
| `dns.tf` | Route 53 private zone `demo.internal` — per-node A records + `k8s.` HA API endpoint |
| `border.tf` | border subnet + SG (80/443 always, 22 toggleable via `border_allow_ssh`, 179 from internal) + border module (cloud-init) |
| `internal.tf` | internal subnet + SG + the `control_plane` and `worker` node pools |
| `cilium-lb.tf` | `cilium_lb` subnet + the `10.0.20.0/24 → cp1 ENI` route |
| `iam-border.tf` | IAM role/policy/instance-profile for the border's LB-failover agent |
| `ssh.tf` | `aws_key_pair` from the public key |
| `k8s-vars.tf` | emits `../k8s/vars/aws.tfvars.json` (BGP ASNs, LB prefix, registry, service host) |
| `ansible-inventory.tf` | emits `ansible/inventory/aws/hosts.ini` from the real IPs + border EIP |
| `outputs.tf` | border public IP, node private IPs, LB CIDR |
| `Makefile` | `init/plan/apply/teardown` (provider-agnostic entrypoints) |
| `vm/` | reusable instance module (AMI lookup, instance, EIP alloc+assoc split, root + data volumes) |
| `border/` | `lb-failover.sh` (BGP-driven route failover) + `lb-failover.service` |
| `templates/` | `border-cloud-init.yaml.tftpl` (Caddy+FRR+agent), `ansible-hosts.ini.tftpl` |
