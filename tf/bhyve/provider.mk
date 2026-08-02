# Provider variables for INFRA_PROVIDER=bhyve, consumed by the top-level Makefile
# (`include tf/$(INFRA_PROVIDER)/provider.mk`). See tf/aws/provider.mk for the format + the
# repo-root cwd note. Values match the apps/ Makefile defaults, so `make app` is a no-op override.

KCFG          := $(HOME)/.kube/config
REGISTRY      := registry.homelab.lan:5000         # DNS name on the bhyve LAN
PUSH_REGISTRY := registry.homelab.lan:5000
PLATFORM      := linux/amd64
REGISTRY_CA   := ../../registry/certs/bhyve/ca.crt   # relative to apps/helloworld
VERIFY_URL    := http://172.23.20.240/
MANUAL_GATE       := yes                             # DNS-zone install on the gateway + disk0 grow
RESET_ON_TEARDOWN := yes                             # bhyve infra is external -> teardown = cluster reset
