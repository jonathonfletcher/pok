# Private image registry — `registry.homelab.lan:5000` (bhyve) / `10.0.10.101:5000` (aws)

CNCF **Distribution** (`registry:2`), TLS with a self-signed CA. Nodes pull over TLS;
images are pushed with `ko build --push`. Provider-generic: deployed by OpenTofu
(`tf/k8s/registry.tf`, count-guarded on `var.manage_registry`), pinned to the node named
by `var.registry_node` — **bhyve** k8sm1 @ 172.23.10.150, **aws** cp1 @ 10.0.10.101. Its
per-provider PKI and node-trust are Ansible roles.

> **The app build/push/deploy flow lives in [../apps/helloworld/README.md](../apps/helloworld/README.md), not here.**
> This doc is only about the registry itself.

## Architecture (what runs where)

- **Registry pod:** Deployment `registry/registry`, pinned to `var.registry_node`
  (`nodeSelector` + control-plane toleration), `hostNetwork` → binds `<node-ip>:5000`
  directly (**no Service**). Managed by OpenTofu — `tf/k8s/registry.tf`. bhyve:
  172.23.10.150 (k8sm1); aws: 10.0.10.101 (cp1).
- **Image storage:** a **hostPath** (`/var/lib/registry`, `DirectoryOrCreate`) on
  `var.registry_node`. Not a TopoLVM PVC: the registry pins to a control-plane node, and on
  aws the control planes have no TopoLVM VG (workers only). Blobs survive pod restarts but
  **not** loss of the pinned node; they are rebuildable (`make app`).
- **TLS:** server cert delivered as the `registry/registry-tls` Secret; source certs in
  **`registry/certs/<provider>/`** (gitignored). bhyve SAN `DNS:registry.homelab.lan,IP:172.23.10.150`;
  aws is **IP-only** `IP:10.0.10.101` (no DNS name).
- **DNS (bhyve only):** `registry.homelab.lan  A  172.23.10.150` (in the `homelab.lan`
  zone). aws has no DNS name — the registry is addressed by cp1's IP.

## How it's stood up (all automated by the top-level `make` flow)

| Piece | Built by |
|---|---|
| CA + server cert → `registry/certs/<provider>/` | `kube_registry_pki` role (`prepare.yml`) |
| Registry Deployment / Secret / namespace | `tf/k8s/registry.tf` (`make services`) |
| Each node's containerd trust (`certs.d`) | `kube_registry_trust` role (`bootstrap.yml`) |
| Build-host CA trust (for `ko`) | `make -C apps/helloworld trust` (once per build host) |
| DNS `registry.homelab.lan → .150` (bhyve only) | `infra_dns_zone` (installed on the gateway) |

> **Critical gotcha (containerd 2.2.1):** the default registry `config_path` is NOT
> honored by the CRI image plugin at runtime. It must be set **explicitly** in
> `/etc/containerd/config.toml` (the `kube_containerd` role writes `version = 3` +
> `config_path = /etc/containerd/certs.d`) **and containerd restarted** (the
> `kube_registry_trust` role does the restart). Symptom if skipped: kubelet pulls fail
> `x509: certificate signed by unknown authority`, while
> `ctr --hosts-dir /etc/containerd/certs.d pull` works.
>
> The CA cert **must** carry `basicConstraints: critical, CA:TRUE` (openssl `-x509`
> adds it) — Go/containerd reject a non-CA signer even though curl accepts it.

## Verify end-to-end

The commands below are **bhyve-specific** (DNS name `registry.homelab.lan`, node `k8sw3`);
on aws use the IP `10.0.10.101:5000` and an aws worker (`w1`–`w3`).

```bash
curl -s https://registry.homelab.lan:5000/v2/ ; echo          # TLS + API up (system trust)
cd apps/helloworld && make image TAG=probe-$RANDOM               # push a throwaway tag
kubectl run rp --image=registry.homelab.lan:5000/helloworld:probe-XXXX --restart=Never \
  --overrides='{"spec":{"nodeName":"k8sw3"}}'                  # pull via a real pod (kubelet CRI)
kubectl get pod rp -o wide      # expect Running; describe shows "Successfully pulled"
kubectl delete pod rp
```

## Maintenance

- **Add a node:** the `kube_registry_trust` role handles it (re-run node-prep on it).
- **Rotate the server cert:** regenerate `tls.crt`/`tls.key` (re-run `kube_registry_pki`),
  `make services` to update the Secret, restart the registry pod. If the **CA** changes,
  re-run `kube_registry_trust` on every node and `make -C apps/helloworld trust` on the build host.
- **Garbage collection** (Distribution has no auto-GC): delete a tag's manifest via the
  API, then reclaim space with `registry garbage-collect /etc/docker/registry/config.yml`
  inside the pod.
- **Storage / backup:** blobs live on `hostPath /var/lib/registry` on the registry node
  (bhyve k8sm1 / aws cp1) — back that up if the images matter. Not a TopoLVM PVC: the
  registry pins to a control-plane node, and on aws the control planes have no TopoLVM VG
  (workers only). Losing the node loses the blobs; re-push with `make app` (images are
  rebuildable).

## Auth

None. The registry is reachable only on the cluster network (bhyve LAN / aws VPC internal
subnet), never externally, so it runs without basic-auth. TLS is still used (server cert
below); the CA is trusted per node out-of-band.

## Under the hood — cert generation (what the `kube_registry_pki` role runs)

CN/SAN are per-provider (`kube_registry_pki_cn` / `..._san` in `group_vars`); values below
are **bhyve** — aws uses `/CN=10.0.10.101` and `subjectAltName=IP:10.0.10.101` (IP-only).

```bash
openssl genrsa -out ca.key 4096
openssl req -x509 -new -key ca.key -sha256 -days 3650 -out ca.crt -subj "/CN=homelab-registry-ca"
openssl genrsa -out tls.key 2048
openssl req -new -key tls.key -out tls.csr -subj "/CN=registry.homelab.lan"   # bhyve; aws /CN=10.0.10.101
# san.ext (bhyve): subjectAltName=DNS:registry.homelab.lan,IP:172.23.10.150    # aws: IP:10.0.10.101
#          extendedKeyUsage=serverAuth; keyUsage=digitalSignature,keyEncipherment
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial -out tls.crt -days 825 -sha256 -extfile san.ext
```

## Files

```
tf/k8s/registry.tf                     registry Deployment + Secret + namespace (OpenTofu)
registry/certs/<provider>/         ca.{crt,key}, tls.{crt,key} — per-provider, GITIGNORED (from kube_registry_pki)
ansible/roles/kube_registry_pki    generates the CA + TLS cert (control node)
ansible/roles/kube_registry_trust  per-node containerd trust (certs.d)
```
