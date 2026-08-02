# Provider variables for INFRA_PROVIDER=aws, consumed by the top-level Makefile
# (`include tf/$(INFRA_PROVIDER)/provider.mk`). Static/committed — the top Makefile reads this
# before `make infra`, so it can't be tofu-emitted like the inventory/tfvars.
#
# NOTE: values are used by the top Makefile's recipes, so relative paths are relative to the
# REPO ROOT (or to the sub-make's dir where noted), NOT to tf/aws/.

KCFG          := $(abspath tf/aws/aws-kubeconfig)  # API + registry reached via the SSH tunnel
REGISTRY      := 10.0.10.101:5000                  # nodes pull from cp1 in-VPC
PUSH_REGISTRY := 10.0.10.101:15000                 # build host pushes via the tunnel forward
PLATFORM      := linux/arm64                        # Graviton
REGISTRY_CA   := ../../registry/certs/aws/ca.crt    # relative to apps/helloworld (the trust sub-make)
VERIFY_URL    := https://pok.somegroup.net/
BORDER_GW     := pok.somegroup.net                  # SSH ProxyJump host for the tunnel
MANUAL_GATE       :=                                # empty: no manual gate on aws
RESET_ON_TEARDOWN :=                                # empty: teardown = tofu destroy (keeps EIP)
