# Emits the bhyve Ansible inventory (ansible/inventory/bhyve/hosts.ini) from the
# hardcoded node list in inventory.tf. A stub root — no cloud infra, just local_file.

.PHONY: init apply plan teardown fmt validate generate

# Auto-init guard: targets that need the providers depend on `.terraform` (order-only), so a
# fresh checkout / the public mirror runs `tofu init` instead of failing on missing plugins.
# Keyed on `.terraform/`, not `*.tfstate` (absent state means "not applied yet", not "not init").
.terraform:
	tofu init

init:
	tofu init

apply: | .terraform
	tofu apply

# Regenerate the generated inventory + tf/k8s var-file (bhyve is a stub — local_file resources only,
# no cloud infra). Matches the aws `generate` target so top-level `make generate`/`services` is uniform.
generate: | .terraform
	tofu apply -target=local_file.k8s_tfvars -target=local_file.inventory -auto-approve

# No-op: bhyve infra is vm-bhyve (external), not tofu-managed — nothing to destroy.
# (The cluster teardown itself is the Ansible kubeadm-reset in the top-level Makefile.)
# The generated inventory is left in place.
teardown:
	@echo "tf/bhyve: nothing to tear down (vm-bhyve infra is external; inventory left in place)"

plan: | .terraform
	tofu plan

fmt:
	tofu fmt -recursive

validate: | .terraform
	tofu validate
