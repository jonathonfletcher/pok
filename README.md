# pok: Proof of K8s Demo

Parameterized IaC (Ansible + OpenTofu) plus a demo application for a **kubeadm Kubernetes
cluster and its in-cluster services across multiple infrastructure providers**.

Two providers:
- **aws** — arm64 EC2 instances in a purpose-built VPC.
- **bhyve** — home lab: Ubuntu VMs on a FreeBSD/`vm-bhyve` host, behind an OpenBSD gateway.

## Demo site

- **aws** — [pok.somegroup.net](https://pok.somegroup.net)

Detailed architecture + per-layer walkthrough: **[docs/README.md](docs/README.md)**.

## Usage

With AWS credentials and a Honeycomb API key in place:

```
make use-bhyve|use-aws   # set the active provider (writes platform.mk; default aws)
make infra               # OpenTofu: provision tf/$(INFRA_PROVIDER) + emit the Ansible inventory
make cluster             # Ansible: node OS prep -> kubeadm init/joins -> storage
make services            # OpenTofu: in-cluster services (Cilium, registry, TopoLVM, OTel, Kafka)
make trust               # trust the generated registry CA on the build host (sudo)
make app                 # ko build + push + deploy all apps (apps/)
make verify              # nodes + tofu drift + app reachability
make teardown            # tear down infra (aws keeps the border EIP; bhyve resets the cluster)
```

CI (`.github/workflows/`): a hosted lint/build/test gate on push/PR, and a self-hosted
`ko` build+push+deploy job (the private registry and cluster API are LAN/VPC-only).

## Cluster and services

Cluster:
- kubeadm, 3 control-plane nodes + 3 (aws) / 7 (bhyve) workers.
- Cilium CNI (kube-proxy replacement, BGP control-plane).
- TopoLVM CSI, per-node storage (workers on aws; all nodes on bhyve).
- Helm for packaging; namespaces per service.

Services (`tf/k8s`, one parameterized root for both providers):
- OpenTelemetry collectors (per-node agent + cluster) exporting to Honeycomb; metrics-server
  for the Metrics API.
- L3 LoadBalancer via Cilium BGP. On bhyve the OpenBSD gateway does active/active ECMP; on aws
  the VPC cannot ECMP, so a systemd BGP-watcher does active/standby route failover (a
  demo-grade substitute for a managed NLB).
- Strimzi Kafka (KRaft) — a KafkaNodePool cluster, each broker on its own CSI volume.
- Cilium default-deny network policies for the app namespaces.

Application (`apps/`, all Go, built with `ko`, all OTel-traced):
- **helloworld** — sample HTTP app; publishes a `url-visits` event per request to Kafka.
- **static** — serves the frontend (styles reused from emf.somegroup.net).
- **clusterinfo** — publishes one cluster aspect per tick to Kafka (nodes, pods, Cilium state,
  node/pod metrics, …).
- **visitcounter** — aggregates `url-visits` into the compacted `visit-counts` topic.
- **wsfeed** — consumes `cluster-info` + `visit-counts`, streams to browsers over a WebSocket.
- **edge** — reverse-proxy front door (routes `/`, `/static`, `/hw`, `/ws`), holds the apps-pool VIP.

Related components:
- **containerd** on nodes; images built with **ko** (no Docker).
- Private registry (`registry:2`) with self-signed PKI, same approach on both providers (aws
  would normally use Elastic Container Registry).
- Edge reverse proxy: **Caddy** on aws, **nginx** on bhyve.

Not implemented (demo scope):
- EKS Standard - used kubeadm on both providers for consistency.
- Elastic Container Registry, managed Network Load Balancer, example Lambda.

## Attribution and foundations

- **@claude used to accelerate learning and development.**
- Cluster build derived from a hand-built setup following
  [kubernetes.io/docs/tutorials/kubernetes-basics](https://kubernetes.io/docs/tutorials/kubernetes-basics/).
- AWS IaC based on [codeberg.org/jonathonfletcher/simple-aws-awscc-tf](https://codeberg.org/jonathonfletcher/simple-aws-awscc-tf).
- Frontend components reused from [emf.somegroup.net](https://emf.somegroup.net) (idle after
  [www.emfcamp.org](https://www.emfcamp.org)).

Notes:
- `imagePullPolicy: Always` on the apps — the in-cluster registry makes re-pull cheap and CI
  tags images per commit; `IfNotPresent` is the alternative with immutable version tags.
- `tf/bhyve` is an inventory stub (the VMs pre-exist on the host); `tf/aws` provisions.
- tofu state is local-only in this demo. not shared with locking backend.