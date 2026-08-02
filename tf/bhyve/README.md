# `tf/bhyve` — bhyve inventory + tfvars emitter (stub)

The bhyve cluster's machines are **vm-bhyve** guests on a FreeBSD host — not managed
by tofu. This root is a **stub**: it emits the two per-provider artifacts the
layers above consume, so both environments share one workflow — *tofu produces the
inventory (+ tfvars)* (bhyve from hardcoded values here; AWS from real resources in
`tf/aws`). No vm-bhyve/cloud provider is wired; only `hashicorp/local`. It emits:
1. the Ansible **inventory** (`../../ansible/inventory/bhyve/hosts.ini`), and
2. the per-provider **tfvars** for the shared `tf/k8s` root (`../k8s/vars/bhyve.tfvars.json`).

- `inventory.tf` — the hardcoded node addresses (static DHCP reservations) + a
  `local_file` rendering `../../ansible/inventory/bhyve/hosts.ini`.
- `k8s-vars.tf` — a `local_file` rendering `../k8s/vars/bhyve.tfvars.json` (Cilium devices,
  API endpoint, LB prefix, BGP ASNs/peer, registry node/certs) — consumed by `tf/k8s` via
  `-var-file`.
- `templates/hosts.ini.tftpl` — the inventory template (direct LAN, no bastion).

## Use
```bash
cd tf/bhyve && tofu init && tofu apply    # writes ansible/inventory/bhyve/hosts.ini
                                          #    + ../k8s/vars/bhyve.tfvars.json
```
The generated inventory is gitignored (regenerated). Edit node addresses in
`inventory.tf`, not the generated file. There is no default inventory (`ansible/ansible.cfg`
sets none) — the top-level `Makefile` selects it per provider with
`-i inventory/$(INFRA_PROVIDER)/hosts.ini`.
